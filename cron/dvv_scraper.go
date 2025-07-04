package main

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/nilsimda/gbt-head2head/db"
	"github.com/nilsimda/gbt-head2head/models"
)

type Config struct {
	BaseURL          string
	ConcurrencyLimit int
	RequestTimeout   time.Duration
	RetryDelay       time.Duration
	RateLimitDelay   time.Duration
	SeasonStart      int
	SeasonEnd        int
}

type DVVScraper struct {
	config     Config
	httpClient *http.Client
	db         *db.BeachVolleyballDB
	logger     *log.Logger
}

type ScrapingResults struct {
	Tournaments []models.Tournament
	Matches     []models.Match
	Teams       []models.Team
}

func NewDVVScraper(dbPath string) (*DVVScraper, error) {
	bvDB, err := db.NewBeachVolleyballDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create database: %w", err)
	}

	currentYear := time.Now().Year()
	seasonEnd := currentYear % 100 // Convert 2025 -> 25, 2026 -> 26, etc.

	config := Config{
		BaseURL:          "https://beach.volleyball-verband.de/public/",
		ConcurrencyLimit: 15,
		RequestTimeout:   30 * time.Second,
		RetryDelay:       1 * time.Second,
		RateLimitDelay:   100 * time.Millisecond,
		SeasonStart:      3,
		SeasonEnd:        seasonEnd,
	}

	httpClient := &http.Client{
		Timeout: config.RequestTimeout,
		Transport: &http.Transport{
			TLSHandshakeTimeout: 10 * time.Second,
			IdleConnTimeout:     30 * time.Second,
			MaxIdleConns:        10,
		},
	}

	return &DVVScraper{
		config:     config,
		httpClient: httpClient,
		db:         bvDB,
		logger:     log.New(log.Writer(), "[DVV-Scraper] ", log.LstdFlags),
	}, nil
}

func (s *DVVScraper) Close() error {
	return s.db.Close()
}

func (s *DVVScraper) collectTournamentURLs() (map[string]struct{}, error) {
	tournamentUrls := make(map[string]struct{})
	var mu sync.Mutex
	var wg sync.WaitGroup

	for season := s.config.SeasonStart; season <= s.config.SeasonEnd; season++ {
		wg.Add(1)
		go func(season int) {
			defer wg.Done()
			s.getTournamentURLsForSeason(season, tournamentUrls, &mu)
		}(season)
	}

	wg.Wait()
	return tournamentUrls, nil
}

func (s *DVVScraper) getTournamentURLsForSeason(season int, tournamentUrls map[string]struct{}, mu *sync.Mutex) {
	url := s.config.BaseURL + "tur.php?kat=1&bytyp=0&saison=" + strconv.Itoa(season) + "#"
	resp, err := s.httpClient.Get(url)
	if err != nil {
		s.logger.Printf("Error fetching season %d: %v", season, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.logger.Printf("Bad status code for season %d: %d", season, resp.StatusCode)
		return
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		s.logger.Printf("Error parsing season %d HTML: %v", season, err)
		return
	}

	linkIndex := 0
	table := doc.Find("table .contenttable").First()
	table.Find("a").Each(func(rowIndex int, sel *goquery.Selection) {
		if rowIndex != 0 && linkIndex%2 == 1 {
			if href, exists := sel.Attr("href"); exists {
				mu.Lock()
				tournamentUrls[href] = struct{}{}
				mu.Unlock()
			}
		}
		linkIndex++
	})
}

func (s *DVVScraper) extractTournamentData(tournamentURL string) models.Tournament {
	fullURL := s.config.BaseURL + tournamentURL
	resp, err := s.httpClient.Get(fullURL)
	if err != nil {
		time.Sleep(s.config.RetryDelay)
		resp, err = s.httpClient.Get(fullURL)
		if err != nil {
			s.logger.Printf("Error fetching tournament %s: %v", tournamentURL, err)
			return models.Tournament{}
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.logger.Printf("Bad status code for tournament %s: %d", tournamentURL, resp.StatusCode)
		return models.Tournament{}
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		s.logger.Printf("Error parsing tournament HTML %s: %v", tournamentURL, err)
		return models.Tournament{}
	}

	tournament := models.Tournament{
		DVVURL: fullURL,
	}

	// Extract title from page header
	pageHeader := doc.Find("p.pageheader").First().Text()
	tournament.Title = strings.TrimSpace(pageHeader)

	// Extract year from title using regex
	yearRegex := regexp.MustCompile(`20\d{2}`)
	yearMatch := yearRegex.FindString(tournament.Title)
	if yearMatch != "" {
		if year, err := strconv.Atoi(yearMatch); err == nil {
			tournament.Year = year
		}
	}

	// Extract tournament ID from URL
	idRegex := regexp.MustCompile(`id=(\d+)`)
	idMatch := idRegex.FindStringSubmatch(tournamentURL)
	if len(idMatch) > 1 {
		tournament.ID = idMatch[1]
	}

	// Extract data from the table rows
	doc.Find("table tr").Each(func(i int, s *goquery.Selection) {
		cells := s.Find("td")
		if cells.Length() >= 2 {
			label := strings.TrimSpace(cells.Eq(0).Text())
			value := strings.TrimSpace(cells.Eq(1).Text())

			switch label {
			case "Datum von":
				tournament.DateFrom = value
			case "Datum bis":
				tournament.DateTo = value
			case "Geschlecht":
				tournament.Gender = value
			case "Typ":
				tournament.Type = value
			case "Ort":
				tournament.Location = value
			case "Ausrichter":
				tournament.Organizer = value
			case "Gelände":
				tournament.Venue = value
			case "Preisgeld":
				tournament.PrizeMoney = value
			case "Teams Hauptfeld":
				if teams, err := strconv.Atoi(value); err == nil {
					tournament.TeamsHauptfeld = teams
				}
			case "Teams Qualifikation":
				if teams, err := strconv.Atoi(value); err == nil {
					tournament.TeamsQualifikation = teams
				}
			case "Teams Hauptfeld aus Qualifikation":
				if teams, err := strconv.Atoi(value); err == nil {
					tournament.TeamsFromQualifikation = teams
				}
			}
		}
	})

	// Extract game links for Qualifikation and Hauptfeld
	doc.Find("table").Each(func(i int, table *goquery.Selection) {
		firstCell := table.Find("tr td").First().Text()
		if strings.Contains(firstCell, "Qualifikation:") {
			table.Find("a").Each(func(j int, a *goquery.Selection) {
				href, exists := a.Attr("href")
				if exists && strings.Contains(href, "tur-sp.php") {
					tournament.QualifikationGamesURL = s.config.BaseURL + href
				}
			})
		} else if strings.Contains(firstCell, "Hauptfeld:") {
			table.Find("a").Each(func(j int, a *goquery.Selection) {
				href, exists := a.Attr("href")
				if exists && strings.Contains(href, "tur-sp.php") {
					tournament.HauptfeldGamesURL = s.config.BaseURL + href
				}
			})
		}
	})

	return tournament
}

func (s *DVVScraper) extractMatchesFromGamesURL(gamesURL string, tournamentID string, fieldType string) []models.Match {
	if gamesURL == "" {
		return nil
	}

	time.Sleep(s.config.RateLimitDelay)

	resp, err := s.httpClient.Get(gamesURL)
	if err != nil {
		time.Sleep(s.config.RetryDelay)
		resp, err = s.httpClient.Get(gamesURL)
		if err != nil {
			s.logger.Printf("Error fetching games %s: %v", gamesURL, err)
			return nil
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.logger.Printf("Bad status code for games %s: %d", gamesURL, resp.StatusCode)
		return nil
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		s.logger.Printf("Error parsing games HTML %s: %v", gamesURL, err)
		return nil
	}

	var matches []models.Match
	currentRound := ""

	// Find all section headers and game tables
	doc.Find(".sectionheader, table").Each(func(i int, s *goquery.Selection) {
		if s.HasClass("sectionheader") {
			currentRound = strings.TrimSpace(s.Text())
		} else if s.Is("table") && s.Find("tr.bez2").Length() > 0 {
			// Verify this is actually a match table by checking headers
			headerRow := s.Find("tr.bez2").First()
			headerText := headerRow.Text()
			if !strings.Contains(headerText, "Spiel") || !strings.Contains(headerText, "Team") {
				return // Not a match table
			}

			s.Find("tr").Each(func(j int, tr *goquery.Selection) {
				// Skip header row
				if tr.HasClass("bez2") {
					return
				}

				cells := tr.Find("td")
				if cells.Length() < 8 {
					return
				}

				// Validate this is actually match data by checking first cell is a number
				matchNumStr := strings.TrimSpace(cells.Eq(0).Text())
				if _, err := strconv.Atoi(matchNumStr); err != nil {
					return // First cell should be match number
				}

				match := models.Match{
					TournamentID: tournamentID,
					FieldType:    fieldType,
					Round:        currentRound,
					DVVURL:       gamesURL,
				}

				// Parse match data (matchNumStr already validated above)
				if matchNum, err := strconv.Atoi(matchNumStr); err == nil {
					match.MatchNumber = matchNum
				}

				match.Date = strings.TrimSpace(cells.Eq(1).Text())
				match.Time = strings.TrimSpace(cells.Eq(2).Text())

				courtStr := strings.TrimSpace(cells.Eq(3).Text())
				if court, err := strconv.Atoi(courtStr); err == nil {
					match.Court = court
				}

				// Extract team information
				team1Cell := cells.Eq(4)
				team2Cell := cells.Eq(6)

				team1Link := team1Cell.Find("a")
				team2Link := team2Cell.Find("a")

				if team1Link.Length() > 0 {
					match.Team1.Name = strings.TrimSpace(team1Link.Text())
					if href, exists := team1Link.Attr("href"); exists && strings.Contains(href, "team.php") {
						match.Team1.DVVURL = href
						// Extract team ID from URL
						idRegex := regexp.MustCompile(`id=(\d+)`)
						if idMatch := idRegex.FindStringSubmatch(href); len(idMatch) > 1 {
							match.Team1.ID = idMatch[1]
						}
					}
					// Extract seed from team name (numbers in parentheses)
					seedRegex := regexp.MustCompile(`\((\d+)\)`)
					if seedMatch := seedRegex.FindStringSubmatch(match.Team1.Name); len(seedMatch) > 1 {
						if seed, err := strconv.Atoi(seedMatch[1]); err == nil {
							match.Team1.Seed = seed
						}
					}
				}

				if team2Link.Length() > 0 {
					match.Team2.Name = strings.TrimSpace(team2Link.Text())
					if href, exists := team2Link.Attr("href"); exists && strings.Contains(href, "team.php") {
						match.Team2.DVVURL = href
						// Extract team ID from URL
						idRegex := regexp.MustCompile(`id=(\d+)`)
						if idMatch := idRegex.FindStringSubmatch(href); len(idMatch) > 1 {
							match.Team2.ID = idMatch[1]
						}
					}
					// Extract seed from team name
					seedRegex := regexp.MustCompile(`\((\d+)\)`)
					if seedMatch := seedRegex.FindStringSubmatch(match.Team2.Name); len(seedMatch) > 1 {
						if seed, err := strconv.Atoi(seedMatch[1]); err == nil {
							match.Team2.Seed = seed
						}
					}
				}

				// Determine winner by font-weight:bold
				team1Style, _ := team1Cell.Attr("style")
				team2Style, _ := team2Cell.Attr("style")

				if strings.Contains(team1Style, "font-weight:bold") {
					match.WinnerTeamID = match.Team1.ID
					match.LoserTeamID = match.Team2.ID
				} else if strings.Contains(team2Style, "font-weight:bold") {
					match.WinnerTeamID = match.Team2.ID
					match.LoserTeamID = match.Team1.ID
				}

				// Extract score
				scoreCell := cells.Eq(7)
				scoreLink := scoreCell.Find("a")
				if scoreLink.Length() > 0 {
					match.Score = strings.TrimSpace(scoreLink.Text())
				} else {
					match.Score = strings.TrimSpace(scoreCell.Text())
				}

				// Extract duration
				if cells.Length() > 8 {
					durationText := strings.TrimSpace(cells.Eq(8).Text())
					for part := range strings.SplitSeq(durationText, ",") {
						part = strings.TrimSpace(part)
						if duration, err := strconv.Atoi(part); err == nil {
							match.Duration = append(match.Duration, duration)
						}
					}
				}

				// Extract placement
				if cells.Length() > 9 {
					match.Placement = strings.TrimSpace(cells.Eq(9).Text())
				}

				// Only add match if both teams have valid data
				if match.Team1.Name != "" && match.Team2.Name != "" &&
					match.Team1.ID != "" && match.Team2.ID != "" {
					match.ID = fmt.Sprintf("%s_%s_%d", tournamentID, fieldType, match.MatchNumber)
					matches = append(matches, match)
				}
			})
		}
	})

	return matches
}

func (s *DVVScraper) extractTeamData(teamURL string) models.Team {
	if teamURL == "" {
		return models.Team{}
	}

	time.Sleep(s.config.RateLimitDelay)

	fullURL := s.config.BaseURL + teamURL
	resp, err := s.httpClient.Get(fullURL)
	if err != nil {
		time.Sleep(s.config.RetryDelay)
		resp, err = s.httpClient.Get(fullURL)
		if err != nil {
			s.logger.Printf("Error fetching team %s: %v", teamURL, err)
			return models.Team{}
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.logger.Printf("Bad status code for team %s: %d", teamURL, resp.StatusCode)
		return models.Team{}
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		s.logger.Printf("Error parsing team HTML %s: %v", teamURL, err)
		return models.Team{}
	}

	team := models.Team{
		DVVURL: fullURL,
	}

	// Extract team ID from URL
	idRegex := regexp.MustCompile(`id=(\d+)`)
	if idMatch := idRegex.FindStringSubmatch(teamURL); len(idMatch) > 1 {
		team.ID = idMatch[1]
	}

	// Extract team name from page header
	pageHeader := doc.Find("p.pageheader").First().Text()
	team.Name = strings.TrimSpace(pageHeader)

	// Extract player information from the table
	playerLinks := doc.Find("table tr").FilterFunction(func(i int, s *goquery.Selection) bool {
		return s.Find("td").First().Text() == "Name, Vorname"
	}).Find("a")

	playerLinks.Each(func(i int, playerLink *goquery.Selection) {
		playerName := strings.TrimSpace(playerLink.Text())
		playerHref, exists := playerLink.Attr("href")

		if !exists {
			return
		}

		// Parse player name (format: "LastName, FirstName")
		nameParts := strings.Split(playerName, ",")
		var firstName, lastName string
		if len(nameParts) >= 2 {
			lastName = strings.TrimSpace(nameParts[0])
			firstName = strings.TrimSpace(nameParts[1])
		} else {
			lastName = playerName
		}

		// Extract player ID from URL
		playerIDRegex := regexp.MustCompile(`id=(\d+)`)
		var playerID string
		if playerIDMatch := playerIDRegex.FindStringSubmatch(playerHref); len(playerIDMatch) > 1 {
			playerID = playerIDMatch[1]
		}

		player := models.Player{
			ID:        playerID,
			FirstName: firstName,
			LastName:  lastName,
			DVVURL:    s.config.BaseURL + playerHref,
		}

		// Extract license ID from the license number row
		licenseRow := doc.Find("table tr").FilterFunction(func(j int, tr *goquery.Selection) bool {
			return tr.Find("td").First().Text() == "Lizenznummer"
		})

		if licenseRow.Length() > 0 {
			licenseCells := licenseRow.Find("td")
			if i == 0 && licenseCells.Length() > 1 {
				player.LicenseID = strings.TrimSpace(licenseCells.Eq(1).Text())
			} else if i == 1 && licenseCells.Length() > 2 {
				player.LicenseID = strings.TrimSpace(licenseCells.Eq(2).Text())
			}
		}

		// Extract club from the club row
		clubRow := doc.Find("table tr").FilterFunction(func(j int, tr *goquery.Selection) bool {
			return tr.Find("td").First().Text() == "Verein"
		})

		if clubRow.Length() > 0 {
			clubCells := clubRow.Find("td")
			if i == 0 && clubCells.Length() > 1 {
				player.Club = strings.TrimSpace(clubCells.Eq(1).Text())
			} else if i == 1 && clubCells.Length() > 2 {
				player.Club = strings.TrimSpace(clubCells.Eq(2).Text())
			}
		}

		// Assign to team based on position
		switch i {
		case 0:
			team.Player1 = player
			team.Player1ID = playerID
		case 1:
			team.Player2 = player
			team.Player2ID = playerID
		}
	})

	return team
}

func (s *DVVScraper) ScrapeAll() (*ScrapingResults, error) {
	s.logger.Println("Starting tournament URL collection...")
	tournamentUrls, err := s.collectTournamentURLs()
	if err != nil {
		return nil, fmt.Errorf("failed to collect tournament URLs: %w", err)
	}
	s.logger.Printf("Found %d tournament URLs", len(tournamentUrls))

	results := &ScrapingResults{}

	s.logger.Println("Extracting tournament data...")
	if err := s.extractTournaments(tournamentUrls, results); err != nil {
		return nil, fmt.Errorf("failed to extract tournaments: %w", err)
	}
	s.logger.Printf("Successfully extracted and saved %d tournaments", len(results.Tournaments))

	s.logger.Println("Extracting match data...")
	teamURLSet, err := s.extractMatches(results)
	if err != nil {
		return nil, fmt.Errorf("failed to extract matches: %w", err)
	}
	s.logger.Printf("Successfully extracted and saved %d matches", len(results.Matches))

	s.logger.Println("Extracting team data...")
	if err := s.extractTeams(teamURLSet, results); err != nil {
		return nil, fmt.Errorf("failed to extract teams: %w", err)
	}
	s.logger.Printf("Successfully extracted and saved %d teams", len(results.Teams))

	s.printSummary(results)
	return results, nil
}

func (s *DVVScraper) extractTournaments(tournamentUrls map[string]struct{}, results *ScrapingResults) error {
	tournamentChan := make(chan models.Tournament, len(tournamentUrls))
	semaphore := make(chan struct{}, s.config.ConcurrencyLimit)
	var extractWg sync.WaitGroup

	for tournamentURL := range tournamentUrls {
		extractWg.Add(1)
		go func(url string) {
			defer extractWg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Extract basic tournament data first to check the date
			tournament := s.extractTournamentData(url)
			if tournament.Title == "" {
				return
			}

			// Check if we should scrape this tournament based on its end date
			if !s.db.ShouldScrapeTournament(tournament.ID, tournament.DateTo) {
				s.logger.Printf("Tournament %s (%s) not ready for scraping or recently scraped, skipping",
					tournament.ID, tournament.Title)
				return
			}

			s.logger.Printf("Scraping tournament %s (%s)", tournament.ID, tournament.Title)
			tournamentChan <- tournament
		}(tournamentURL)
	}

	extractWg.Wait()
	close(tournamentChan)

	for tournament := range tournamentChan {
		results.Tournaments = append(results.Tournaments, tournament)
		if err := s.db.InsertTournament(tournament); err != nil {
			s.logger.Printf("Failed to insert tournament %s: %v", tournament.ID, err)
		}
	}

	return nil
}

func (s *DVVScraper) extractMatches(results *ScrapingResults) (map[string]struct{}, error) {
	matchChan := make(chan []models.Match, len(results.Tournaments)*2)
	teamURLSet := make(map[string]struct{})
	var matchMu sync.Mutex
	var matchWg sync.WaitGroup
	matchSemaphore := make(chan struct{}, s.config.ConcurrencyLimit)

	for _, tournament := range results.Tournaments {
		if tournament.HauptfeldGamesURL != "" {
			matchWg.Add(1)
			go s.extractMatchesForType(tournament, "Hauptfeld", tournament.HauptfeldGamesURL,
				matchChan, teamURLSet, &matchMu, &matchWg, matchSemaphore)
		}

		if tournament.QualifikationGamesURL != "" {
			matchWg.Add(1)
			go s.extractMatchesForType(tournament, "Qualifikation", tournament.QualifikationGamesURL,
				matchChan, teamURLSet, &matchMu, &matchWg, matchSemaphore)
		}
	}

	matchWg.Wait()
	close(matchChan)

	for matches := range matchChan {
		results.Matches = append(results.Matches, matches...)
		for _, match := range matches {
			if err := s.db.InsertMatch(match); err != nil {
				s.logger.Printf("Failed to insert match %s: %v", match.ID, err)
			}
		}
	}

	return teamURLSet, nil
}

func (s *DVVScraper) extractMatchesForType(tournament models.Tournament, fieldType, gamesURL string,
	matchChan chan<- []models.Match, teamURLSet map[string]struct{},
	matchMu *sync.Mutex, matchWg *sync.WaitGroup, semaphore chan struct{}) {

	defer matchWg.Done()
	semaphore <- struct{}{}
	defer func() { <-semaphore }()

	matches := s.extractMatchesFromGamesURL(gamesURL, tournament.ID, fieldType)
	if len(matches) > 0 {
		matchChan <- matches

		teamURLRegex := regexp.MustCompile(`team\.php\?id=\d+`)
		matchMu.Lock()
		for _, match := range matches {
			if match.Team1.DVVURL != "" && teamURLRegex.MatchString(match.Team1.DVVURL) {
				teamURLSet[match.Team1.DVVURL] = struct{}{}
			}
			if match.Team2.DVVURL != "" && teamURLRegex.MatchString(match.Team2.DVVURL) {
				teamURLSet[match.Team2.DVVURL] = struct{}{}
			}
		}
		matchMu.Unlock()
	}
}

func (s *DVVScraper) extractTeams(teamURLSet map[string]struct{}, results *ScrapingResults) error {
	teamChan := make(chan models.Team, len(teamURLSet))
	var teamWg sync.WaitGroup
	teamSemaphore := make(chan struct{}, s.config.ConcurrencyLimit)

	for teamURL := range teamURLSet {
		teamWg.Add(1)
		go func(url string) {
			defer teamWg.Done()
			teamSemaphore <- struct{}{}
			defer func() { <-teamSemaphore }()

			idRegex := regexp.MustCompile(`id=(\d+)`)
			var teamID string
			if idMatch := idRegex.FindStringSubmatch(url); len(idMatch) > 1 {
				teamID = idMatch[1]
			}

			if teamID != "" && s.db.TeamExists(teamID) {
				return
			}

			team := s.extractTeamData(url)
			if team.ID != "" {
				teamChan <- team
			}
		}(teamURL)
	}

	teamWg.Wait()
	close(teamChan)

	for team := range teamChan {
		results.Teams = append(results.Teams, team)
		if err := s.db.InsertTeam(team); err != nil {
			s.logger.Printf("Failed to insert team %s: %v", team.ID, err)
		}
	}

	return nil
}

func (s *DVVScraper) printSummary(results *ScrapingResults) {
	s.logger.Println("=== SUMMARY ===")
	s.logger.Printf("Tournaments: %d", len(results.Tournaments))
	s.logger.Printf("Matches: %d", len(results.Matches))
	s.logger.Printf("Teams: %d", len(results.Teams))

	if len(results.Tournaments) > 0 {
		s.logger.Println("\nExample Tournament:")
		tournament := results.Tournaments[0]
		s.logger.Printf("  Title: %s", tournament.Title)
		s.logger.Printf("  Location: %s", tournament.Location)
		s.logger.Printf("  Date: %s to %s", tournament.DateFrom, tournament.DateTo)
	}

	if len(results.Matches) > 0 {
		s.logger.Println("\nExample Match:")
		match := results.Matches[0]
		s.logger.Printf("  Round: %s", match.Round)
		s.logger.Printf("  Teams: %s vs %s", match.Team1.Name, match.Team2.Name)
		s.logger.Printf("  Score: %s", match.Score)
	}

	if len(results.Teams) > 0 {
		s.logger.Println("\nExample Team:")
		team := results.Teams[0]
		s.logger.Printf("  Name: %s", team.Name)
		s.logger.Printf("  Player1: %s %s", team.Player1.FirstName, team.Player1.LastName)
		s.logger.Printf("  Player2: %s %s", team.Player2.FirstName, team.Player2.LastName)
	}
}

func main() {
	scraper, err := NewDVVScraper("./beach_volleyball.db")
	if err != nil {
		log.Fatalf("Failed to create scraper: %v", err)
	}
	defer scraper.Close()

	results, err := scraper.ScrapeAll()
	if err != nil {
		log.Fatalf("Scraping failed: %v", err)
	}

	fmt.Printf("Scraping completed successfully!\n")
	fmt.Printf("Total: %d tournaments, %d matches, %d teams\n",
		len(results.Tournaments), len(results.Matches), len(results.Teams))
}

package main

import (
	"database/sql"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/nilsimda/gbt-head2head/models"
	_ "github.com/mattn/go-sqlite3"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	tournamentUrls map[string]struct{}
	tournaments    []models.Tournament
	allMatches     []models.Match
	allTeams       []models.Team
	httpClient     *http.Client
	db             *sql.DB
)

func getTournamentURL(baseURL string, season int, mu *sync.Mutex) {
	resp, err := httpClient.Get(baseURL + "tur.php?kat=1&bytyp=0&saison=" + strconv.Itoa(season) + "#")
	if err != nil {
		fmt.Printf("Error fetching season %d: %v\n", season, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Printf("Bad status code for season %d: %d\n", season, resp.StatusCode)
		return
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		fmt.Printf("Error parsing season %d HTML: %v\n", season, err)
		return
	}

	linkIndex := 0
	table := doc.Find("table .contenttable").First()
	table.Find("a").Each(func(rowIndex int, s *goquery.Selection) {
		if rowIndex != 0 && linkIndex%2 == 1 { // skip header and every second link (each row contains the same link twice)
			href, exists := s.Attr("href")
			if exists {
				mu.Lock()
				tournamentUrls[href] = struct{}{}
				mu.Unlock()
			}
		}
		linkIndex++
	})
}

func extractTournamentData(tournamentURL string, baseURL string) models.Tournament {

	resp, err := httpClient.Get(baseURL + tournamentURL)
	if err != nil {
		// Retry once after a longer delay
		time.Sleep(1 * time.Second)
		resp, err = httpClient.Get(baseURL + tournamentURL)
		if err != nil {
			fmt.Printf("Error fetching tournament %s: %v\n", tournamentURL, err)
			return models.Tournament{}
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("Bad status code for tournament %s: %d\n", tournamentURL, resp.StatusCode)
		return models.Tournament{}
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		fmt.Printf("Error parsing tournament HTML %s: %v\n", tournamentURL, err)
		return models.Tournament{}
	}

	tournament := models.Tournament{
		DVVURL: baseURL + tournamentURL,
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
					tournament.QualifikationGamesURL = baseURL + href
				}
			})
		} else if strings.Contains(firstCell, "Hauptfeld:") {
			table.Find("a").Each(func(j int, a *goquery.Selection) {
				href, exists := a.Attr("href")
				if exists && strings.Contains(href, "tur-sp.php") {
					tournament.HauptfeldGamesURL = baseURL + href
				}
			})
		}
	})

	return tournament
}

func extractMatchesFromGamesURL(gamesURL string, tournamentID string, fieldType string) []models.Match {
	if gamesURL == "" {
		return nil
	}

	time.Sleep(100 * time.Millisecond)

	resp, err := httpClient.Get(gamesURL)
	if err != nil {
		time.Sleep(1 * time.Second)
		resp, err = httpClient.Get(gamesURL)
		if err != nil {
			fmt.Printf("Error fetching games %s: %v\n", gamesURL, err)
			return nil
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("Bad status code for games %s: %d\n", gamesURL, resp.StatusCode)
		return nil
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		fmt.Printf("Error parsing games HTML %s: %v\n", gamesURL, err)
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
					durationParts := strings.Split(durationText, ",")
					for _, part := range durationParts {
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

func extractTeamData(teamURL string, baseURL string) models.Team {
	if teamURL == "" {
		return models.Team{}
	}

	time.Sleep(100 * time.Millisecond)

	resp, err := httpClient.Get(baseURL + teamURL)
	if err != nil {
		time.Sleep(1 * time.Second)
		resp, err = httpClient.Get(baseURL + teamURL)
		if err != nil {
			fmt.Printf("Error fetching team %s: %v\n", teamURL, err)
			return models.Team{}
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("Bad status code for team %s: %d\n", teamURL, resp.StatusCode)
		return models.Team{}
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		fmt.Printf("Error parsing team HTML %s: %v\n", teamURL, err)
		return models.Team{}
	}

	team := models.Team{
		DVVURL: baseURL + teamURL,
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
			DVVURL:    baseURL + playerHref,
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
		if i == 0 {
			team.Player1 = player
			team.Player1ID = playerID
		} else if i == 1 {
			team.Player2 = player
			team.Player2ID = playerID
		}
	})

	return team
}

func initDatabase() error {
	var err error
	db, err = sql.Open("sqlite3", "./beach_volleyball.db")
	if err != nil {
		return fmt.Errorf("failed to open database: %v", err)
	}

	// Create tables
	queries := []string{
		`CREATE TABLE IF NOT EXISTS tournaments (
			id TEXT PRIMARY KEY,
			title TEXT,
			year INTEGER,
			location TEXT,
			gender TEXT,
			type TEXT,
			date_from TEXT,
			date_to TEXT,
			organizer TEXT,
			venue TEXT,
			prize_money TEXT,
			teams_hauptfeld INTEGER,
			teams_qualifikation INTEGER,
			teams_from_qualifikation INTEGER,
			qualifikation_games_url TEXT,
			hauptfeld_games_url TEXT,
			dvv_url TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS players (
			id TEXT PRIMARY KEY,
			first_name TEXT,
			last_name TEXT,
			license_id TEXT,
			club TEXT,
			gender TEXT,
			image_url TEXT,
			dvv_url TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS teams (
			id TEXT PRIMARY KEY,
			name TEXT,
			player1_id TEXT,
			player2_id TEXT,
			seed INTEGER,
			dvv_url TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (player1_id) REFERENCES players (id),
			FOREIGN KEY (player2_id) REFERENCES players (id)
		)`,
		`CREATE TABLE IF NOT EXISTS matches (
			id TEXT PRIMARY KEY,
			match_number INTEGER,
			date TEXT,
			time TEXT,
			court INTEGER,
			team1_id TEXT,
			team2_id TEXT,
			score TEXT,
			duration TEXT,
			winner_team_id TEXT,
			loser_team_id TEXT,
			round TEXT,
			placement TEXT,
			tournament_id TEXT,
			field_type TEXT,
			dvv_url TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (team1_id) REFERENCES teams (id),
			FOREIGN KEY (team2_id) REFERENCES teams (id),
			FOREIGN KEY (tournament_id) REFERENCES tournaments (id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tournaments_year ON tournaments (year)`,
		`CREATE INDEX IF NOT EXISTS idx_tournaments_location ON tournaments (location)`,
		`CREATE INDEX IF NOT EXISTS idx_tournaments_gender ON tournaments (gender)`,
		`CREATE INDEX IF NOT EXISTS idx_players_name ON players (last_name, first_name)`,
		`CREATE INDEX IF NOT EXISTS idx_players_license ON players (license_id)`,
		`CREATE INDEX IF NOT EXISTS idx_teams_players ON teams (player1_id, player2_id)`,
		`CREATE INDEX IF NOT EXISTS idx_matches_tournament ON matches (tournament_id)`,
		`CREATE INDEX IF NOT EXISTS idx_matches_teams ON matches (team1_id, team2_id)`,
		`CREATE INDEX IF NOT EXISTS idx_matches_date ON matches (date)`,
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			return fmt.Errorf("failed to execute query: %v", err)
		}
	}

	return nil
}

func tournamentExists(tournamentID string) bool {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM tournaments WHERE id = ?", tournamentID).Scan(&count)
	return err == nil && count > 0
}

func teamExists(teamID string) bool {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM teams WHERE id = ?", teamID).Scan(&count)
	return err == nil && count > 0
}

func playerExists(playerID string) bool {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM players WHERE id = ?", playerID).Scan(&count)
	return err == nil && count > 0
}

func insertTournament(t models.Tournament) error {
	_, err := db.Exec(`
		INSERT OR REPLACE INTO tournaments 
		(id, title, year, location, gender, type, date_from, date_to, organizer, venue, 
		 prize_money, teams_hauptfeld, teams_qualifikation, teams_from_qualifikation,
		 qualifikation_games_url, hauptfeld_games_url, dvv_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		t.ID, t.Title, t.Year, t.Location, t.Gender, t.Type, t.DateFrom, t.DateTo,
		t.Organizer, t.Venue, t.PrizeMoney, t.TeamsHauptfeld, t.TeamsQualifikation,
		t.TeamsFromQualifikation, t.QualifikationGamesURL, t.HauptfeldGamesURL, t.DVVURL)
	return err
}

func insertPlayer(p models.Player) error {
	_, err := db.Exec(`
		INSERT OR REPLACE INTO players 
		(id, first_name, last_name, license_id, club, gender, image_url, dvv_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		p.ID, p.FirstName, p.LastName, p.LicenseID, p.Club, p.Gender, p.ImageURL, p.DVVURL)
	return err
}

func insertTeam(t models.Team) error {
	// Insert players first
	if t.Player1.ID != "" {
		if err := insertPlayer(t.Player1); err != nil {
			return fmt.Errorf("failed to insert player1: %v", err)
		}
	}
	if t.Player2.ID != "" {
		if err := insertPlayer(t.Player2); err != nil {
			return fmt.Errorf("failed to insert player2: %v", err)
		}
	}

	// Insert team
	_, err := db.Exec(`
		INSERT OR REPLACE INTO teams 
		(id, name, player1_id, player2_id, seed, dvv_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		t.ID, t.Name, t.Player1ID, t.Player2ID, t.Seed, t.DVVURL)
	return err
}

func insertMatch(m models.Match) error {
	durationStr := ""
	if len(m.Duration) > 0 {
		parts := make([]string, len(m.Duration))
		for i, d := range m.Duration {
			parts[i] = strconv.Itoa(d)
		}
		durationStr = strings.Join(parts, ",")
	}

	_, err := db.Exec(`
		INSERT OR REPLACE INTO matches 
		(id, match_number, date, time, court, team1_id, team2_id, score, duration,
		 winner_team_id, loser_team_id, round, placement, tournament_id, field_type, dvv_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		m.ID, m.MatchNumber, m.Date, m.Time, m.Court, m.Team1.ID, m.Team2.ID, m.Score,
		durationStr, m.WinnerTeamID, m.LoserTeamID, m.Round, m.Placement, m.TournamentID,
		m.FieldType, m.DVVURL)
	return err
}

func main() {
	// Initialize database
	if err := initDatabase(); err != nil {
		fmt.Printf("Failed to initialize database: %v\n", err)
		return
	}
	defer db.Close()

	// Initialize HTTP client with proper timeouts
	httpClient = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSHandshakeTimeout: 10 * time.Second,
			IdleConnTimeout:     30 * time.Second,
			MaxIdleConns:        10,
		},
	}

	tournamentUrls = make(map[string]struct{})
	tournaments = make([]models.Tournament, 0)
	allMatches = make([]models.Match, 0)
	allTeams = make([]models.Team, 0)
	baseURL := "https://beach.volleyball-verband.de/public/"
	var mu sync.Mutex
	var wg sync.WaitGroup

	// First, collect all tournament URLs
	for i := 3; i <= 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			getTournamentURL(baseURL, i, &mu)
		}()
	}

	wg.Wait()

	fmt.Printf("Found %d tournament URLs\n", len(tournamentUrls))

	// Now extract data from each tournament with limited concurrency
	tournamentChan := make(chan models.Tournament, len(tournamentUrls))
	semaphore := make(chan struct{}, 15) // Limit to 15 concurrent requests
	var extractWg sync.WaitGroup

	for tournamentURL := range tournamentUrls {
		extractWg.Add(1)
		go func(url string) {
			defer extractWg.Done()
			semaphore <- struct{}{}        // Acquire semaphore
			defer func() { <-semaphore }() // Release semaphore

			// Extract tournament ID from URL for existence check
			idRegex := regexp.MustCompile(`id=(\d+)`)
			var tournamentID string
			if idMatch := idRegex.FindStringSubmatch(url); len(idMatch) > 1 {
				tournamentID = idMatch[1]
			}
			
			// Skip if tournament already exists in database
			if tournamentID != "" && tournamentExists(tournamentID) {
				fmt.Printf("Tournament %s already exists, skipping\n", tournamentID)
				return
			}

			tournament := extractTournamentData(url, baseURL)
			if tournament.Title != "" {
				tournamentChan <- tournament
			}
		}(tournamentURL)
	}

	extractWg.Wait()
	close(tournamentChan)

	// Collect all tournaments and save to database
	for tournament := range tournamentChan {
		tournaments = append(tournaments, tournament)
		if err := insertTournament(tournament); err != nil {
			fmt.Printf("Failed to insert tournament %s: %v\n", tournament.ID, err)
		}
	}

	fmt.Printf("Successfully extracted and saved %d tournaments\n", len(tournaments))

	// Now extract matches from each tournament
	matchChan := make(chan []models.Match, len(tournaments)*2) // *2 for hauptfeld and qualification
	teamURLSet := make(map[string]struct{})
	var matchMu sync.Mutex
	var matchWg sync.WaitGroup
	matchSemaphore := make(chan struct{}, 15) // Limit match extraction concurrency

	for _, tournament := range tournaments {
		// Extract Hauptfeld matches
		if tournament.HauptfeldGamesURL != "" {
			matchWg.Add(1)
			go func(t models.Tournament) {
				defer matchWg.Done()
				matchSemaphore <- struct{}{}
				defer func() { <-matchSemaphore }()

				matches := extractMatchesFromGamesURL(t.HauptfeldGamesURL, t.ID, "Hauptfeld")
				if len(matches) > 0 {
					matchChan <- matches

					// Collect unique team URLs (validate they're actual team URLs)
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
			}(tournament)
		}

		// Extract Qualification matches
		if tournament.QualifikationGamesURL != "" {
			matchWg.Add(1)
			go func(t models.Tournament) {
				defer matchWg.Done()
				matchSemaphore <- struct{}{}
				defer func() { <-matchSemaphore }()

				matches := extractMatchesFromGamesURL(t.QualifikationGamesURL, t.ID, "Qualifikation")
				if len(matches) > 0 {
					matchChan <- matches

					// Collect unique team URLs
					matchMu.Lock()
					for _, match := range matches {
						if match.Team1.DVVURL != "" {
							teamURLSet[match.Team1.DVVURL] = struct{}{}
						}
						if match.Team2.DVVURL != "" {
							teamURLSet[match.Team2.DVVURL] = struct{}{}
						}
					}
					matchMu.Unlock()
				}
			}(tournament)
		}
	}

	matchWg.Wait()
	close(matchChan)

	// Collect all matches and save to database
	for matches := range matchChan {
		allMatches = append(allMatches, matches...)
		for _, match := range matches {
			if err := insertMatch(match); err != nil {
				fmt.Printf("Failed to insert match %s: %v\n", match.ID, err)
			}
		}
	}

	fmt.Printf("Successfully extracted and saved %d matches\n", len(allMatches))
	fmt.Printf("Found %d unique teams\n", len(teamURLSet))

	// Extract team data
	teamChan := make(chan models.Team, len(teamURLSet))
	var teamWg sync.WaitGroup
	teamSemaphore := make(chan struct{}, 15) // Even more conservative for team extraction

	for teamURL := range teamURLSet {
		teamWg.Add(1)
		go func(url string) {
			defer teamWg.Done()
			teamSemaphore <- struct{}{}
			defer func() { <-teamSemaphore }()

			// Extract team ID from URL for existence check
			idRegex := regexp.MustCompile(`id=(\d+)`)
			var teamID string
			if idMatch := idRegex.FindStringSubmatch(url); len(idMatch) > 1 {
				teamID = idMatch[1]
			}
			
			// Skip if team already exists in database
			if teamID != "" && teamExists(teamID) {
				return
			}

			team := extractTeamData(url, baseURL)
			if team.ID != "" {
				teamChan <- team
			}
		}(teamURL)
	}

	teamWg.Wait()
	close(teamChan)

	// Collect all teams and save to database
	for team := range teamChan {
		allTeams = append(allTeams, team)
		if err := insertTeam(team); err != nil {
			fmt.Printf("Failed to insert team %s: %v\n", team.ID, err)
		}
	}

	fmt.Printf("Successfully extracted and saved %d teams\n", len(allTeams))

	// Print some example data
	fmt.Printf("\n=== SUMMARY ===\n")
	fmt.Printf("Tournaments: %d\n", len(tournaments))
	fmt.Printf("Matches: %d\n", len(allMatches))
	fmt.Printf("Teams: %d\n", len(allTeams))

	if len(tournaments) > 0 {
		fmt.Printf("\nExample Tournament:\n")
		tournament := tournaments[0]
		fmt.Printf("  Title: %s\n", tournament.Title)
		fmt.Printf("  Location: %s\n", tournament.Location)
		fmt.Printf("  Date: %s to %s\n", tournament.DateFrom, tournament.DateTo)
	}

	if len(allMatches) > 0 {
		fmt.Printf("\nExample Match:\n")
		match := allMatches[0]
		fmt.Printf("  Round: %s\n", match.Round)
		fmt.Printf("  Teams: %s vs %s\n", match.Team1.Name, match.Team2.Name)
		fmt.Printf("  Score: %s\n", match.Score)
	}

	if len(allTeams) > 0 {
		fmt.Printf("\nExample Team:\n")
		team := allTeams[0]
		fmt.Printf("  Name: %s\n", team.Name)
		fmt.Printf("  Player1: %s %s\n", team.Player1.FirstName, team.Player1.LastName)
		fmt.Printf("  Player2: %s %s\n", team.Player2.FirstName, team.Player2.LastName)
	}
}

package db

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nilsimda/gbt-head2head/models"
	_ "github.com/mattn/go-sqlite3"
)

type BeachVolleyballDB struct {
	db *sql.DB
}

func NewBeachVolleyballDB(dbPath string) (*BeachVolleyballDB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	bvDB := &BeachVolleyballDB{db: db}
	if err := bvDB.initDatabase(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return bvDB, nil
}

func (db *BeachVolleyballDB) Close() error {
	return db.db.Close()
}

func (db *BeachVolleyballDB) initDatabase() error {
	// First, check if we need to migrate existing tables
	if err := db.migrateDatabase(); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

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
			last_scraped_at DATETIME,
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
		`CREATE INDEX IF NOT EXISTS idx_tournaments_date_to ON tournaments (date_to)`,
		`CREATE INDEX IF NOT EXISTS idx_tournaments_last_scraped ON tournaments (last_scraped_at)`,
		`CREATE INDEX IF NOT EXISTS idx_players_name ON players (last_name, first_name)`,
		`CREATE INDEX IF NOT EXISTS idx_players_license ON players (license_id)`,
		`CREATE INDEX IF NOT EXISTS idx_teams_players ON teams (player1_id, player2_id)`,
		`CREATE INDEX IF NOT EXISTS idx_matches_tournament ON matches (tournament_id)`,
		`CREATE INDEX IF NOT EXISTS idx_matches_teams ON matches (team1_id, team2_id)`,
		`CREATE INDEX IF NOT EXISTS idx_matches_date ON matches (date)`,
	}

	for _, query := range queries {
		if _, err := db.db.Exec(query); err != nil {
			return fmt.Errorf("failed to execute query: %w", err)
		}
	}

	return nil
}

func (db *BeachVolleyballDB) migrateDatabase() error {
	// Check if last_scraped_at column exists in tournaments table
	var count int
	err := db.db.QueryRow(`
		SELECT COUNT(*) 
		FROM pragma_table_info('tournaments') 
		WHERE name = 'last_scraped_at'
	`).Scan(&count)
	
	if err != nil {
		// Table might not exist yet, which is fine
		return nil
	}
	
	if count == 0 {
		// Add the missing column
		_, err = db.db.Exec(`
			ALTER TABLE tournaments 
			ADD COLUMN last_scraped_at DATETIME
		`)
		if err != nil {
			return fmt.Errorf("failed to add last_scraped_at column: %w", err)
		}
	}
	
	return nil
}

// Tournament operations
func (db *BeachVolleyballDB) TournamentExists(tournamentID string) bool {
	var count int
	err := db.db.QueryRow("SELECT COUNT(*) FROM tournaments WHERE id = ?", tournamentID).Scan(&count)
	return err == nil && count > 0
}

func (db *BeachVolleyballDB) ShouldScrapeTournament(tournamentID string, tournamentDateTo string) bool {
	// Parse the tournament end date
	if tournamentDateTo == "" {
		return false // Can't determine if tournament is complete
	}

	// Try common German date formats
	var endDate time.Time
	var err error
	
	dateFormats := []string{
		"02.01.2006",    // DD.MM.YYYY
		"2.1.2006",      // D.M.YYYY
		"02.01.06",      // DD.MM.YY
		"2006-01-02",    // YYYY-MM-DD
	}

	for _, format := range dateFormats {
		endDate, err = time.Parse(format, tournamentDateTo)
		if err == nil {
			break
		}
	}

	if err != nil {
		// If we can't parse the date, skip this tournament
		return false
	}

	// Only scrape tournaments that ended at least 1 day ago
	// This gives time for final results to be posted
	cutoffDate := time.Now().AddDate(0, 0, -1)
	if endDate.After(cutoffDate) {
		return false // Tournament too recent
	}

	// Check if we've never scraped this tournament or it's been a while
	var lastScraped sql.NullTime
	err = db.db.QueryRow(
		"SELECT last_scraped_at FROM tournaments WHERE id = ?", 
		tournamentID,
	).Scan(&lastScraped)

	if err != nil {
		// Tournament doesn't exist in DB, should scrape
		return true
	}

	if !lastScraped.Valid {
		// Tournament exists but never been scraped
		return true
	}

	// Re-scrape if it's been more than 7 days since last scrape
	// (in case of late updates)
	weekAgo := time.Now().AddDate(0, 0, -7)
	return lastScraped.Time.Before(weekAgo)
}

func (db *BeachVolleyballDB) InsertTournament(t models.Tournament) error {
	_, err := db.db.Exec(`
		INSERT OR REPLACE INTO tournaments 
		(id, title, year, location, gender, type, date_from, date_to, organizer, venue, 
		 prize_money, teams_hauptfeld, teams_qualifikation, teams_from_qualifikation,
		 qualifikation_games_url, hauptfeld_games_url, dvv_url, last_scraped_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		t.ID, t.Title, t.Year, t.Location, t.Gender, t.Type, t.DateFrom, t.DateTo,
		t.Organizer, t.Venue, t.PrizeMoney, t.TeamsHauptfeld, t.TeamsQualifikation,
		t.TeamsFromQualifikation, t.QualifikationGamesURL, t.HauptfeldGamesURL, t.DVVURL)
	return err
}

// Player operations
func (db *BeachVolleyballDB) PlayerExists(playerID string) bool {
	var count int
	err := db.db.QueryRow("SELECT COUNT(*) FROM players WHERE id = ?", playerID).Scan(&count)
	return err == nil && count > 0
}

func (db *BeachVolleyballDB) InsertPlayer(p models.Player) error {
	_, err := db.db.Exec(`
		INSERT OR REPLACE INTO players 
		(id, first_name, last_name, license_id, club, gender, image_url, dvv_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		p.ID, p.FirstName, p.LastName, p.LicenseID, p.Club, p.Gender, p.ImageURL, p.DVVURL)
	return err
}

// Team operations
func (db *BeachVolleyballDB) TeamExists(teamID string) bool {
	var count int
	err := db.db.QueryRow("SELECT COUNT(*) FROM teams WHERE id = ?", teamID).Scan(&count)
	return err == nil && count > 0
}

func (db *BeachVolleyballDB) InsertTeam(t models.Team) error {
	// Insert players first
	if t.Player1.ID != "" {
		if err := db.InsertPlayer(t.Player1); err != nil {
			return fmt.Errorf("failed to insert player1: %w", err)
		}
	}
	if t.Player2.ID != "" {
		if err := db.InsertPlayer(t.Player2); err != nil {
			return fmt.Errorf("failed to insert player2: %w", err)
		}
	}

	// Insert team
	_, err := db.db.Exec(`
		INSERT OR REPLACE INTO teams 
		(id, name, player1_id, player2_id, seed, dvv_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		t.ID, t.Name, t.Player1ID, t.Player2ID, t.Seed, t.DVVURL)
	return err
}

// Match operations
func (db *BeachVolleyballDB) InsertMatch(m models.Match) error {
	durationStr := ""
	if len(m.Duration) > 0 {
		parts := make([]string, len(m.Duration))
		for i, d := range m.Duration {
			parts[i] = strconv.Itoa(d)
		}
		durationStr = strings.Join(parts, ",")
	}

	_, err := db.db.Exec(`
		INSERT OR REPLACE INTO matches 
		(id, match_number, date, time, court, team1_id, team2_id, score, duration,
		 winner_team_id, loser_team_id, round, placement, tournament_id, field_type, dvv_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		m.ID, m.MatchNumber, m.Date, m.Time, m.Court, m.Team1.ID, m.Team2.ID, m.Score,
		durationStr, m.WinnerTeamID, m.LoserTeamID, m.Round, m.Placement, m.TournamentID,
		m.FieldType, m.DVVURL)
	return err
}

// Read operations for the main app
func (db *BeachVolleyballDB) GetAllTournaments() ([]models.Tournament, error) {
	rows, err := db.db.Query(`
		SELECT id, title, year, location, gender, type, date_from, date_to, 
		       organizer, venue, prize_money, teams_hauptfeld, teams_qualifikation, 
		       teams_from_qualifikation, qualifikation_games_url, hauptfeld_games_url, 
		       dvv_url
		FROM tournaments 
		ORDER BY year DESC, date_from DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tournaments []models.Tournament
	for rows.Next() {
		var t models.Tournament
		err := rows.Scan(&t.ID, &t.Title, &t.Year, &t.Location, &t.Gender, &t.Type,
			&t.DateFrom, &t.DateTo, &t.Organizer, &t.Venue, &t.PrizeMoney,
			&t.TeamsHauptfeld, &t.TeamsQualifikation, &t.TeamsFromQualifikation,
			&t.QualifikationGamesURL, &t.HauptfeldGamesURL, &t.DVVURL)
		if err != nil {
			return nil, err
		}
		tournaments = append(tournaments, t)
	}
	return tournaments, rows.Err()
}

func (db *BeachVolleyballDB) GetTeamsByPlayer(playerName string) ([]models.Team, error) {
	rows, err := db.db.Query(`
		SELECT DISTINCT t.id, t.name, t.player1_id, t.player2_id, t.seed, t.dvv_url,
		       p1.first_name, p1.last_name, p1.license_id, p1.club, p1.gender, p1.image_url, p1.dvv_url,
		       p2.first_name, p2.last_name, p2.license_id, p2.club, p2.gender, p2.image_url, p2.dvv_url
		FROM teams t
		LEFT JOIN players p1 ON t.player1_id = p1.id
		LEFT JOIN players p2 ON t.player2_id = p2.id
		WHERE p1.first_name LIKE ? OR p1.last_name LIKE ? 
		   OR p2.first_name LIKE ? OR p2.last_name LIKE ?`,
		"%"+playerName+"%", "%"+playerName+"%", "%"+playerName+"%", "%"+playerName+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var teams []models.Team
	for rows.Next() {
		var t models.Team
		var p1FirstName, p1LastName, p1LicenseID, p1Club, p1Gender, p1ImageURL, p1DVVURL sql.NullString
		var p2FirstName, p2LastName, p2LicenseID, p2Club, p2Gender, p2ImageURL, p2DVVURL sql.NullString

		err := rows.Scan(&t.ID, &t.Name, &t.Player1ID, &t.Player2ID, &t.Seed, &t.DVVURL,
			&p1FirstName, &p1LastName, &p1LicenseID, &p1Club, &p1Gender, &p1ImageURL, &p1DVVURL,
			&p2FirstName, &p2LastName, &p2LicenseID, &p2Club, &p2Gender, &p2ImageURL, &p2DVVURL)
		if err != nil {
			return nil, err
		}

		// Fill player data if available
		if p1FirstName.Valid {
			t.Player1 = models.Player{
				ID:        t.Player1ID,
				FirstName: p1FirstName.String,
				LastName:  p1LastName.String,
				LicenseID: p1LicenseID.String,
				Club:      p1Club.String,
				Gender:    p1Gender.String,
				ImageURL:  p1ImageURL.String,
				DVVURL:    p1DVVURL.String,
			}
		}
		if p2FirstName.Valid {
			t.Player2 = models.Player{
				ID:        t.Player2ID,
				FirstName: p2FirstName.String,
				LastName:  p2LastName.String,
				LicenseID: p2LicenseID.String,
				Club:      p2Club.String,
				Gender:    p2Gender.String,
				ImageURL:  p2ImageURL.String,
				DVVURL:    p2DVVURL.String,
			}
		}

		teams = append(teams, t)
	}
	return teams, rows.Err()
}

func (db *BeachVolleyballDB) GetHead2HeadMatches(team1ID, team2ID string) ([]models.Match, error) {
	rows, err := db.db.Query(`
		SELECT m.id, m.match_number, m.date, m.time, m.court, m.team1_id, m.team2_id,
		       m.score, m.duration, m.winner_team_id, m.loser_team_id, m.round, m.placement,
		       m.tournament_id, m.field_type, m.dvv_url,
		       t1.name, t2.name,
		       tour.title, tour.location, tour.date_from, tour.date_to
		FROM matches m
		JOIN teams t1 ON m.team1_id = t1.id
		JOIN teams t2 ON m.team2_id = t2.id
		JOIN tournaments tour ON m.tournament_id = tour.id
		WHERE (m.team1_id = ? AND m.team2_id = ?) OR (m.team1_id = ? AND m.team2_id = ?)
		ORDER BY tour.date_from DESC, m.date DESC`,
		team1ID, team2ID, team2ID, team1ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []models.Match
	for rows.Next() {
		var m models.Match
		var t1Name, t2Name string
		var tourTitle, tourLocation, tourDateFrom, tourDateTo string
		var durationStr sql.NullString

		err := rows.Scan(&m.ID, &m.MatchNumber, &m.Date, &m.Time, &m.Court,
			&m.Team1.ID, &m.Team2.ID, &m.Score, &durationStr, &m.WinnerTeamID, &m.LoserTeamID,
			&m.Round, &m.Placement, &m.TournamentID, &m.FieldType, &m.DVVURL,
			&t1Name, &t2Name, &tourTitle, &tourLocation, &tourDateFrom, &tourDateTo)
		if err != nil {
			return nil, err
		}

		m.Team1.Name = t1Name
		m.Team2.Name = t2Name

		// Parse duration if available
		if durationStr.Valid && durationStr.String != "" {
			parts := strings.Split(durationStr.String, ",")
			for _, part := range parts {
				if duration, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
					m.Duration = append(m.Duration, duration)
				}
			}
		}

		matches = append(matches, m)
	}
	return matches, rows.Err()
}
package models

// Models defined as per requirements
type Tournament struct {
	ID                        string
	Title                     string
	Year                      int
	Location                  string
	Gender                    string
	Type                      string // e.g., "German Beach Tour"
	DateFrom                  string
	DateTo                    string
	Organizer                 string
	Venue                     string
	PrizeMoney                string
	TeamsHauptfeld            int
	TeamsQualifikation        int
	TeamsFromQualifikation    int
	QualifikationGamesURL     string
	HauptfeldGamesURL         string
	DVVURL                    string
}

type Player struct {
	ID           string
	FirstName    string
	LastName     string
	LicenseID    string
	Club         string
	Gender       string
	ImageURL     string
	DVVURL       string
}

type Team struct {
	ID          string
	Name        string
	Player1     Player
	Player2     Player
	Player1ID   string
	Player2ID   string
	Seed        int
	DVVURL      string
}

type Match struct {
	ID           string
	MatchNumber  int
	Date         string
	Time         string
	Court        int
	Team1        Team
	Team2        Team
	Score        string
	Duration     []int // duration of each set in minutes
	WinnerTeamID string
	LoserTeamID  string
	Round        string // e.g., "Achtelfinale Winner", "Finale"
	Placement    string // e.g., "Verl. 7", "Gew. 1"
	TournamentID string
	FieldType    string // "Hauptfeld" or "Qualifikation"
	DVVURL       string
}

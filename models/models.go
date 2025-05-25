package models

// Models defined as per requirements
type Tournament struct {
	Title    string
	Year     int
	Location string
	Gender   string
	DVVURL   string
}

type Player struct {
	ID        string
	FirstName string
	LastName  string
	Gender    string
	DVVURL    string
}

type Team struct {
	ID        string
	Player1ID string
	Player2ID string
	DVVURL    string
}

type Match struct {
	ID         string
	Team1      Team
	Team2      Team
	Score      string
	Tournament Tournament
	MatchType  string
	DVVURL     string
}

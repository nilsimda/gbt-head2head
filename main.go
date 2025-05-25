package main

import (
	"embed"
	"github.com/nilsimda/gbt-head2head/components"
	"log/slog"
	"net/http"
	"os"
)

var (
	//go:embed all:assets/*
	assets embed.FS
)

func getIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/player-head2head", http.StatusSeeOther)
}

func getPlayerHead2Head(w http.ResponseWriter, r *http.Request) {
	components.PlayerHead2Head().Render(r.Context(), w)
}

func getTeamHead2Head(w http.ResponseWriter, r *http.Request) {
	components.TeamHead2Head().Render(r.Context(), w)
}

func postPlayerSearch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	mux := http.NewServeMux()
	mux.Handle("GET /assets/{pathname...}", http.FileServer(http.FS(assets)))

	mux.HandleFunc("GET /", getIndex)
	mux.HandleFunc("GET /player-head2head", getPlayerHead2Head)
	mux.HandleFunc("GET /team-head2head", getTeamHead2Head)
	mux.HandleFunc("POST /player-search", postPlayerSearch)

	logger.Info("Starting server...")
	http.ListenAndServe(":8080", mux)
}

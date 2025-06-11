# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Commands

**Start development environment:**
```bash
make live
```
This starts 4 watch processes in parallel:
- Templ generation with browser proxy on localhost:7331
- Go server rebuild with Air on localhost:8080  
- TailwindCSS compilation
- Asset sync for browser reload

**Individual development processes:**
```bash
make live/templ    # Watch .templ files and regenerate
make live/server   # Watch Go files and rebuild server
make live/tailwind # Watch CSS and compile with Tailwind
```

**Build commands:**
```bash
go build -o tmp/bin/main  # Build the server
templ generate           # Generate Go code from .templ files
```

## Architecture Overview

This is a Go web application for beach volleyball head-to-head statistics using:

**Core Stack:**
- **Templ**: HTML templating that compiles to Go code (components/*.templ)
- **HTMX**: Client-side interactivity and AJAX requests
- **TailwindCSS**: Utility-first CSS framework
- **GoQuery**: HTML parsing for web scraping

**Application Structure:**
- **main.go**: HTTP server with embedded assets, handles routing for player/team comparisons
- **components/**: Templ templates for UI components (Layout, PlayerHead2Head, TeamHead2Head)
- **models/**: Data structures for Tournament, Player, Team, Match with DVV URLs
- **cron/**: Web scraper for German Beach Volleyball Association (DVV) tournament data
- **assets/**: Static files (CSS, JS) embedded in binary

**Data Flow:**
The app scrapes tournament data from beach.volleyball-verband.de, stores player/team information, and provides head-to-head comparison interfaces. The scraper (cron/dvv_scraper.go) collects tournament URLs across multiple seasons concurrently.

**Key Patterns:**
- Templ components use `@Layout()` wrapper pattern
- HTMX triggers on input changes with 500ms delay for search
- Embedded assets using Go's embed directive
- Concurrent scraping with sync.WaitGroup and mutex protection
package main

import (
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"net/http"
	"strconv"
	"sync"
)

var tournamentUrls map[string]struct{}

func getTournamentURL(baseURL string, season int, mu *sync.Mutex) {
	resp, err := http.Get(baseURL + "tur.php?kat=1&bytyp=0&saison=" + strconv.Itoa(season) + "#")
	if err != nil {
		fmt.Println(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Println(resp.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		fmt.Println(err)
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

func main() {
	tournamentUrls = make(map[string]struct{})
	baseURL := "https://beach.volleyball-verband.de/public/"
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 3; i <= 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			getTournamentURL(baseURL, i, &mu)
		}()
	}

	wg.Wait()

	fmt.Println(len(tournamentUrls))
}

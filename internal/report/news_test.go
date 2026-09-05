package report

import (
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/models"
	"stock-portfolio/internal/news"
)

func TestSourcedNewsRendersWithoutAIAndEscapesHTML(t *testing.T) {
	g, err := NewGenerator(nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	digest := news.Digest{LookbackHours: 72, TotalFeeds: 2, FailedFeeds: 1, Articles: []news.Article{{Title: "Oracle <script>alert(1)</script>", Summary: "Revenue grows", URL: "https://example.com/article", Source: "Example", PublishedAt: time.Date(2026, 9, 4, 18, 0, 0, 0, time.UTC), Tickers: []string{"ORCL"}, Reason: "Mentionne vos positions : ORCL"}}}
	content, err := g.GenerateWithAI(nil, nil, "", nil, nil, &digest)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Actualités à suivre", "Portefeuille", "04/09/2026 18:00", "Couverture partielle", `href="https://example.com/article"`, "&lt;script&gt;"} {
		if !strings.Contains(content, expected) {
			t.Errorf("missing %q", expected)
		}
	}
	if strings.Contains(content, "<script>alert(1)</script>") {
		t.Fatal("unescaped feed content")
	}
	if strings.Contains(content, "Mentionne vos positions") {
		t.Fatal("redundant matching explanation is visible")
	}
}

func TestHiddenPositionsPreservesCalendarsAndNews(t *testing.T) {
	g, err := NewGenerator(nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	earnings := time.Now().Add(48 * time.Hour)
	results := []*models.StockResult{{Stock: models.Stock{Ticker: "PL", Name: "Planet Labs PBC", Category: "Space"}, NextEarningsDate: &earnings, CurrentPrice: 10, RSI: 25}}
	digest := news.Digest{LookbackHours: 72, Articles: []news.Article{{Title: "Planet Labs results", URL: "https://example.com/pl", PublishedAt: time.Now(), Tickers: []string{"PL"}}}}
	events := []EconomicEventData{{Name: "FOMC meeting", Date: "Wed 16 Sep, 14:00"}}
	for _, show := range []bool{false, true} {
		g.ShowPositions = show
		content, err := g.GenerateWithAI(results, nil, "", nil, events, &digest)
		if err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"Prochains résultats", "Planet Labs PBC", "FOMC meeting", "Actualités à suivre", "Planet Labs results"} {
			if !strings.Contains(content, expected) {
				t.Errorf("show_positions=%v: missing %q", show, expected)
			}
		}
		if strings.Contains(content, `<table class="stock-table">`) != show {
			t.Errorf("show_positions=%v: unexpected position table visibility", show)
		}
		if strings.Contains(content, "stocks tracked") != show {
			t.Errorf("show_positions=%v: unexpected position counter visibility", show)
		}
	}
}

package marketdigest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"stock-portfolio/internal/models"
)

func historyFixture() RankingOptions {
	f := 3.0
	o := RankingOptions{Limit: 3, Evaluations: map[string]models.Evaluation{}}
	for _, item := range []struct{ ticker, signal string }{
		{"KEEP", "BUY"}, {"MOVE", "SELL"}, {"DIP", "HOLD"}, {"REBOUND", "HOLD"},
	} {
		o.Stocks = append(o.Stocks, models.Stock{Ticker: item.ticker, Name: item.ticker})
		o.Evaluations[item.ticker] = models.Evaluation{Version: models.EvaluationVersion, Eligible: true, QuoteType: "EQUITY", Signal: item.signal, Fundamental: &f}
	}
	e := o.Evaluations["DIP"]
	e.Pullback = &models.PullbackEvaluation{Eligible: true, Drawdown20: 20}
	o.Evaluations["DIP"] = e
	e = o.Evaluations["REBOUND"]
	e.Daily, e.Weekly = 2, -3
	o.Evaluations["REBOUND"] = e
	return o
}

func renderHistory(o RankingOptions) string {
	var sb strings.Builder
	writeEvaluationTopSection(&sb, o)
	return sb.String()
}

func TestHistoryFirstRunUnchangedAndSectionMove(t *testing.T) {
	o := historyFixture()
	if strings.Contains(renderHistory(o), `class="new-entry"`) {
		t.Fatal("first run should only establish a baseline")
	}
	o.Previous = SnapshotSections(o, "scope")
	e := o.Evaluations["KEEP"]
	e.Signal, e.Score, e.Price = "STRONG BUY", 9, 123
	o.Evaluations["KEEP"] = e
	if strings.Contains(renderHistory(o), `class="new-entry"`) {
		t.Fatal("price/score/badge change marked as a new section entry")
	}
	e = o.Evaluations["MOVE"]
	e.Signal = "BUY"
	o.Evaluations["MOVE"] = e
	content := renderHistory(o)
	if strings.Count(content, `class="new-entry"`) != 1 || !strings.Contains(content, "background-color:#edf7ef") {
		t.Fatal("moving from sells to buys must highlight only its new section")
	}
	// A ticker present in one section can be new in another simultaneously.
	e = o.Evaluations["DIP"]
	e.Signal = "SELL"
	o.Evaluations["DIP"] = e
	content = renderHistory(o)
	if strings.Count(content, `class="new-entry"`) != 2 || !strings.Contains(content, "background-color:#fdf0ef") {
		t.Fatal("section membership was conflated across buy/sell/pullback lists")
	}
}

func TestHistoryEmptySectionsReentryAndRankingCutoff(t *testing.T) {
	o := historyFixture()
	baseline := SnapshotSections(o, "scope")
	// A successful report with empty sections must clear the previous membership.
	empty := RankingOptions{Evaluations: map[string]models.Evaluation{}}
	o.Previous = SnapshotSections(empty, "scope")
	content := renderHistory(o)
	if strings.Count(content, `class="new-entry"`) != 4 || strings.Count(content, "background-color:#fdf6e7") != 2 {
		t.Fatal("reappearing entries or amber section highlights missing")
	}
	if !baseline.isNew(sectionBuys, "NEW") || baseline.isNew(sectionBuys, "KEEP") {
		t.Fatal("incorrect membership comparison")
	}
	for _, ticker := range []string{"A", "B", "C", "Z"} {
		o.Stocks = append(o.Stocks, models.Stock{Ticker: ticker})
		o.Evaluations[ticker] = models.Evaluation{Version: models.EvaluationVersion, Eligible: true, Signal: "BUY", Score: 6}
	}
	o.Previous = SnapshotSections(o, "scope")
	if !o.Previous.isNew(sectionBuys, "Z") {
		t.Fatal("off-screen candidate incorrectly recorded as displayed")
	}
	e := o.Evaluations["Z"]
	e.Score = 6.5
	o.Evaluations["Z"] = e
	if strings.Count(renderHistory(o), `class="new-entry"`) != 1 {
		t.Fatal("candidate newly entering the limited ranking not highlighted")
	}
}

func TestHistorySaveLoadAndInvalidBaselines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "digest.json")
	scope := HistoryScope("both", 1.5, 3, 10)
	if h, err := LoadHistory(path, scope); h != nil || err != nil {
		t.Fatalf("missing cache: %v %v", h, err)
	}
	h := SnapshotSections(historyFixture(), scope)
	if err := h.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadHistory(path, scope)
	if err != nil || loaded == nil || loaded.isNew(sectionPullbacks, "DIP") || !loaded.isNew(sectionPullbacks, "OTHER") {
		t.Fatalf("round trip: %v %v", loaded, err)
	}
	for _, otherScope := range []string{HistoryScope("daily", 1.5, 3, 10), HistoryScope("both", 2, 3, 10), HistoryScope("both", 1.5, 3, 5)} {
		if loaded, err := LoadHistory(path, otherScope); err != nil || loaded != nil {
			t.Fatalf("incompatible configuration reused: %v %v", loaded, err)
		}
	}
	h.Version++
	if err := h.Save(path); err != nil {
		t.Fatal(err)
	}
	if loaded, err := LoadHistory(path, scope); err != nil || loaded != nil {
		t.Fatalf("incompatible version reused: %v %v", loaded, err)
	}
	for _, bad := range []string{`{`, `{"version":1,"scope":"` + scope + `","sections":{}}`} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if loaded, err := LoadHistory(path, scope); err == nil || loaded != nil {
			t.Fatalf("corrupt history reused: %v %v", loaded, err)
		}
	}
}

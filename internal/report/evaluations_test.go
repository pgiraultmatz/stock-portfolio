package report

import (
	"stock-portfolio/internal/models"
	"testing"
)

func TestReportUsesSharedBadge(t *testing.T) {
	g, err := NewGenerator(nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &models.StockResult{Stock: models.Stock{Ticker: "TEST"}, Evaluation: &models.Evaluation{Version: models.EvaluationVersion, Eligible: true, Signal: "BUY", Score: 5, Warnings: []string{"WAIT EARNINGS", "Provisoire"}}}
	signal, note := r.Evaluation.Badge()
	_, got, _, gotNote := g.computeSignal(r)
	if got != signal || gotNote != note || g.ExtractSignals([]*models.StockResult{r})["TEST"] != [2]string{signal, note} {
		t.Fatal("report recomputed the shared badge")
	}
	r.Evaluation = nil
	_, got, _, _ = g.computeSignal(r)
	if got != "À recalculer" {
		t.Fatal("legacy scoring still active")
	}
}

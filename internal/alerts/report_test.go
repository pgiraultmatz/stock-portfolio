package alerts

import (
	"strings"
	"testing"

	"stock-portfolio/internal/models"
)

func TestGenerateReportSortsByAbsoluteChange(t *testing.T) {
	alerts := []Alert{
		alert("ARM", "Arm Holdings", "AI Compute", -7.63),
		alert("APLD", "Applied Digital", "AI Infrastructure", -3.74),
		alert("SMCI", "Super Micro", "AI Infrastructure", 6.10),
		alert("VICR", "Vicor", "AI Power", -8.22),
		alert("POWL", "Powell", "AI Power", 3.18),
	}

	html := GenerateReport(alerts)

	assertBefore(t, html, "VICR", "ARM")
	assertBefore(t, html, "ARM", "SMCI")
	assertBefore(t, html, "SMCI", "APLD")
	assertBefore(t, html, "APLD", "POWL")
}

func assertBefore(t *testing.T, body, first, second string) {
	t.Helper()

	firstIndex := strings.Index(body, first)
	if firstIndex == -1 {
		t.Fatalf("expected %q in report", first)
	}

	secondIndex := strings.Index(body, second)
	if secondIndex == -1 {
		t.Fatalf("expected %q in report", second)
	}

	if firstIndex > secondIndex {
		t.Fatalf("expected %q before %q", first, second)
	}
}

func alert(ticker, name, category string, change float64) Alert {
	return Alert{
		Stock: models.Stock{
			Ticker:   ticker,
			Name:     name,
			Category: category,
		},
		OpenPrice:     100,
		CurrentPrice:  100 + change,
		ChangePercent: change,
		Threshold:     3,
	}
}

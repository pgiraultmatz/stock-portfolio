package marketdigest

import (
	"strings"
	"testing"

	"stock-portfolio/internal/chartcalc"
	"stock-portfolio/internal/divergencealerts"
	"stock-portfolio/internal/models"
	"stock-portfolio/internal/technicalalerts"
)

func TestGenerateReportIncludesBothSections(t *testing.T) {
	stock := models.Stock{Ticker: "MSFT", Name: "Microsoft", Category: "USA"}
	html := GenerateReport(
		[]divergencealerts.Alert{{
			Stock: stock,
			Divergence: chartcalc.Divergence{
				Kind: "bullish", FromTime: 100, ToTime: 200,
				FromPrice: 90, ToPrice: 85, FromRSI: 30, ToRSI: 35,
			},
			LastClose: 86,
		}},
		[]technicalalerts.Alert{{
			Stock: stock, Kind: technicalalerts.EMAProximity, Label: "EMA50 touch",
			Bias: "watch", Period: 50, LastClose: 100, Level: 99,
			DistancePercent: 1.01, Detail: "Close is near EMA50.",
		}},
		"daily",
		1.5,
	)

	for _, expected := range []string{"Market Signal Digest", "Signals by Ticker", "BULLISH RSI divergence", "EMA50 touch", "MSFT"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("expected report to contain %q", expected)
		}
	}
	if got := strings.Count(html, `<td><span class="ticker">MSFT</span>`); got != 1 {
		t.Fatalf("expected one ticker row for grouped signals, got %d", got)
	}
}

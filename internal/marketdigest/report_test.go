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

	for _, expected := range []string{"Market Signal Digest", "USA", "BULLISH RSI divergence", "EMA50 touch", "MSFT"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("expected report to contain %q", expected)
		}
	}
	if got := strings.Count(html, `<td><span class="ticker">MSFT</span>`); got != 1 {
		t.Fatalf("expected one ticker row for grouped signals, got %d", got)
	}
}

func TestGenerateReportUsesCategoryOrder(t *testing.T) {
	usa := models.Stock{Ticker: "MSFT", Name: "Microsoft", Category: "USA"}
	crypto := models.Stock{Ticker: "BTC-USD", Name: "Bitcoin", Category: "Cryptos"}
	html := GenerateReportWithCategoryOrder(
		nil,
		[]technicalalerts.Alert{
			{Stock: usa, Kind: technicalalerts.Breakout, Label: "20-candle breakout", Bias: "bullish", LastClose: 100},
			{Stock: crypto, Kind: technicalalerts.EMAProximity, Label: "EMA200 touch", Bias: "watch", LastClose: 100},
		},
		"daily",
		1.5,
		map[string]int{"Cryptos": 1, "USA": 2},
	)

	if strings.Index(html, "<h3>Cryptos") > strings.Index(html, "<h3>USA") {
		t.Fatal("expected configured category order to be used")
	}
}

func TestGenerateReportIncludesTopBullishAndBearish(t *testing.T) {
	bullish := models.Stock{Ticker: "MSFT", Name: "Microsoft", Category: "USA"}
	bearish := models.Stock{Ticker: "TSLA", Name: "Tesla", Category: "USA"}
	watch := models.Stock{Ticker: "AAPL", Name: "Apple", Category: "USA"}

	html := GenerateReport(
		[]divergencealerts.Alert{
			{Stock: bullish, Divergence: chartcalc.Divergence{Kind: "bullish"}, LastClose: 100},
			{Stock: bearish, Divergence: chartcalc.Divergence{Kind: "bearish"}, LastClose: 100},
		},
		[]technicalalerts.Alert{
			{Stock: bullish, Kind: technicalalerts.EMAReclaim, Label: "EMA50 reclaim", Bias: "bullish", LastClose: 100},
			{Stock: bearish, Kind: technicalalerts.EMALoss, Label: "EMA50 loss", Bias: "bearish", LastClose: 100},
			{Stock: watch, Kind: technicalalerts.EMAProximity, Label: "EMA50 touch", Bias: "watch", LastClose: 100},
		},
		"daily",
		1.5,
	)

	for _, expected := range []string{"Top Signals", "MSFT", "TSLA", "2 signals"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("expected report to contain %q", expected)
		}
	}
}

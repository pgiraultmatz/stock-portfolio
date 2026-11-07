package marketdigest

import (
	"strings"
	"testing"
	"time"

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
	if !strings.Contains(html, `<span class="ticker">MSFT</span> <span class="muted">- Microsoft</span>`) {
		t.Fatal("expected compact ticker and name format")
	}
	if strings.Contains(html, "Last close") {
		t.Fatal("did not expect last close column")
	}
	if strings.Contains(html, `<li><span class="watch">EMA50 touch</span></li>`) {
		t.Fatal("expected table signals to be inline, not list items")
	}
	if got := strings.Count(html, `<span class="ticker">MSFT</span>`); got != 1 {
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

func TestGenerateReportIncludesTopBuyAndSellCandidates(t *testing.T) {
	bullish := models.Stock{Ticker: "MSFT", Name: "Microsoft", Category: "USA"}
	bearish := models.Stock{Ticker: "TSLA", Name: "Tesla", Category: "USA"}
	watch := models.Stock{Ticker: "AAPL", Name: "Apple", Category: "USA"}

	html := GenerateReport(
		[]divergencealerts.Alert{
			{Stock: bullish, Divergence: chartcalc.Divergence{Kind: "bullish", ToTime: time.Now().Add(-time.Hour).Unix()}, LastClose: 100},
			{Stock: bearish, Divergence: chartcalc.Divergence{Kind: "bearish", ToTime: time.Now().Add(-time.Hour).Unix()}, LastClose: 100},
		},
		[]technicalalerts.Alert{
			{Stock: bullish, Kind: technicalalerts.EMAReclaim, Label: "EMA50 reclaim", Bias: "bullish", LastClose: 100, CandleTime: time.Now().Add(-time.Hour).Unix()},
			{Stock: bearish, Kind: technicalalerts.EMALoss, Label: "EMA50 loss", Bias: "bearish", LastClose: 100, CandleTime: time.Now().Add(-time.Hour).Unix()},
			{Stock: watch, Kind: technicalalerts.EMAProximity, Label: "EMA50 touch", Bias: "watch", LastClose: 100},
		},
		"daily",
		1.5,
	)

	for _, expected := range []string{"Top 10 achats / renforcements", "Top 10 allègements / ventes", "MSFT", "TSLA"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("expected report to contain %q", expected)
		}
	}
	for _, removed := range []string{"Top bullish", "Top bearish", "2 signals", "Renforcement à étudier", "Protection / sortie à étudier"} {
		if strings.Contains(html, removed) {
			t.Fatalf("obsolete ranking remains: %s", removed)
		}
	}
}

func TestGenerateMultiTimeframeReportUsesDailyAndWeeklyColumns(t *testing.T) {
	stock := models.Stock{Ticker: "MSFT", Name: "Microsoft", Category: "USA"}
	html := GenerateMultiTimeframeReportWithChanges(
		nil,
		[]technicalalerts.Alert{{
			Stock: stock, Kind: technicalalerts.EMAReclaim, Label: "EMA50 reclaim",
			Bias: "bullish", LastClose: 100, ChangePercent: 1.25, Level: 99, DistancePercent: 1,
		}},
		nil,
		[]technicalalerts.Alert{{
			Stock: stock, Kind: technicalalerts.EMAProximity, Label: "EMA200 touch",
			Bias: "watch", LastClose: 100, ChangePercent: -2.5, Level: 95, DistancePercent: 5,
		}},
		1.5,
		3.0,
		map[string]int{"USA": 1},
		map[string]ChangeSummary{
			"MSFT": {DailyChange: 0.75, HasDailyChange: true, WeeklyChange: 4.25, HasWeeklyChange: true},
		},
	)

	for _, expected := range []string{"Market Signal Digest -", "Daily", "Weekly", "+0.75%", "+4.25%", "EMA50 reclaim", "EMA200 touch"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("expected report to contain %q", expected)
		}
	}
	for _, expected := range []string{"signal-table", "stock-col", "period-col"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("expected report to contain column class %q", expected)
		}
	}
	if got := strings.Count(html, `<span class="ticker" style="color:#243043;font-size:14px">MSFT</span>`); got != 1 {
		t.Fatalf("expected one ticker row for multi-timeframe grouped signals, got %d", got)
	}
}

func TestDigestDivergencesInColumnAndCards(t *testing.T) {
	stock := models.Stock{Ticker: "TEST", Name: "Example", Category: "Test"}
	div := func(kind string) divergencealerts.Alert {
		return divergencealerts.Alert{Stock: stock, Divergence: chartcalc.Divergence{Kind: kind, FromTime: 1700000000, ToTime: 1701000000}}
	}
	opts := RankingOptions{Stocks: []models.Stock{stock}, Evaluations: map[string]models.Evaluation{"TEST": {Version: models.EvaluationVersion, Eligible: true, QuoteType: "ETF", Signal: "BUY", Score: 5, Warnings: []string{"Provisoire", "Date de résultats inconnue", "WAIT EARNINGS"}}}}
	result := GenerateMultiTimeframeReportWithChanges([]divergencealerts.Alert{div("bearish")}, nil, []divergencealerts.Alert{div("bullish")}, nil, 1.5, 3, nil, nil, opts)
	for _, want := range []string{`class="divergence-col" style="background:#f0f3f6;color:#637184;font-size:11px">Divergence`, `style="color:#e74c3c;background:#fff4f3;`, `style="color:#27ae60;background:#e8f5ed;`, "WAIT EARNINGS"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(result, "Provisoire") || strings.Contains(result, "Résultats inconnus") {
		t.Fatal("unwanted warnings")
	}
	if strings.Count(result, "BEARISH RSI divergence") != 2 || strings.Count(result, "BULLISH RSI divergence") != 2 {
		t.Fatal("divergences should occur once in card and once in divergence column")
	}
}

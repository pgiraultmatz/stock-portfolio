package divergencealerts

import (
	"net/url"
	"testing"
	"time"

	"stock-portfolio/internal/chartcalc"
	"stock-portfolio/internal/config"
)

func TestDivergenceOnLatestCandle(t *testing.T) {
	div := chartcalc.Divergence{ToTime: 200}
	if !divergenceOnLatestCandle(div, 200) {
		t.Fatal("expected divergence on latest candle to be alertable")
	}
	if divergenceOnLatestCandle(div, 201) {
		t.Fatal("expected older divergence to be ignored")
	}
	if divergenceOnLatestCandle(chartcalc.Divergence{}, 201) {
		t.Fatal("expected empty divergence to be ignored")
	}
}

func TestParseTimeframe(t *testing.T) {
	tf, err := ParseTimeframe("weekly")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tf != Weekly {
		t.Fatalf("expected weekly, got %q", tf)
	}

	if _, err := ParseTimeframe("monthly"); err == nil {
		t.Fatal("expected invalid timeframe to fail")
	}
}

func TestWeeklyStateScope(t *testing.T) {
	scope := StateScope(Weekly, time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC))
	if scope != "2026-W33" {
		t.Fatalf("expected ISO week scope, got %q", scope)
	}
}

func TestFetchCandlesUsesWeeklyQuery(t *testing.T) {
	client := NewClient(testYahooConfig("https://example.test/chart"))
	u, err := url.Parse(client.chartURL("MSFT", Weekly))
	if err != nil {
		t.Fatalf("unexpected URL error: %v", err)
	}

	if gotRange := u.Query().Get("range"); gotRange != "5y" {
		t.Fatalf("expected weekly range, got %q", gotRange)
	}
	if gotInterval := u.Query().Get("interval"); gotInterval != "1wk" {
		t.Fatalf("expected weekly interval, got %q", gotInterval)
	}
}

func testYahooConfig(baseURL string) config.YahooAPIConfig {
	return config.YahooAPIConfig{BaseURL: baseURL}
}

func TestRecentDailyDivergenceExpiryAndInvalidation(t *testing.T) {
	now := time.Date(2026, 11, 7, 15, 0, 0, 0, time.UTC)
	div := chartcalc.Divergence{Kind: "bearish", ToTime: now.Add(-48 * time.Hour).Unix(), ToPrice: 100}
	candles := []chartcalc.Candle{{Time: div.ToTime, Close: 99}, {Time: now.Add(-time.Hour).Unix(), Close: 100}}
	if !recentDailyDivergence(div, candles, now) {
		t.Fatal("recent divergence should remain visible")
	}
	candles = append(candles, chartcalc.Candle{Time: now.Unix(), Close: 102})
	if recentDailyDivergence(div, candles, now) {
		t.Fatal("invalidated divergence must disappear")
	}
	div.ToTime = now.Add(-8 * 24 * time.Hour).Unix()
	if recentDailyDivergence(div, nil, now) {
		t.Fatal("old divergence must expire")
	}
}

package emaalerts

import (
	"net/url"
	"testing"
	"time"

	"stock-portfolio/internal/chartcalc"
	"stock-portfolio/internal/config"
	"stock-portfolio/internal/models"
)

func TestLatestEMA(t *testing.T) {
	candles := []chartcalc.Candle{
		{Close: 10}, {Close: 11}, {Close: 12}, {Close: 13},
	}
	ema, ok := latestEMA(candles, 3)
	if !ok {
		t.Fatal("expected EMA value")
	}
	if ema != 12 {
		t.Fatalf("expected EMA 12, got %.4f", ema)
	}
}

func TestCheckDetectsTouchedEMA(t *testing.T) {
	client := &Client{}
	candles := make([]chartcalc.Candle, 50)
	for i := range candles {
		candles[i] = chartcalc.Candle{Time: int64(i + 1), Close: 100, Low: 99, High: 101}
	}
	last := candles[len(candles)-1]
	ema, ok := latestEMA(candles, 50)
	if !ok {
		t.Fatal("expected EMA value")
	}
	touched := last.Low <= ema && last.High >= ema
	distance := ((last.Close - ema) / ema) * 100

	alert := Alert{
		Stock:           models.Stock{Ticker: "TEST", Name: "Test", Category: "USA"},
		Timeframe:       Daily,
		Period:          50,
		CandleTime:      last.Time,
		LastClose:       last.Close,
		LastLow:         last.Low,
		LastHigh:        last.High,
		EMA:             ema,
		DistancePercent: distance,
		Touched:         touched,
	}

	if !alert.Touched {
		t.Fatal("expected latest candle to touch EMA")
	}
	if key := Key(alert); key != "daily:TEST:ema50:50" {
		t.Fatalf("unexpected key %q", key)
	}
	_ = client
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

func TestChartURLUsesWeeklyQuery(t *testing.T) {
	client := NewClient(config.YahooAPIConfig{BaseURL: "https://example.test/chart"})
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

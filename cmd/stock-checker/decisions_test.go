package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"stock-portfolio/internal/chartcalc"
	"stock-portfolio/internal/config"
	"stock-portfolio/internal/models"
	"stock-portfolio/internal/technicalalerts"
	"stock-portfolio/internal/yahoo"
)

func TestDecisionCollectionReusesHistoriesAndPreservesCoverage(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Stocks = []models.Stock{{Ticker: "ETF"}, {Ticker: "EQUITY"}, {Ticker: "FAILED"}, {Ticker: "ETF"}}
	var mu sync.Mutex
	calls := map[string]int{}
	now := time.Now()
	source := decisionSources{
		series: func(_ context.Context, ticker string, tf technicalalerts.Timeframe) (technicalalerts.Series, error) {
			mu.Lock()
			calls[ticker+tf.String()]++
			mu.Unlock()
			if ticker == "FAILED" {
				return technicalalerts.Series{}, errors.New("offline")
			}
			kind := "EQUITY"
			if ticker == "ETF" {
				kind = "ETF"
			}
			s := technicalalerts.Series{QuoteType: kind, Timezone: "America/New_York"}
			step := 24 * time.Hour
			if tf == technicalalerts.Weekly {
				step *= 7
			}
			for i := 0; i < 90; i++ {
				v := 100 + float64(i*i)/100
				s.Candles = append(s.Candles, chartcalc.Candle{Time: now.Add(-time.Duration(89-i)*step - time.Hour).Unix(), Open: v, High: v + 1, Low: v - 1, Close: v})
			}
			return s, nil
		},
		valuation: func(_ context.Context, ticker string) (yahoo.ValuationData, error) {
			mu.Lock()
			calls["valuation"+ticker]++
			mu.Unlock()
			positive := .2
			return yahoo.ValuationData{PEGRatio: .8, PSGRatio: .1, EVGrossProfit: 5, FinancialQuality: &models.FinancialQuality{QuoteType: "EQUITY", CollectedAt: now, ProfitMargin: &positive, RevenueGrowth: &positive, FreeCashflow: &positive}}, nil
		},
		earnings: func(_ context.Context, ticker string) (*time.Time, error) {
			date := now.AddDate(0, 0, 3)
			return &date, nil
		},
	}
	out := collectDecisionData(context.Background(), cfg, nil, nil, source, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if len(out.Results) != 3 {
		t.Fatalf("duplicate ticker or missing failure: %d", len(out.Results))
	}
	var stamp time.Time
	for _, r := range out.Results {
		if r.Evaluation == nil {
			t.Fatal("missing snapshot")
		}
		if stamp.IsZero() {
			stamp = r.Evaluation.EvaluatedAt
		} else if !stamp.Equal(r.Evaluation.EvaluatedAt) {
			t.Fatal("inconsistent snapshot timestamp")
		}
		if r.Stock.Ticker == "FAILED" && (r.Evaluation.Eligible || len(r.Evaluation.Warnings) == 0) {
			t.Fatal("fetch failure hidden")
		}
		if r.Stock.Ticker != "FAILED" && !r.Evaluation.Eligible {
			t.Fatalf("unexpected exclusion: %+v", r.Evaluation)
		}
	}
	for key, count := range calls {
		if count != 1 {
			t.Fatalf("repeated request %s: %d", key, count)
		}
	}
	if calls["valuationETF"] != 0 || calls["valuationEQUITY"] != 1 {
		t.Fatalf("wrong valuation requests: %v", calls)
	}
}

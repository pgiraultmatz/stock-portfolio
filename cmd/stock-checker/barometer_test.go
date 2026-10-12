package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"stock-portfolio/internal/barometer"
	"stock-portfolio/internal/yahoo"
)

type fakeMarketSource struct {
	calls int
	fail  bool
	t     *testing.T
}

type fakeContextSource struct {
	quotes, histories []string
	t                 *testing.T
}

func (f *fakeContextSource) deadline(ctx context.Context) {
	f.t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 8*time.Second {
		f.t.Error("unbounded supplemental request")
	}
}

func (f *fakeContextSource) GetMarketQuote(ctx context.Context, ticker string) (yahoo.MarketQuote, error) {
	f.deadline(ctx)
	f.quotes = append(f.quotes, ticker)
	return yahoo.MarketQuote{}, fmt.Errorf("unavailable")
}

func (f *fakeContextSource) GetDailyHistory(ctx context.Context, ticker string, start, end time.Time) ([]yahoo.DailyBar, error) {
	f.deadline(ctx)
	f.histories = append(f.histories, ticker)
	if start != end.AddDate(-16, 0, 0) || end.Hour() != 0 || end.Location().String() != "America/New_York" {
		f.t.Error("incorrect history bounds")
	}
	return nil, fmt.Errorf("unavailable")
}

func TestCollectContextReusesNasdaqAndNonFatalFailures(t *testing.T) {
	source := &fakeContextSource{t: t}
	now := time.Now()
	quotes := map[string]yahoo.MarketQuote{"NQ=F": {Price: 101, PreviousClose: 100, AsOf: now}}
	summary := &barometer.Summary{Label: "Favorable", Class: "positive"}
	c := collectMarketContext(context.Background(), source, summary, quotes, now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if len(source.quotes) != 1 || source.quotes[0] != "SOXX" || len(source.histories) != 2 || source.histories[0] != "^NDX" || source.histories[1] != "SOXX" {
		t.Fatalf("duplicate or missing fetches: %+v", source)
	}
	if c.Barometer.Label != "Favorable" || c.Sector.Label != "Indisponible" || c.Monthly.Bias.Label != "Indisponible" || summary.Label != "Favorable" || len(quotes) != 1 {
		t.Fatalf("failure corrupted main context: %+v", c)
	}
}

func (f *fakeMarketSource) GetMarketQuote(ctx context.Context, ticker string) (yahoo.MarketQuote, error) {
	f.calls++
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 8*time.Second {
		f.t.Error("missing bounded request deadline")
	}
	if f.fail || ticker == "BZ=F" {
		return yahoo.MarketQuote{}, fmt.Errorf("unavailable")
	}
	return yahoo.MarketQuote{Price: 18, PreviousClose: 20, AsOf: time.Now()}, nil
}

func TestCollectBarometerNonFatalFailures(t *testing.T) {
	for _, fail := range []bool{false, true} {
		source := &fakeMarketSource{t: t, fail: fail}
		summary, vix, _ := collectBarometer(context.Background(), source, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if source.calls != 5 || summary == nil || len(summary.Factors) != 5 {
			t.Fatal("all inputs must be attempted and barometer always rendered")
		}
		if fail {
			if vix != nil || summary.Label != "Indisponible" {
				t.Fatal("failed inputs produced a signal")
			}
		} else if vix == nil || vix.Price != "18.00" || vix.Change != "-10.00%" {
			t.Fatal("VIX banner must reuse barometer quote")
		}
	}
}

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"stock-portfolio/internal/yahoo"
)

type fakeMarketSource struct {
	calls int
	fail  bool
	t     *testing.T
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
		summary, vix := collectBarometer(context.Background(), source, slog.New(slog.NewTextHandler(io.Discard, nil)))
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

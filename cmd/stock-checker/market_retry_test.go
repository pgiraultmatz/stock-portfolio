package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/config"
	"stock-portfolio/internal/yahoo"
)

type retryMarketSource struct {
	now                                  time.Time
	attempt, quoteCalls, historyCalls    int
	failQuotes, failHistory, staleQuotes int
	shortHistory                         bool
	bars                                 []yahoo.DailyBar
}

func retryFixture() *retryMarketSource {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 11, 3, 7, 30, 0, 0, loc)
	f := &retryMarketSource{now: now}
	end := time.Date(2026, 11, 3, 0, 0, 0, 0, loc)
	for d := end.AddDate(-16, 0, 0); d.Before(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			price := 100 + float64(len(f.bars))/100
			f.bars = append(f.bars, yahoo.DailyBar{Date: d.Add(16 * time.Hour), Open: price, Close: price + .01})
		}
	}
	return f
}

func (f *retryMarketSource) GetMarketQuote(ctx context.Context, ticker string) (yahoo.MarketQuote, error) {
	if err := ctx.Err(); err != nil {
		return yahoo.MarketQuote{}, err
	}
	f.quoteCalls++
	if ticker == "ES=F" {
		f.attempt++
	}
	if f.attempt <= f.failQuotes && ticker == "BZ=F" {
		return yahoo.MarketQuote{}, fmt.Errorf("Yahoo HTTP 429")
	}
	asOf := f.now.Add(-15 * time.Minute)
	if f.attempt <= f.staleQuotes {
		asOf = asOf.AddDate(0, 0, -4)
	}
	return yahoo.MarketQuote{Price: 18, PreviousClose: 17.9, AsOf: asOf}, nil
}

func (f *retryMarketSource) GetDailyHistory(ctx context.Context, ticker string, start, end time.Time) ([]yahoo.DailyBar, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.historyCalls++
	if f.attempt <= f.failHistory {
		return nil, fmt.Errorf("history HTTP 503")
	}
	if f.shortHistory {
		return f.bars[len(f.bars)-10:], nil
	}
	return f.bars, nil
}

func TestMarketRetryRecoversAndLogsCauseAndDelays(t *testing.T) {
	f := retryFixture()
	f.failQuotes = 2
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	var waits []time.Duration
	policy := defaultMarketRetryPolicy()
	policy.Now = func() time.Time { return f.now }
	policy.Wait = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	data, err := collectRequiredMarketData(context.Background(), f, logger, policy)
	if err != nil || data == nil || len(data.missing()) != 0 || f.attempt != 3 {
		t.Fatalf("retry did not recover: data=%+v err=%v attempts=%d", data, err, f.attempt)
	}
	if !reflect.DeepEqual(waits, []time.Duration{30 * time.Second, time.Minute}) {
		t.Fatalf("unexpected spacing: %v", waits)
	}
	for _, want := range []string{"HTTP 429", "ticker=\"BZ=F\"", "attempt=1", "retry_in=30s", "required market summary ready"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("missing log %q: %s", want, logs.String())
		}
	}
}

func TestMarketRetryRejectsStaleAndInsufficientHistory(t *testing.T) {
	for _, kind := range []string{"stale", "history_failure", "short_history"} {
		t.Run(kind, func(t *testing.T) {
			f := retryFixture()
			switch kind {
			case "stale":
				f.staleQuotes = 4
			case "history_failure":
				f.failHistory = 4
			case "short_history":
				f.shortHistory = true
			}
			var logs bytes.Buffer
			var waits []time.Duration
			policy := defaultMarketRetryPolicy()
			policy.Now = func() time.Time { return f.now }
			policy.Wait = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
			data, err := collectRequiredMarketData(context.Background(), f, slog.New(slog.NewTextHandler(&logs, nil)), policy)
			if err == nil || data != nil || f.attempt != 4 || !strings.Contains(err.Error(), "report blocked") || !strings.Contains(logs.String(), "level=ERROR") {
				t.Fatalf("incomplete summary allowed: data=%+v err=%v attempts=%d logs=%s", data, err, f.attempt, logs.String())
			}
			if !reflect.DeepEqual(waits, []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute}) {
				t.Fatalf("delays: %v", waits)
			}
		})
	}
}

func TestMarketRetryHistoryRecoveryAndCancellation(t *testing.T) {
	f := retryFixture()
	f.failHistory = 1
	policy := defaultMarketRetryPolicy()
	policy.Now = func() time.Time { return f.now }
	policy.Wait = func(context.Context, time.Duration) error { return nil }
	if data, err := collectRequiredMarketData(context.Background(), f, slog.Default(), policy); err != nil || data == nil || f.attempt != 2 {
		t.Fatalf("history recovery failed: %v %v", data, err)
	}
	f = retryFixture()
	f.failQuotes = 4
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	policy.Wait = func(ctx context.Context, d time.Duration) error { cancel(); return waitForMarketRetry(ctx, d) }
	if _, err := collectRequiredMarketData(ctx, f, slog.Default(), policy); !errors.Is(err, context.Canceled) || f.attempt != 1 {
		t.Fatalf("cancellation ignored: %v attempts=%d", err, f.attempt)
	}
}

func TestFullReportBlocksBeforeOtherCollectorsWhenDeadlineExpired(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output := filepath.Join(t.TempDir(), "report.html")
	err := runFullReport(ctx, config.DefaultConfig(), output, "", "", true, slog.Default(), false)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "report blocked") {
		t.Fatalf("expired deadline did not block report: %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("failed generation created an output report")
	}
}

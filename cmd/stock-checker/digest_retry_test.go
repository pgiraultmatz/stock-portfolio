package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/models"
)

func digestCoverageFixture(ready, unavailable, unsupported int) decisionSnapshot {
	var s decisionSnapshot
	change := 1.0
	for i := 0; i < ready+unavailable+unsupported; i++ {
		e := &models.Evaluation{QuoteType: "ETF", Signal: "HOLD"}
		if i < ready {
			e.DailyChange = &change
			e.WeeklyChange = &change
			e.Eligible = true
		} else if i >= ready+unavailable {
			e.QuoteType = "CRYPTOCURRENCY"
		}
		s.Results = append(s.Results, &models.StockResult{Evaluation: e})
	}
	return s
}

func TestDigestCoverageDoesNotRequireBuyCandidates(t *testing.T) {
	for _, s := range []decisionSnapshot{digestCoverageFixture(10, 0, 30), digestCoverageFixture(5, 5, 0), digestCoverageFixture(0, 0, 5)} {
		got, err := collectDigestWithRetry(context.Background(), func(context.Context) decisionSnapshot { return s }, slog.New(slog.NewTextHandler(io.Discard, nil)), digestRetryPolicy{})
		if err != nil || len(got.Results) != len(s.Results) {
			t.Fatalf("healthy HOLD/unsupported-only universe blocked: %v", err)
		}
	}
	// Missing fundamentals should not be mistaken for a Yahoo history outage.
	s := digestCoverageFixture(4, 0, 0)
	for _, r := range s.Results {
		r.Evaluation.Eligible = false
	}
	_, err := collectDigestWithRetry(context.Background(), func(context.Context) decisionSnapshot { return s }, slog.New(slog.NewTextHandler(io.Discard, nil)), digestRetryPolicy{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDigestRetriesOutageAndStopsAfterRecovery(t *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))
	calls, waits := 0, 0
	policy := digestRetryPolicy{Delays: []time.Duration{30 * time.Second, time.Minute}, Wait: func(context.Context, time.Duration) error { waits++; return nil }}
	result, err := collectDigestWithRetry(context.Background(), func(context.Context) decisionSnapshot {
		calls++
		if calls == 1 {
			return digestCoverageFixture(3, 200, 30)
		}
		return digestCoverageFixture(190, 13, 30)
	}, logger, policy)
	if err != nil || len(result.Results) == 0 || calls != 2 || waits != 1 {
		t.Fatalf("retry/recovery: calls=%d waits=%d err=%v", calls, waits, err)
	}
	if !strings.Contains(log.String(), "retry scheduled") || !strings.Contains(log.String(), "usable_histories=190") {
		t.Fatal("missing coverage diagnostics")
	}
	calls, waits = 0, 0
	result, err = collectDigestWithRetry(context.Background(), func(context.Context) decisionSnapshot { calls++; return digestCoverageFixture(3, 200, 30) }, logger, policy)
	if err == nil || len(result.Results) != 0 || calls != 3 || waits != 2 || !strings.Contains(log.String(), "level=ERROR") {
		t.Fatalf("outage not blocked/logged: %v", err)
	}
}

func TestDigestRejectsUnknownTypesAndCancellation(t *testing.T) {
	s := digestCoverageFixture(0, 5, 0)
	for _, r := range s.Results {
		r.Evaluation.QuoteType = ""
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, bad := range []decisionSnapshot{s, {}} {
		if _, err := collectDigestWithRetry(context.Background(), func(context.Context) decisionSnapshot { return bad }, logger, digestRetryPolicy{}); err == nil {
			t.Fatal("empty/unknown outage accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, err := collectDigestWithRetry(ctx, func(context.Context) decisionSnapshot { calls++; return s }, logger, digestRetryPolicy{})
	if err == nil || calls != 0 {
		t.Fatal("cancellation ignored")
	}
	ctx, cancel = context.WithCancel(context.Background())
	_, err = collectDigestWithRetry(ctx, func(context.Context) decisionSnapshot { cancel(); return digestCoverageFixture(10, 0, 0) }, logger, digestRetryPolicy{})
	if err == nil {
		t.Fatal("partial collection after cancellation accepted")
	}
}

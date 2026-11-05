package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type digestRetryPolicy struct {
	Delays []time.Duration
	Wait   func(context.Context, time.Duration) error
}

func digestHistoryCoverage(snapshot decisionSnapshot) (expected, ready, eligible int) {
	for _, r := range snapshot.Results {
		if r == nil || r.Evaluation == nil {
			expected++
			continue
		}
		e := r.Evaluation
		// Explicitly unsupported types (crypto, indexes...) do not dilute coverage.
		// Unknown types do count: both requests can have failed during an outage.
		if e.QuoteType != "EQUITY" && e.QuoteType != "ETF" && e.QuoteType != "" {
			continue
		}
		expected++
		if e.DailyChange != nil && e.WeeklyChange != nil {
			ready++
		}
		if e.Eligible {
			eligible++
		}
	}
	return
}

// Guard data coverage, not the number of BUY signals: a healthy universe can
// legitimately have no buys. Failed attempts never reach report/Gist/state writes.
func collectDigestWithRetry(ctx context.Context, collect func(context.Context) decisionSnapshot, logger *slog.Logger, policy digestRetryPolicy) (decisionSnapshot, error) {
	attempts := len(policy.Delays) + 1
	var failure error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return decisionSnapshot{}, fmt.Errorf("digest collection canceled; report blocked: %w", err)
		}
		snapshot := collect(ctx)
		if err := ctx.Err(); err != nil {
			return decisionSnapshot{}, fmt.Errorf("digest collection interrupted; report blocked: %w", err)
		}
		expected, ready, eligible := digestHistoryCoverage(snapshot)
		logger.Info("market digest data coverage", "attempt", attempt, "max_attempts", attempts, "expected_histories", expected, "usable_histories", ready, "eligible", eligible)
		if len(snapshot.Results) > 0 && (expected == 0 || ready*2 >= expected) {
			return snapshot, nil
		}
		failure = fmt.Errorf("only %d/%d equity/ETF histories usable (minimum 50%%); market digest blocked", ready, expected)
		if attempt < attempts {
			delay := policy.Delays[attempt-1]
			logger.Warn("market digest data incomplete; retry scheduled", "error", failure, "retry_in", delay)
			if err := policy.Wait(ctx, delay); err != nil {
				return decisionSnapshot{}, fmt.Errorf("digest retry interrupted; report blocked: %w", err)
			}
		}
	}
	logger.Error("market digest data unavailable; report blocked", "attempts", attempts, "error", failure)
	return decisionSnapshot{}, failure
}

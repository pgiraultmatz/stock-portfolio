package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"stock-portfolio/internal/barometer"
	"stock-portfolio/internal/report"
)

type requiredMarketData struct {
	Barometer *barometer.Summary
	Context   *barometer.MarketContext
	VIX       *report.VIXData
}

type marketRetryPolicy struct {
	Delays []time.Duration
	Now    func() time.Time
	Wait   func(context.Context, time.Duration) error
}

func defaultMarketRetryPolicy() marketRetryPolicy {
	return marketRetryPolicy{
		Delays: []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute},
		Now:    time.Now, Wait: waitForMarketRetry,
	}
}

func waitForMarketRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// A report must contain all five usable factors, sector context and both
// historical summaries. HTTP success alone does not imply usable market data.
func (data requiredMarketData) missing() []string {
	var missing []string
	if data.Barometer == nil {
		missing = append(missing, "baromètre actions US")
	} else {
		for _, factor := range data.Barometer.Factors {
			if factor.Value == "Indisponible" {
				missing = append(missing, factor.Name+": "+factor.Observation)
			}
		}
		if len(data.Barometer.Factors) != len(barometer.Instruments) || data.Barometer.Label == "Indisponible" {
			missing = append(missing, "baromètre actions US")
		}
	}
	if data.Context == nil {
		return append(missing, "AI/Semis", "backtest du jour", "saisonnalité")
	}
	if data.Context.Sector.Label == "Indisponible" {
		missing = append(missing, "AI/Semis")
	}
	if data.Context.Weekday.Bias.Label == "Indisponible" {
		missing = append(missing, "backtest du jour")
	}
	if data.Context.Monthly.Bias.Label == "Indisponible" {
		missing = append(missing, "saisonnalité")
	}
	return missing
}

func collectRequiredMarketData(ctx context.Context, source marketContextSource, logger *slog.Logger, policy marketRetryPolicy) (*requiredMarketData, error) {
	attempts := len(policy.Delays) + 1
	var lastMissing []string
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("market summary unavailable; report blocked before attempt %d/%d: %w", attempt, attempts, err)
		}
		attemptLogger := logger.With("attempt", attempt, "max_attempts", attempts)
		attemptLogger.Info("collecting required market summary")
		summary, vix, quotes := collectBarometerAt(ctx, source, attemptLogger, policy.Now)
		market := collectMarketContext(ctx, source, summary, quotes, policy.Now(), attemptLogger)
		data := &requiredMarketData{Barometer: summary, Context: market, VIX: vix}
		lastMissing = data.missing()
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("market summary unavailable; report blocked after attempt %d/%d (%s): %w", attempt, attempts, strings.Join(lastMissing, "; "), err)
		}
		if len(lastMissing) == 0 {
			attemptLogger.Info("required market summary ready", "barometer", summary.Label, "sector", market.Sector.Label)
			return data, nil
		}
		if attempt < attempts {
			delay := policy.Delays[attempt-1]
			attemptLogger.Warn("market summary incomplete; retry scheduled", "missing", strings.Join(lastMissing, "; "), "retry_in", delay)
			if err := policy.Wait(ctx, delay); err != nil {
				return nil, fmt.Errorf("market summary retry interrupted; report blocked (%s): %w", strings.Join(lastMissing, "; "), err)
			}
		}
	}
	err := fmt.Errorf("market summary unavailable after %d attempts; report blocked: %s", attempts, strings.Join(lastMissing, "; "))
	logger.Error("required market summary failed", "attempts", attempts, "error", err)
	return nil, err
}

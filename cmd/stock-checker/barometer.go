package main

import (
	"context"
	"log/slog"
	"time"

	"stock-portfolio/internal/barometer"
	"stock-portfolio/internal/report"
	"stock-portfolio/internal/yahoo"
)

type marketQuoteSource interface {
	GetMarketQuote(context.Context, string) (yahoo.MarketQuote, error)
}

func collectBarometer(ctx context.Context, source marketQuoteSource, logger *slog.Logger) (*barometer.Summary, *report.VIXData, map[string]yahoo.MarketQuote) {
	quotes := make(map[string]yahoo.MarketQuote)
	logger.Info("fetching market barometer", "instruments", len(barometer.Instruments))
	for _, instrument := range barometer.Instruments {
		requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		quote, err := source.GetMarketQuote(requestCtx, instrument.Ticker)
		cancel()
		if err != nil {
			logger.Warn("market barometer input unavailable", "ticker", instrument.Ticker, "error", err)
			continue
		}
		quotes[instrument.Ticker] = quote
	}
	summary := barometer.Evaluate(quotes, time.Now())
	var vix *report.VIXData
	if quote, ok := quotes["^VIX"]; ok {
		vix = report.NewVIXData(quote.Price, quote.ChangePercent())
	}
	logger.Info("market barometer ready", "label", summary.Label, "coverage", summary.Coverage)
	return summary, vix, quotes
}

type marketContextSource interface {
	marketQuoteSource
	GetDailyHistory(context.Context, string, time.Time, time.Time) ([]yahoo.DailyBar, error)
}

func collectMarketContext(ctx context.Context, source marketContextSource, summary *barometer.Summary, quotes map[string]yahoo.MarketQuote, now time.Time, logger *slog.Logger) *barometer.MarketContext {
	// Copy inputs so supplemental collection cannot change the main barometer inputs.
	sectorQuotes := make(map[string]yahoo.MarketQuote, len(quotes)+1)
	for ticker, quote := range quotes {
		sectorQuotes[ticker] = quote
	}
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	quote, err := source.GetMarketQuote(requestCtx, "SOXX")
	cancel()
	if err != nil {
		logger.Warn("sector quote unavailable", "ticker", "SOXX", "error", err)
	} else {
		sectorQuotes["SOXX"] = quote
	}
	loc, _ := time.LoadLocation("America/New_York")
	now = now.In(loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	start := end.AddDate(-16, 0, 0)
	history := make(map[string][]yahoo.DailyBar, 2)
	for _, ticker := range []string{"^NDX", "SOXX"} {
		requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		bars, err := source.GetDailyHistory(requestCtx, ticker, start, end)
		cancel()
		if err != nil {
			logger.Warn("market history unavailable", "ticker", ticker, "error", err)
			continue
		}
		history[ticker] = bars
	}
	return barometer.EvaluateContext(summary, sectorQuotes, history, now)
}

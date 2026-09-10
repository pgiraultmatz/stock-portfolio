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

func collectBarometer(ctx context.Context, source marketQuoteSource, logger *slog.Logger) (*barometer.Summary, *report.VIXData) {
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
	return summary, vix
}

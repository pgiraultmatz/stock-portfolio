package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"stock-portfolio/internal/config"
	"stock-portfolio/internal/decisions"
	"stock-portfolio/internal/models"
	"stock-portfolio/internal/technicalalerts"
	"stock-portfolio/internal/yahoo"
)

type decisionSnapshot struct {
	Results       []*models.StockResult
	Daily, Weekly map[string]technicalalerts.Series
}

type decisionSources struct {
	series    func(context.Context, string, technicalalerts.Timeframe) (technicalalerts.Series, error)
	valuation func(context.Context, string) (yahoo.ValuationData, error)
	earnings  func(context.Context, string) (*time.Time, error)
}

func collectDecisions(ctx context.Context, cfg *config.Config, seeds []*models.StockResult, logger *slog.Logger) decisionSnapshot {
	history := technicalalerts.NewClient(cfg.YahooAPI)
	quotes := yahoo.NewClient(cfg.YahooAPI)
	cacheCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	cache := config.LoadStockDataContext(cacheCtx)
	cancel()
	return collectDecisionData(ctx, cfg, seeds, cache, decisionSources{history.FetchSeries, quotes.GetValuation, quotes.GetNextEarningsDate}, logger)
}

func collectDecisionData(ctx context.Context, cfg *config.Config, seeds []*models.StockResult, cache *config.StockDataFile, source decisionSources, logger *slog.Logger) decisionSnapshot {
	byTicker := map[string]*models.StockResult{}
	for _, r := range seeds {
		if r.Error == nil {
			byTicker[r.Stock.Ticker] = r
		}
	}
	out := decisionSnapshot{Daily: map[string]technicalalerts.Series{}, Weekly: map[string]technicalalerts.Series{}}
	inputs := map[string]decisions.Input{}
	jobs := make(chan models.Stock, len(cfg.Stocks))
	seen := map[string]bool{}
	for _, stock := range cfg.Stocks {
		if !seen[stock.Ticker] {
			jobs <- stock
			seen[stock.Ticker] = true
		}
	}
	close(jobs)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < max(1, min(cfg.Concurrency, 4)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for stock := range jobs {
				r := byTicker[stock.Ticker]
				seeded := r != nil
				if r == nil {
					r = &models.StockResult{Stock: stock}
				}
				d, de := source.series(ctx, stock.Ticker, technicalalerts.Daily)
				w, we := source.series(ctx, stock.Ticker, technicalalerts.Weekly)
				if de != nil {
					logger.Warn("decision daily history unavailable", "ticker", stock.Ticker, "error", de)
				}
				if we != nil {
					logger.Warn("decision weekly history unavailable", "ticker", stock.Ticker, "error", we)
				}
				kind := d.QuoteType
				if kind == "" {
					kind = w.QuoteType
				}
				if kind == "EQUITY" && !seeded {
					var f config.StockFundamentals
					if cache != nil {
						f = cache.Stocks[stock.Ticker]
					}
					if f.FinancialQuality != nil && recentFinancials(f.FinancialQuality.CollectedAt, time.Now()) && recentFinancials(cache.UpdatedAt, time.Now()) {
						r.FinancialQuality = f.FinancialQuality
						r.PEGRatio = f.PEGRatio
						r.PSGRatio = f.PSGRatio
						r.EVGrossProfit = f.EVGrossProfit
						r.TargetPrice = f.TargetPrice
					} else {
						v, err := source.valuation(ctx, stock.Ticker)
						if err != nil {
							logger.Warn("decision financials unavailable", "ticker", stock.Ticker, "error", err)
						} else {
							r.FinancialQuality = v.FinancialQuality
							r.PEGRatio = v.PEGRatio
							r.PSGRatio = v.PSGRatio
							r.EVGrossProfit = v.EVGrossProfit
							r.TargetPrice = v.TargetPrice
						}
					}
					if date, err := time.Parse(time.RFC3339, f.NextEarnings); err == nil && date.After(time.Now()) {
						r.NextEarningsDate = &date
					} else {
						date, err := source.earnings(ctx, stock.Ticker)
						if err == nil {
							r.NextEarningsDate = date
						}
					}
				}
				input := decisions.Input{QuoteType: kind, Daily: decisions.Series{Candles: d.Candles, Timezone: d.Timezone, SessionEnd: d.SessionEnd}, Weekly: decisions.Series{Candles: w.Candles, Timezone: w.Timezone, SessionEnd: w.SessionEnd}, TargetPrice: r.TargetPrice, PEG: r.PEGRatio, PSG: r.PSGRatio, EVGP: r.EVGrossProfit, Quality: r.FinancialQuality, Earnings: r.NextEarningsDate}
				mu.Lock()
				out.Results = append(out.Results, r)
				out.Daily[stock.Ticker] = d
				out.Weekly[stock.Ticker] = w
				inputs[stock.Ticker] = input
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	now := time.Now()
	for _, r := range out.Results {
		r.Evaluation = decisions.Evaluate(inputs[r.Stock.Ticker], now)
	}
	logger.Info("shared evaluations ready", "stocks", len(out.Results), "version", models.EvaluationVersion)
	return out
}

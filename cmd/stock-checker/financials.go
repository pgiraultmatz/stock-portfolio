package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"stock-portfolio/internal/config"
	"stock-portfolio/internal/marketdigest"
	"stock-portfolio/internal/yahoo"
)

func recentFinancials(at, now time.Time) bool {
	return !at.IsZero() && !at.After(now) && now.Sub(at) <= 24*time.Hour
}

func collectDigestFinancials(ctx context.Context, cfg *config.Config, now time.Time, logger *slog.Logger) map[string]marketdigest.FinancialContext {
	if len(cfg.Stocks) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	cacheCtx, cacheCancel := context.WithTimeout(ctx, 10*time.Second)
	cache := config.LoadStockDataContext(cacheCtx)
	cacheCancel()
	client := yahoo.NewClient(cfg.YahooAPI)
	logger.Info("scanning independent financial universe", "stocks", len(cfg.Stocks))
	jobs := make(chan int, len(cfg.Stocks))
	seen := make(map[string]bool)
	for i, stock := range cfg.Stocks {
		if !seen[stock.Ticker] {
			jobs <- i
			seen[stock.Ticker] = true
		}
	}
	close(jobs)
	result := make(map[string]marketdigest.FinancialContext, len(seen))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < max(1, min(cfg.Concurrency, 4)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				stock := cfg.Stocks[i]
				entry := enrichDigestFinancials(ctx, []string{stock.Ticker}, cache, now, client.GetValuation, logger)[stock.Ticker]
				entry.Stock = stock
				mu.Lock()
				result[stock.Ticker] = entry
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return result
}

func enrichDigestFinancials(ctx context.Context, tickers []string, cache *config.StockDataFile, now time.Time, fetch func(context.Context, string) (yahoo.ValuationData, error), logger *slog.Logger) map[string]marketdigest.FinancialContext {
	result := make(map[string]marketdigest.FinancialContext, len(tickers))
	for _, ticker := range tickers {
		if _, exists := result[ticker]; exists {
			continue
		}
		var entry marketdigest.FinancialContext
		if cache != nil {
			if data, ok := cache.Stocks[ticker]; ok {
				entry.Data = data
				entry.ValuationAt = cache.UpdatedAt
			}
		}
		q := entry.Data.FinancialQuality
		if q != nil && q.QuoteType != "" && q.QuoteType != "EQUITY" {
			result[ticker] = entry
			continue
		}
		if q != nil && recentFinancials(q.CollectedAt, now) && recentFinancials(entry.ValuationAt, now) {
			result[ticker] = entry
			continue
		}
		if ctx.Err() != nil {
			entry.RefreshFailed = true
			result[ticker] = entry
			continue
		}
		data, err := fetch(ctx, ticker)
		if err != nil {
			logger.Warn("financial context refresh failed", "ticker", ticker, "error", err)
			entry.RefreshFailed = true
		} else {
			entry.Data.FinancialQuality = data.FinancialQuality
			// Old Gists can have fresh valuation ratios without the new quality
			// fields. Preserve those ratios while populating the missing fields.
			if !recentFinancials(entry.ValuationAt, now) || (entry.Data.PEGRatio == 0 && entry.Data.PSGRatio == 0 && entry.Data.EVGrossProfit == 0) {
				entry.Data.PEGRatio = data.PEGRatio
				entry.Data.PSGRatio = data.PSGRatio
				entry.Data.EVGrossProfit = data.EVGrossProfit
				entry.ValuationAt = now
			}
		}
		result[ticker] = entry
	}
	return result
}

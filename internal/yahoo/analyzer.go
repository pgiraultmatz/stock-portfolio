// Package yahoo provides a client for the Yahoo Finance API.
package yahoo

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"stock-portfolio/internal/analysis"
	"stock-portfolio/internal/config"
	"stock-portfolio/internal/models"
)

// Analyzer fetches and analyzes stock data.
type Analyzer struct {
	client        *Client
	rsiCalculator *analysis.RSICalculator
	concurrency   int
	logger        *slog.Logger
}

// NewAnalyzer creates a new stock analyzer.
func NewAnalyzer(cfg *config.Config, logger *slog.Logger) *Analyzer {
	if logger == nil {
		logger = slog.Default()
	}

	return &Analyzer{
		client:        NewClient(cfg.YahooAPI),
		rsiCalculator: analysis.NewRSICalculator(14),
		concurrency:   cfg.Concurrency,
		logger:        logger,
	}
}

// AnalyzeStock analyzes a single stock.
func (a *Analyzer) AnalyzeStock(ctx context.Context, stock models.Stock) *models.StockResult {
	result := &models.StockResult{Stock: stock}

	// Fetch chart data and valuation concurrently.
	type chartResult struct {
		data *StockData
		err  error
	}
	type valuationResult struct {
		target, peg, psg, evgp float64
		err                    error
	}

	chartCh := make(chan chartResult, 1)
	valCh := make(chan valuationResult, 1)

	go func() {
		d, err := a.client.GetChartData(ctx, stock.Ticker)
		chartCh <- chartResult{d, err}
	}()
	go func() {
		v, err := a.client.GetValuation(ctx, stock.Ticker)
		valCh <- valuationResult{v.TargetPrice, v.PEGRatio, v.PSGRatio, v.EVGrossProfit, err}
	}()

	cr := <-chartCh
	if cr.err != nil {
		a.logger.Warn("failed to fetch stock data", "ticker", stock.Ticker, "error", cr.err)
		result.Error = cr.err
		<-valCh
		return result
	}
	if len(cr.data.Closes) < 15 {
		a.logger.Warn("insufficient data for analysis", "ticker", stock.Ticker, "weeks", len(cr.data.Closes))
		result.Error = fmt.Errorf("insufficient data")
		<-valCh
		return result
	}

	result.CurrentPrice = cr.data.CurrentPrice
	result.Currency = cr.data.Currency

	j1, j2, err := a.client.GetPreviousDayClose(ctx, stock.Ticker)
	if err != nil {
		a.logger.Warn("failed to fetch daily closes", "ticker", stock.Ticker, "error", err)
	} else if j1 > 0 {
		change := ((cr.data.CurrentPrice - j1) / j1) * 100
		if math.Abs(change) < 0.05 && j2 > 0 {
			change = ((j1 - j2) / j2) * 100
		}
		result.ChangePercent = change
	}

	result.RSI = a.rsiCalculator.Calculate(cr.data.Closes)

	vr := <-valCh
	if vr.err != nil {
		a.logger.Warn("failed to fetch valuation", "ticker", stock.Ticker, "error", vr.err)
	} else {
		result.TargetPrice = vr.target
		result.PEGRatio = vr.peg
		result.PSGRatio = vr.psg
		result.EVGrossProfit = vr.evgp
	}

	return result
}

// FetchEarningsDates fetches the next earnings date for each result sequentially
// by parsing Yahoo Finance quote pages, with 200ms between requests.
func (a *Analyzer) FetchEarningsDates(ctx context.Context, results []*models.StockResult) {
	first := true
	for _, r := range results {
		if r.NextEarningsDate != nil {
			continue // already populated from cached Gist data
		}
		if !first {
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
		first = false
		date, err := a.client.GetNextEarningsDate(ctx, r.Stock.Ticker)
		if err != nil {
			a.logger.Warn("failed to fetch earnings date", "ticker", r.Stock.Ticker, "error", err)
			continue
		}
		r.NextEarningsDate = date
	}
}

// AnalyzeAll analyzes multiple stocks concurrently.
func (a *Analyzer) AnalyzeAll(ctx context.Context, stocks []models.Stock) []*models.StockResult {
	results := make([]*models.StockResult, len(stocks))

	// Use a semaphore to limit concurrency
	sem := make(chan struct{}, a.concurrency)
	var wg sync.WaitGroup

	for i, stock := range stocks {
		wg.Add(1)
		go func(idx int, s models.Stock) {
			defer wg.Done()

			// Acquire semaphore
			sem <- struct{}{}
			defer func() { <-sem }()

			// Check for context cancellation
			select {
			case <-ctx.Done():
				results[idx] = &models.StockResult{
					Stock: s,
					Error: ctx.Err(),
				}
				return
			default:
			}

			results[idx] = a.AnalyzeStock(ctx, s)
		}(i, stock)
	}

	wg.Wait()

	// Filter out nil results and failed analyses
	validResults := make([]*models.StockResult, 0, len(results))
	for _, r := range results {
		if r != nil && r.Error == nil {
			validResults = append(validResults, r)
		}
	}

	return validResults
}

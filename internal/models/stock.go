// Package models defines the domain models for the stock checker application.
package models

import "time"

// Stock represents a stock or financial instrument to track.
type Stock struct {
	Ticker      string `json:"ticker" yaml:"ticker"`
	Name        string `json:"name" yaml:"name"`
	Category    string `json:"category" yaml:"category"`
	InPortfolio *bool  `json:"inPortfolio,omitempty" yaml:"inPortfolio,omitempty"`
}

// IsInPortfolio returns true when the stock is in the portfolio (nil = true by default).
func (s Stock) IsInPortfolio() bool {
	return s.InPortfolio == nil || *s.InPortfolio
}

// StockResult contains the analysis results for a stock.
type StockResult struct {
	Stock            Stock
	CurrentPrice     float64
	ChangePercent    float64
	RSI              float64
	Currency         string
	NextEarningsDate *time.Time
	TargetPrice      float64 // analyst mean price target (0 = not available)
	PEGRatio         float64 // trailing PEG ratio, 5yr expected IBES (0 = not available)
	PSGRatio         float64 // EV/fwdRevenue ÷ fwdRevenueGrowth% (0 = not available)
	EVGrossProfit    float64 // EV / Gross Profit TTM (0 = not available)
	Error            error   // Non-nil if analysis failed
}

// IsOversold returns true if RSI indicates oversold condition (< 30).
func (r *StockResult) IsOversold() bool {
	return r.RSI < 30
}

// IsOverbought returns true if RSI indicates overbought condition (> 70).
func (r *StockResult) IsOverbought() bool {
	return r.RSI > 70
}

// IsPositive returns true if the change percent is positive.
func (r *StockResult) IsPositive() bool {
	return r.ChangePercent > 0.01
}

// IsNegative returns true if the change percent is negative.
func (r *StockResult) IsNegative() bool {
	return r.ChangePercent < -0.01
}

// Category represents a grouping of stocks with display metadata.
type Category struct {
	Name           string `json:"name" yaml:"name"`
	Emoji          string `json:"emoji" yaml:"emoji"`
	Order          int    `json:"order" yaml:"order"`
	Narrative      string `json:"narrative" yaml:"narrative"`             // optional investment thesis
	NarrativeScore int    `json:"narrative_score" yaml:"narrative_score"` // optional 1-10, 0 = not set
}

// CategoryGroup represents a category with its associated stock results.
type CategoryGroup struct {
	Category Category
	Results  []*StockResult
}

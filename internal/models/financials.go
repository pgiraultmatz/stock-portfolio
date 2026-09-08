package models

import "time"

// FinancialQuality preserves missing values separately from real zero values.
// CollectedAt is a retrieval date, not a financial statement period.
type FinancialQuality struct {
	CollectedAt       time.Time `json:"collected_at"`
	QuoteType         string    `json:"quote_type,omitempty"`
	Currency          string    `json:"currency,omitempty"`
	TrailingPE        *float64  `json:"trailing_pe,omitempty"`
	ForwardPE         *float64  `json:"forward_pe,omitempty"`
	ProfitMargin      *float64  `json:"profit_margin,omitempty"`
	OperatingMargin   *float64  `json:"operating_margin,omitempty"`
	RevenueGrowth     *float64  `json:"revenue_growth,omitempty"`
	OperatingCashflow *float64  `json:"operating_cashflow,omitempty"`
	FreeCashflow      *float64  `json:"free_cashflow,omitempty"`
	TotalDebt         *float64  `json:"total_debt,omitempty"`
	TotalCash         *float64  `json:"total_cash,omitempty"`
}

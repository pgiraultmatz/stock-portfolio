package models

import (
	"strings"
	"time"
)

const EvaluationVersion = "swing-v1"

// Evaluation is a dated decision snapshot, not a prediction or a live quote.
type Evaluation struct {
	Pullback          *PullbackEvaluation `json:"pullback,omitempty"`
	Version           string              `json:"version"`
	EvaluatedAt       time.Time           `json:"evaluated_at"`
	QuoteType         string              `json:"quote_type"`
	Basis             string              `json:"basis"`
	Eligible          bool                `json:"eligible"`
	Score             float64             `json:"score"`
	Weekly            float64             `json:"weekly"`
	Daily             float64             `json:"daily"`
	Fundamental       *float64            `json:"fundamental,omitempty"`
	Signal            string              `json:"signal"`
	Reasons           []string            `json:"reasons"`
	Warnings          []string            `json:"warnings"`
	Provisional       bool                `json:"provisional"`
	EarningsAt        *time.Time          `json:"earnings_at,omitempty"`
	AnalystTarget     *float64            `json:"analyst_target,omitempty"`
	NetDebtToCashflow *float64            `json:"net_debt_to_cashflow,omitempty"`
	Price             float64             `json:"price"`
	PriceAt           time.Time           `json:"price_at"`
	WeeklyAt          time.Time           `json:"weekly_at"`
	DailyChange       *float64            `json:"daily_change,omitempty"`
	WeeklyChange      *float64            `json:"weekly_change,omitempty"`
}

// PullbackEvaluation is an independent screen; it never changes the swing badge.
type PullbackEvaluation struct {
	Eligible   bool    `json:"eligible"`
	Drawdown20 float64 `json:"drawdown_20"` // positive decline from the highest close in 20 sessions
	Quality    float64 `json:"quality"`
	Valuation  float64 `json:"valuation"`
	NetLoss    bool    `json:"net_loss"`
}

func (e *Evaluation) Current() bool { return e != nil && e.Version == EvaluationVersion }

// Badge keeps legacy signal fields consistent across all consumers.
func (e *Evaluation) Badge() (string, string) {
	if !e.Current() {
		return "À recalculer", ""
	}
	if !e.Eligible {
		return "Indisponible", strings.Join(e.Warnings, " · ")
	}
	return e.Signal, strings.Join(e.Warnings, " · ")
}

func SignalClass(signal string) string {
	switch signal {
	case "STRONG BUY", "BUY", "ACCUMULATE", "HOLD", "LIGHT TRIM", "TRIM", "SELL", "STRONG SELL":
		return "signal-" + strings.ReplaceAll(strings.ToLower(signal), " ", "-")
	default:
		return "neutral"
	}
}

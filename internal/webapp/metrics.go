package webapp

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type StockMetrics struct {
	RSI           float64 `json:"rsi"`
	RSILabel      string  `json:"rsiLabel"`
	RSIClass      string  `json:"rsiClass"`
	TargetPrice   float64 `json:"targetPrice"`
	TargetPct     float64 `json:"targetPct"`
	PEGRatio      float64 `json:"pegRatio"`
	PEGLabel      string  `json:"pegLabel"`
	PEGClass      string  `json:"pegClass"`
	PSGRatio      float64 `json:"psgRatio"`
	PSGLabel      string  `json:"psgLabel"`
	PSGClass      string  `json:"psgClass"`
	EVGPRatio     float64 `json:"evgpRatio"`
	EVGPLabel     string  `json:"evgpLabel"`
	EVGPClass     string  `json:"evgpClass"`
	Earnings      string  `json:"earnings"`
	EarningsDate  string  `json:"earningsDate,omitempty"` // ISO 2006-01-02
	EarningsClass string  `json:"earningsClass"`
	Signal        string  `json:"signal"`
	SignalClass   string  `json:"signalClass"`
	SignalNote    string  `json:"signalNote"`
	UpdatedAt     string  `json:"updatedAt,omitempty"`
}

// ---- Label helpers --------------------------------------------------------

func rsiLabel(rsi float64) (label, class string) {
	switch {
	case rsi < 30:
		return "STRONG OVERSOLD", "rsi-strong-oversold"
	case rsi < 40:
		return "ACCUMULATION", "rsi-accumulation"
	case rsi < 55:
		return "NEUTRAL", "rsi-neutral"
	case rsi < 65:
		return "MOMENTUM", "rsi-momentum"
	case rsi < 75:
		return "EXTENDED", "rsi-extended"
	case rsi < 85:
		return "OVERBOUGHT", "rsi-overbought"
	default:
		return "VERY OVERBOUGHT", "rsi-very-overbought"
	}
}

func pegLabel(peg float64) (label, class string) {
	switch {
	case peg < 1.0:
		return "UNDERVALUED", "val-under"
	case peg < 1.5:
		return "REASONABLE", "val-reasonable"
	case peg < 2.2:
		return "FAIR", "val-fair"
	case peg < 3.0:
		return "EXPENSIVE", "val-expensive"
	default:
		return "OVERVALUED", "val-over"
	}
}

func psgLabel(psg float64) (label, class string) {
	switch {
	case psg < 0.15:
		return "ATTRACTIVE", "psg-very-attractive"
	case psg < 0.30:
		return "REASONABLE", "psg-reasonable"
	case psg < 0.50:
		return "EXPENSIVE", "psg-expensive"
	default:
		return "PREMIUM", "psg-very-expensive"
	}
}

func evgpLabel(evgp float64) (label, class string) {
	switch {
	case evgp < 8:
		return "ATTRACTIVE", "evgp-attractive"
	case evgp < 15:
		return "REASONABLE", "evgp-reasonable"
	case evgp < 25:
		return "EXPENSIVE", "evgp-expensive"
	default:
		return "VERY EXPENSIVE", "evgp-very-expensive"
	}
}

func signalClass(signal string) string {
	switch signal {
	case "STRONG BUY":
		return "signal-strong-buy"
	case "BUY":
		return "signal-buy"
	case "ACCUMULATE":
		return "signal-accumulate"
	case "HOLD":
		return "signal-hold"
	case "LIGHT TRIM":
		return "signal-light-trim"
	case "TRIM":
		return "signal-trim"
	case "SELL":
		return "signal-sell"
	case "STRONG SELL":
		return "signal-strong-sell"
	default:
		return ""
	}
}

func earningsInfo(isoDate string) (display, class string) {
	if isoDate == "" {
		return "", ""
	}
	t, err := time.Parse(time.RFC3339, isoDate)
	if err != nil {
		t, err = time.Parse("2006-01-02", isoDate)
		if err != nil {
			return isoDate, ""
		}
	}
	paris, _ := time.LoadLocation("Europe/Paris")
	tParis := t.In(paris)
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	day := time.Date(tParis.Year(), tParis.Month(), tParis.Day(), 0, 0, 0, 0, now.Location())
	daysAway := int(day.Sub(today).Hours() / 24)
	display = tParis.Format("Jan 2")
	if tParis.Hour() != 0 {
		display += tParis.Format(" · 15h04")
	}
	switch {
	case daysAway <= 7:
		class = "earnings-soon"
	case daysAway <= 30:
		class = "earnings-upcoming"
	default:
		class = "earnings-later"
	}
	return
}

// ---- HTTP handler ---------------------------------------------------------

func (s *Server) getMetrics(w http.ResponseWriter, r *http.Request) {
	type getter interface {
		GetStockData(context.Context, string) (*StockDataFile, error)
	}
	g, ok := s.store.(getter)
	if !ok {
		http.Error(w, "not supported", http.StatusNotImplemented)
		return
	}

	userID := r.Context().Value(ctxUserID).(string)
	sd, err := g.GetStockData(r.Context(), userID)
	if err != nil || sd == nil {
		http.Error(w, "no metrics data available", http.StatusNotFound)
		return
	}

	updatedAt := sd.UpdatedAt.Format(time.RFC3339)
	results := make(map[string]StockMetrics, len(sd.Stocks))
	for ticker, f := range sd.Stocks {
		rsiLbl, rsiCls := rsiLabel(f.RSI)
		pegLbl, pegCls := "", ""
		if f.PEGRatio > 0 {
			pegLbl, pegCls = pegLabel(f.PEGRatio)
		}
		psgLbl, psgCls := "", ""
		if f.PSGRatio > 0 {
			psgLbl, psgCls = psgLabel(f.PSGRatio)
		}
		evgpLbl, evgpCls := "", ""
		if f.EVGrossProfit > 0 {
			evgpLbl, evgpCls = evgpLabel(f.EVGrossProfit)
		}
		earningsDisplay, earningsCls := earningsInfo(f.NextEarnings)

		results[ticker] = StockMetrics{
			RSI: f.RSI, RSILabel: rsiLbl, RSIClass: rsiCls,
			TargetPrice: f.TargetPrice, TargetPct: f.TargetPct,
			PEGRatio: f.PEGRatio, PEGLabel: pegLbl, PEGClass: pegCls,
			PSGRatio: f.PSGRatio, PSGLabel: psgLbl, PSGClass: psgCls,
			EVGPRatio: f.EVGrossProfit, EVGPLabel: evgpLbl, EVGPClass: evgpCls,
			Earnings: earningsDisplay, EarningsDate: f.NextEarnings, EarningsClass: earningsCls,
			Signal: f.Signal, SignalClass: signalClass(f.Signal), SignalNote: f.SignalNote,
			UpdatedAt: updatedAt,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

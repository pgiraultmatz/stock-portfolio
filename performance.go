package main

import (
	"encoding/csv"
	"math"
	"sort"
	"strconv"
	"strings"
)

// ── Types ─────────────────────────────────────────────────────────────────────

type TRTransaction struct {
	Date       string
	Type       string
	AssetClass string
	Name       string
	Symbol     string
	Shares     float64
	Price      float64
	Amount     float64
	Fee        float64
	Tax        float64
}

// MonthlyData maps "YYYY-MM" → category → amount.
type MonthlyData map[string]map[string]float64

type PerfTRYearRecord struct {
	Year      string `json:"year"`
	IsCurrent bool   `json:"isCurrent"`
}

// ── Parser ────────────────────────────────────────────────────────────────────

var trKeepTypes = map[string]bool{
	"BUY": true, "SELL": true, "DIVIDEND": true,
	"INTEREST_PAYMENT": true, "FREE_RECEIPT": true,
	"BENEFITS_SAVEBACK": true, "STOCKPERK": true,
}

// parseTRCSV parses a Trade Republic activity CSV export.
// Returns sorted transactions and the detected year (from median row).
func parseTRCSV(content string) ([]TRTransaction, string, error) {
	r := csv.NewReader(strings.NewReader(content))
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	records, err := r.ReadAll()
	if err != nil {
		return nil, "", err
	}
	if len(records) < 2 {
		return nil, "", nil
	}
	headers := make(map[string]int, len(records[0]))
	for i, h := range records[0] {
		headers[strings.ToLower(strings.TrimSpace(h))] = i
	}
	get := func(row []string, h string) string {
		idx, ok := headers[h]
		if !ok || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}
	pf := func(s string) float64 {
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}

	var txs []TRTransaction
	for _, row := range records[1:] {
		typ := get(row, "type")
		if !trKeepTypes[typ] {
			continue
		}
		date := get(row, "date")
		if date == "" {
			if dt := get(row, "datetime"); len(dt) >= 10 {
				date = dt[:10]
			}
		}
		if date == "" {
			continue
		}
		sym := get(row, "symbol")
		if sym == "" {
			sym = get(row, "name")
		}
		txs = append(txs, TRTransaction{
			Date: date, Type: typ,
			AssetClass: get(row, "asset_class"),
			Name:       get(row, "name"),
			Symbol:     sym,
			Shares:     pf(get(row, "shares")),
			Price:      pf(get(row, "price")),
			Amount:     pf(get(row, "amount")),
			Fee:        pf(get(row, "fee")),
			Tax:        pf(get(row, "tax")),
		})
	}
	sort.Slice(txs, func(i, j int) bool { return txs[i].Date < txs[j].Date })

	year := ""
	if len(txs) > 0 {
		if d := txs[len(txs)/2].Date; len(d) >= 4 {
			year = d[:4]
		}
	}
	return txs, year, nil
}

// ── FIFO engine ───────────────────────────────────────────────────────────────

var perfCatNames = []string{
	"pv_etf_stock", "mv_etf_stock", "pv_crypto", "mv_crypto",
	"dividendes", "interets", "staking", "cashback",
}

// calcPerfPnL runs FIFO P&L over pre-sorted transactions and returns monthly aggregates.
func calcPerfPnL(txs []TRTransaction) MonthlyData {
	etfClasses    := map[string]bool{"FUND": true, "STOCK": true}
	cryptoClasses := map[string]bool{"CRYPTO": true}

	type lot struct{ shares, unitCost float64 }
	fifo   := map[string][]lot{}
	result := MonthlyData{}

	ensureYM := func(ym string) {
		if _, ok := result[ym]; !ok {
			m := make(map[string]float64, len(perfCatNames))
			for _, c := range perfCatNames {
				m[c] = 0
			}
			result[ym] = m
		}
	}
	fifoAdd := func(sym string, shares, unitCost float64) {
		if shares > 0 {
			fifo[sym] = append(fifo[sym], lot{shares, unitCost})
		}
	}
	fifoConsume := func(sym string, qty float64) float64 {
		q := fifo[sym]
		var cost float64
		for qty > 1e-9 && len(q) > 0 {
			take := math.Min(qty, q[0].shares)
			cost += take * q[0].unitCost
			qty -= take
			q[0].shares -= take
			if q[0].shares < 1e-9 {
				q = q[1:]
			}
		}
		fifo[sym] = q
		return cost
	}

	for _, t := range txs {
		if len(t.Date) < 7 {
			continue
		}
		ym := t.Date[:7]
		ensureYM(ym)

		sym := t.Symbol
		if sym == "" {
			sym = t.Name
		}
		isCrypto := cryptoClasses[t.AssetClass]
		isEtf    := etfClasses[t.AssetClass] ||
			(!isCrypto && t.AssetClass == "" && (t.Type == "BUY" || t.Type == "SELL"))

		switch t.Type {
		case "BUY":
			if isEtf || isCrypto {
				totalCost := math.Abs(t.Amount) + math.Abs(t.Fee)
				var uc float64
				if math.Abs(t.Shares) > 0 {
					uc = totalCost / math.Abs(t.Shares)
				}
				fifoAdd(sym, math.Abs(t.Shares), uc)
			}
		case "SELL":
			if isEtf || isCrypto {
				net  := math.Abs(t.Amount) + t.Fee // Fee is negative in TR exports
				cost := fifoConsume(sym, math.Abs(t.Shares))
				pnl  := net - cost
				cat  := "pv_etf_stock"
				if isCrypto {
					cat = "pv_crypto"
				}
				if pnl < 0 {
					cat = "mv_etf_stock"
					if isCrypto {
						cat = "mv_crypto"
					}
				}
				result[ym][cat] += pnl
			}
		case "DIVIDEND":
			result[ym]["dividendes"] += t.Amount - math.Abs(t.Tax)
		case "INTEREST_PAYMENT":
			result[ym]["interets"] += t.Amount - math.Abs(t.Tax)
		case "FREE_RECEIPT":
			if isCrypto && t.Shares > 0 {
				fifoAdd(sym, t.Shares, t.Price)
				result[ym]["staking"] += t.Shares * t.Price
			}
		case "BENEFITS_SAVEBACK":
			if t.Amount > 0 {
				result[ym]["cashback"] += t.Amount
			}
		case "STOCKPERK":
			if t.Shares > 0 {
				fifoAdd(sym, t.Shares, 0)
			}
		}
	}
	return result
}

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

// OpenPosition represents a currently-held ETF/stock position derived from FIFO.
// Price, Value, PnL, PnLPct and Currency are populated at serve time with live quotes.
type OpenPosition struct {
	Symbol       string  `json:"symbol"`
	Name         string  `json:"name"`
	Shares       float64 `json:"shares"`
	AvgCost      float64 `json:"avgCost"`
	TotalCost    float64 `json:"totalCost"`
	CostCurrency string  `json:"costCurrency,omitempty"` // "" or "EUR" for TR; native currency for Yuh
	Price        float64 `json:"price,omitempty"`
	Value        float64 `json:"value,omitempty"`
	PnL          float64 `json:"pnl,omitempty"`
	PnLPct       float64 `json:"pnlPct,omitempty"`
	Currency     string  `json:"currency,omitempty"`
	HasPrice     bool    `json:"hasPrice"`
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
	etfClasses := map[string]bool{"FUND": true, "STOCK": true}
	cryptoClasses := map[string]bool{"CRYPTO": true}

	type lot struct{ shares, unitCost float64 }
	fifo := map[string][]lot{}
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
		isEtf := etfClasses[t.AssetClass] ||
			(!isCrypto && t.AssetClass == "" && (t.Type == "BUY" || t.Type == "SELL"))

		switch t.Type {
		case "BUY":
			if isEtf || isCrypto {
				totalCost := math.Abs(t.Amount) + math.Abs(t.Fee)
				if totalCost == 0 && t.Price > 0 { // amount missing in some TR exports
					totalCost = math.Abs(t.Shares) * t.Price
				}
				var uc float64
				if math.Abs(t.Shares) > 0 {
					uc = totalCost / math.Abs(t.Shares)
				}
				fifoAdd(sym, math.Abs(t.Shares), uc)
			}
		case "SELL":
			if isEtf || isCrypto {
				net := math.Abs(t.Amount) + t.Fee // Fee is negative in TR exports
				if net == 0 && t.Price > 0 {      // pending transaction (amount not yet settled)
					break
				}
				cost := fifoConsume(sym, math.Abs(t.Shares))
				pnl := net - cost
				cat := "pv_etf_stock"
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

// calcOpenPositions returns currently-held ETF/stock positions (no crypto).
func calcOpenPositions(txs []TRTransaction) []OpenPosition {
	etfClasses := map[string]bool{"FUND": true, "STOCK": true}

	type lot struct{ shares, unitCost float64 }
	fifo := map[string][]lot{}
	names := map[string]string{}

	for _, t := range txs {
		sym := t.Symbol
		if sym == "" {
			sym = t.Name
		}
		isCrypto := t.AssetClass == "CRYPTO"
		isEtf := etfClasses[t.AssetClass] ||
			(!isCrypto && t.AssetClass == "" && (t.Type == "BUY" || t.Type == "SELL"))
		if !isEtf {
			continue
		}
		if t.Name != "" {
			names[sym] = t.Name
		}
		switch t.Type {
		case "BUY":
			totalCost := math.Abs(t.Amount) + math.Abs(t.Fee)
			if totalCost == 0 && t.Price > 0 { // amount missing in some TR exports
				totalCost = math.Abs(t.Shares) * t.Price
			}
			var uc float64
			if math.Abs(t.Shares) > 0 {
				uc = totalCost / math.Abs(t.Shares)
			}
			if math.Abs(t.Shares) > 0 {
				fifo[sym] = append(fifo[sym], lot{math.Abs(t.Shares), uc})
			}
		case "SELL":
			qty := math.Abs(t.Shares)
			q := fifo[sym]
			for qty > 1e-9 && len(q) > 0 {
				take := math.Min(qty, q[0].shares)
				qty -= take
				q[0].shares -= take
				if q[0].shares < 1e-9 {
					q = q[1:]
				}
			}
			fifo[sym] = q
		case "STOCKPERK":
			if t.Shares > 0 {
				fifo[sym] = append(fifo[sym], lot{t.Shares, 0})
			}
		}
	}

	var positions []OpenPosition
	for sym, lots := range fifo {
		var totalShares, totalCost float64
		for _, l := range lots {
			totalShares += l.shares
			totalCost += l.shares * l.unitCost
		}
		if totalShares < 1e-9 {
			continue
		}
		avgCost := 0.0
		if totalShares > 0 {
			avgCost = totalCost / totalShares
		}
		positions = append(positions, OpenPosition{
			Symbol:    sym,
			Name:      names[sym],
			Shares:    totalShares,
			AvgCost:   avgCost,
			TotalCost: totalCost,
		})
	}
	sort.Slice(positions, func(i, j int) bool {
		return positions[i].TotalCost > positions[j].TotalCost
	})
	return positions
}

// ── Yuh ───────────────────────────────────────────────────────────────────────

type YuhTransaction struct {
	Date           string
	ActivityType   string
	Name           string
	Debit          float64
	DebitCurrency  string
	Credit         float64
	CreditCurrency string
	Fee            float64
	Side           string // "BUY" or "SELL"
	Quantity       float64
	Asset          string
	PricePerUnit   float64
}

// yuhBloombergToYahoo converts Bloomberg-style exchange codes embedded in Yuh
// asset tickers (e.g. "CHDVD SW Equity") to Yahoo Finance format ("CHDVD.SW").
var yuhBloombergSuffix = map[string]string{
	"SW": ".SW", // SIX Swiss Exchange
	"LN": ".L",  // London
	"GY": ".DE", // XETRA
	"FP": ".PA", // Euronext Paris
	"NA": ".AS", // Euronext Amsterdam
	"SM": ".MC", // Madrid
	"IM": ".MI", // Milan
	"SS": ".ST", // Stockholm
	"DC": ".CO", // Copenhagen
	"OS": ".OL", // Oslo
}

func cleanYuhTicker(asset string) string {
	// "CHDVD SW Equity" → "CHDVD.SW"; plain tickers pass through unchanged.
	parts := strings.Fields(asset)
	if len(parts) == 0 {
		return ""
	}
	if len(parts) >= 2 {
		if suffix, ok := yuhBloombergSuffix[parts[len(parts)-2]]; ok {
			return parts[0] + suffix
		}
	}
	return parts[0]
}

// parseYuhCSV parses a Yuh ACTIVITIES_REPORT CSV export (semicolon-delimited, BOM).
func parseYuhCSV(content string) ([]YuhTransaction, error) {
	// Strip UTF-8 BOM if present.
	content = strings.TrimPrefix(content, "\xef\xbb\xbf")

	r := csv.NewReader(strings.NewReader(content))
	r.Comma = ';'
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return nil, nil
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
		s = strings.ReplaceAll(s, ",", ".")
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}
	// Convert DD/MM/YYYY → YYYY-MM-DD.
	parseDate := func(s string) string {
		if len(s) == 10 && s[2] == '/' && s[5] == '/' {
			return s[6:10] + "-" + s[3:5] + "-" + s[0:2]
		}
		return s
	}

	var txs []YuhTransaction
	for _, row := range records[1:] {
		actType := get(row, "activity type")
		if actType != "INVEST_ORDER_EXECUTED" &&
			actType != "CASH_TRANSACTION_RELATED_OTHER" &&
			actType != "CASH_TRANSACTION_OTHER" {
			continue
		}
		side := strings.ToUpper(get(row, "buy/sell"))
		asset := strings.Trim(get(row, "asset"), `"`)
		// For invest orders, skip rows with no asset or SWQ (Yuh loyalty points).
		if actType == "INVEST_ORDER_EXECUTED" && (asset == "" || asset == "SWQ") {
			continue
		}
		date := parseDate(get(row, "date"))
		if date == "" {
			continue
		}
		txs = append(txs, YuhTransaction{
			Date:           date,
			ActivityType:   actType,
			Name:           strings.Trim(get(row, "activity name"), `"`),
			Debit:          math.Abs(pf(get(row, "debit"))),
			DebitCurrency:  get(row, "debit currency"),
			Credit:         pf(get(row, "credit")),
			CreditCurrency: get(row, "credit currency"),
			Fee:            math.Abs(pf(get(row, "fees/commission"))),
			Side:           side,
			Quantity:       math.Abs(pf(get(row, "quantity"))),
			Asset:          cleanYuhTicker(asset),
			PricePerUnit:   pf(get(row, "price per unit")),
		})
	}
	sort.Slice(txs, func(i, j int) bool { return txs[i].Date < txs[j].Date })
	return txs, nil
}

// YuhYearRecord holds activity for one calendar year, all in EUR.
type YuhYearRecord struct {
	Year        string  `json:"year"`
	Invested    float64 `json:"invested"`    // total cost of BUYs
	RealizedPnL float64 `json:"realizedPnL"` // gains/losses from SELLs
	Dividends   float64 `json:"dividends"`   // CASH_TRANSACTION_RELATED_OTHER (dividends, capital gain distributions)
	Interest    float64 `json:"interest"`    // CASH_TRANSACTION_OTHER (cash deposit interest)
	Total       float64 `json:"total"`       // RealizedPnL + Dividends + Interest
}

// calcYuhData runs a single FIFO pass over all transactions and returns:
//   - open positions (costs in EUR via annual avg FX rates)
//   - per-year realized P&L and dividends (also in EUR)
//
// fxByYearCur maps "CHF:2024" → average EURCHF rate for 2024.
func calcYuhData(txs []YuhTransaction, fxByYearCur map[string]float64) ([]OpenPosition, []YuhYearRecord) {
	type lot struct{ shares, unitCostEUR float64 }
	fifo := map[string][]lot{}
	names := map[string]string{}

	investedByYear := map[string]float64{}
	realizedByYear := map[string]float64{}
	dividendsByYear := map[string]float64{}
	interestByYear := map[string]float64{}

	toEUR := func(amount float64, cur, year string) float64 {
		if cur == "" || cur == "EUR" {
			return amount
		}
		if rate, ok := fxByYearCur[cur+":"+year]; ok && rate > 0 {
			return amount / rate
		}
		return amount
	}

	for _, t := range txs {
		year := ""
		if len(t.Date) >= 4 {
			year = t.Date[:4]
		}

		switch t.ActivityType {
		case "INVEST_ORDER_EXECUTED":
			sym := t.Asset
			if t.Name != "" {
				names[sym] = t.Name
			}
			switch t.Side {
			case "BUY":
				totalCost := t.Debit // abs; Yuh gross debit = price×qty + fee
				if totalCost == 0 && t.PricePerUnit > 0 {
					totalCost = t.Quantity*t.PricePerUnit + t.Fee
				}
				costEUR := toEUR(totalCost, t.DebitCurrency, year)
				uc := 0.0
				if t.Quantity > 0 {
					uc = costEUR / t.Quantity
				}
				fifo[sym] = append(fifo[sym], lot{t.Quantity, uc})
				investedByYear[year] += costEUR
			case "SELL":
				// Proceeds are net of fees (Yuh credits net amount).
				proceedsEUR := toEUR(t.Credit, t.CreditCurrency, year)
				qty := t.Quantity
				var costBasis float64
				q := fifo[sym]
				for qty > 1e-9 && len(q) > 0 {
					take := math.Min(qty, q[0].shares)
					costBasis += take * q[0].unitCostEUR
					qty -= take
					q[0].shares -= take
					if q[0].shares < 1e-9 {
						q = q[1:]
					}
				}
				fifo[sym] = q
				realizedByYear[year] += proceedsEUR - costBasis
			}
		case "CASH_TRANSACTION_RELATED_OTHER":
			if t.Credit > 0 {
				dividendsByYear[year] += toEUR(t.Credit, t.CreditCurrency, year)
			}
		case "CASH_TRANSACTION_OTHER":
			if t.Credit > 0 {
				interestByYear[year] += toEUR(t.Credit, t.CreditCurrency, year)
			}
		}
	}

	// Build open positions.
	var positions []OpenPosition
	for sym, lots := range fifo {
		var totalShares, totalCostEUR float64
		for _, l := range lots {
			totalShares += l.shares
			totalCostEUR += l.shares * l.unitCostEUR
		}
		if totalShares < 1e-9 {
			continue
		}
		avgCost := 0.0
		if totalShares > 0 {
			avgCost = totalCostEUR / totalShares
		}
		positions = append(positions, OpenPosition{
			Symbol:    sym,
			Name:      names[sym],
			Shares:    totalShares,
			AvgCost:   avgCost,
			TotalCost: totalCostEUR,
		})
	}
	sort.Slice(positions, func(i, j int) bool {
		return positions[i].TotalCost > positions[j].TotalCost
	})

	// Build year records — union of keys from all maps.
	allYears := map[string]struct{}{}
	for y := range investedByYear {
		allYears[y] = struct{}{}
	}
	for y := range realizedByYear {
		allYears[y] = struct{}{}
	}
	for y := range dividendsByYear {
		allYears[y] = struct{}{}
	}
	for y := range interestByYear {
		allYears[y] = struct{}{}
	}
	var yearRecords []YuhYearRecord
	for y := range allYears {
		pnl := realizedByYear[y]
		div := dividendsByYear[y]
		interest := interestByYear[y]
		yearRecords = append(yearRecords, YuhYearRecord{
			Year: y, Invested: investedByYear[y],
			RealizedPnL: pnl, Dividends: div, Interest: interest,
			Total: pnl + div + interest,
		})
	}
	sort.Slice(yearRecords, func(i, j int) bool {
		return yearRecords[i].Year < yearRecords[j].Year
	})
	return positions, yearRecords
}

// ── Vinted ───────────────────────────────────────────────────────────────────

type VintedTransaction struct {
	Date          string  `json:"date"`
	TransactionID string  `json:"transactionId,omitempty"`
	Type          string  `json:"type"`
	Item          string  `json:"item"`
	Amount        float64 `json:"amount"`
	GrossAmount   float64 `json:"grossAmount"`
	Currency      string  `json:"currency"`
	Status        string  `json:"status,omitempty"`
	Description   string  `json:"description,omitempty"`
	Realized      bool    `json:"realized"`
	SourceOrder   int     `json:"sourceOrder,omitempty"`
	SourceFile    string  `json:"sourceFile,omitempty"`
}

type VintedSummary struct {
	Sales     float64 `json:"sales"`
	Purchases float64 `json:"purchases"`
	Net       float64 `json:"net"`
	Count     int     `json:"count"`
}

func parseVintedCSV(content string) ([]VintedTransaction, error) {
	content = strings.TrimPrefix(content, "\xef\xbb\xbf")
	firstLine := content
	if idx := strings.IndexAny(content, "\r\n"); idx >= 0 {
		firstLine = content[:idx]
	}

	r := csv.NewReader(strings.NewReader(content))
	if strings.Count(firstLine, ";") > strings.Count(firstLine, ",") {
		r.Comma = ';'
	}
	r.LazyQuotes = true
	r.TrimLeadingSpace = true

	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return nil, nil
	}

	headers := make(map[string]int, len(records[0]))
	for i, h := range records[0] {
		headers[normalizeCSVHeader(h)] = i
	}

	get := func(row []string, names ...string) string {
		for _, name := range names {
			if idx, ok := headers[normalizeCSVHeader(name)]; ok && idx < len(row) {
				return strings.TrimSpace(strings.Trim(row[idx], `"`))
			}
		}
		return ""
	}

	var txs []VintedTransaction
	for _, row := range records[1:] {
		date := normalizeVintedDate(get(row, "date", "transaction date", "created at", "paid at"))
		txID := get(row, "transaction id", "id")
		txType := get(row, "type", "transaction type", "operation")
		item := get(row, "item", "item title", "title", "name", "article", "product")
		status := get(row, "status")
		description := get(row, "source status", "description", "details", "comment")
		currency := get(row, "currency", "devise")
		grossAmount := parseMoney(get(row, "amount", "amount eur", "total", "price", "value"))
		cashFlowText := get(row, "cash flow eur", "cash flow")
		amountText := cashFlowText
		if amountText == "" {
			amountText = get(row, "amount", "amount eur", "total", "price", "net amount", "net", "value")
		}
		amount := parseMoney(amountText)
		if cashFlowText == "" && strings.Contains(strings.ToLower(txType), "purchase") && amount > 0 {
			amount = -amount
		}
		if currency == "" {
			currency = detectCurrency(amountText)
		}
		realized := parseBoolDefault(get(row, "included in realized cashflow"), true)
		sourceOrder, _ := strconv.Atoi(get(row, "source order"))

		if date == "" && txID == "" && txType == "" && item == "" && amount == 0 && description == "" {
			continue
		}
		txs = append(txs, VintedTransaction{
			Date:          date,
			TransactionID: txID,
			Type:          txType,
			Item:          item,
			Amount:        amount,
			GrossAmount:   grossAmount,
			Currency:      currency,
			Status:        status,
			Description:   description,
			Realized:      realized,
			SourceOrder:   sourceOrder,
		})
	}

	sort.SliceStable(txs, func(i, j int) bool {
		if txs[i].Date == txs[j].Date {
			if txs[i].Type == txs[j].Type && txs[i].SourceOrder != txs[j].SourceOrder {
				return txs[i].SourceOrder < txs[j].SourceOrder
			}
			return txs[i].Item < txs[j].Item
		}
		return txs[i].Date > txs[j].Date
	})
	return txs, nil
}

func summarizeVinted(txs []VintedTransaction) VintedSummary {
	var s VintedSummary
	s.Count = len(txs)
	for _, tx := range txs {
		amount := vintedSummaryAmount(tx)
		if math.Abs(amount) < 0.005 {
			continue
		}
		kind := strings.ToLower(tx.Type + " " + tx.Description)
		if amount > 0 || strings.Contains(kind, "sale") || strings.Contains(kind, "sold") || strings.Contains(kind, "vente") || strings.Contains(kind, "vendu") {
			s.Sales += math.Abs(amount)
		} else if amount < 0 || strings.Contains(kind, "purchase") || strings.Contains(kind, "buy") || strings.Contains(kind, "achat") {
			s.Purchases += math.Abs(amount)
		}
		s.Net += amount
	}
	return s
}

func vintedSummaryAmount(tx VintedTransaction) float64 {
	if !tx.Realized && math.Abs(tx.Amount) < 0.005 {
		return 0
	}
	return tx.Amount
}

func normalizeCSVHeader(s string) string {
	s = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(s, "\xef\xbb\xbf")))
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	return strings.Join(strings.Fields(s), " ")
}

func normalizeVintedDate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 10 && s[2] == '/' && s[5] == '/' {
		return s[6:10] + "-" + s[3:5] + "-" + s[0:2]
	}
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

func parseMoney(s string) float64 {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\u00a0", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "€", "")
	s = strings.ReplaceAll(s, "CHF", "")
	s = strings.ReplaceAll(s, "EUR", "")
	s = strings.ReplaceAll(s, "'", "")
	s = strings.ReplaceAll(s, ",", ".")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseBoolDefault(s string, def bool) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "true", "1", "yes", "y", "oui":
		return true
	case "false", "0", "no", "n", "non":
		return false
	default:
		return def
	}
}

func detectCurrency(s string) string {
	up := strings.ToUpper(s)
	switch {
	case strings.Contains(up, "CHF"):
		return "CHF"
	case strings.Contains(up, "EUR"), strings.Contains(up, "€"):
		return "EUR"
	default:
		return ""
	}
}

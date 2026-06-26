// Package report generates HTML reports from stock analysis results.
package report

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"sort"
	"strings"
	"time"

	"stock-portfolio/internal/ai"
	"stock-portfolio/internal/models"
)

//go:embed templates/*.html
var templateFS embed.FS

// Generator creates HTML reports from stock analysis results.
type Generator struct {
	templates         *template.Template
	categoryEmojis    map[string]string
	categoryOrder     map[string]int
	categoryNarrative map[string]string
	categoryNarrScore map[string]int
}

// VIXData holds the VIX index data for display at the top of the report.
type VIXData struct {
	Price         string
	ChangePercent float64
	Change        string
	ChangeClass   string
	Level         string // "low", "moderate", "high", "extreme"
	LevelClass    string
}

// TemplateData contains the data passed to the HTML template.
type TemplateData struct {
	Title            string
	GeneratedAt      string
	CategoryGroups   []CategoryGroupData
	TotalStocks      int
	OversoldCount    int
	OverboughtCount  int
	AIAnalysis       *AIAnalysisData
	ManualPrompt     string
	VIX              *VIXData
	EarningsCalendar []EarningsEventData
	EconomicEvents   []EconomicEventData
}

// EconomicEventData represents a macro economic event for the template.
type EconomicEventData struct {
	Name string
	Date string
}

// EarningsEventData represents a single upcoming earnings event.
type EarningsEventData struct {
	Ticker    string
	Name      string
	Date      string
	DaysAway  int
	DaysLabel string
	Urgency   string // "urgent" (<7d), "upcoming" (7-30d), "future" (>30d)
}

// CategoryGroupData represents a category with its stocks for the template.
type CategoryGroupData struct {
	Name           string
	Emoji          string
	Stocks         []StockRowData
	Narrative      string
	NarrativeScore int
	NarrScoreClass string // color class based on score
}

// StockRowData represents a single stock row for the template.
type StockRowData struct {
	Name          string
	Ticker        string
	Price         string
	Currency      string
	Change        string
	ChangePercent float64
	ChangeClass   string
	ChangeIcon    string
	RSI           string
	RSIValue      float64
	RSIClass      string
	RSILabel      string
	Score         int
	ScoreStr      string
	Signal        string
	SignalClass   string
	SignalNote    string
	TargetPrice   string
	TargetPctHTML template.HTML
	PEGRatio      string
	ValuationHTML template.HTML
	Earnings      string
	EarningsClass string
	PSGRatio      string
	PSGLabel      string
	PSGClass      string
	EVGPRatio     string
	EVGPLabel     string
	EVGPClass     string
}

// AIAnalysisData contains AI analysis data for the template.
type AIAnalysisData struct {
	TopStocks       []TopStockData
	NewsContext     []NewsItemData
	Recommendations []RecommendationData
	MarketSummary   string
}

// TopStockData represents an AI-highlighted stock.
type TopStockData struct {
	Ticker      string
	Name        string
	Reasoning   string
	Signal      string
	SignalClass string
	SignalIcon  string
}

// NewsItemData represents a market news item.
type NewsItemData struct {
	Headline    string
	Impact      string
	ImpactClass string
	AffectedBy  string
	Description string
}

// RecommendationData represents an actionable recommendation.
type RecommendationData struct {
	Ticker      string
	Name        string
	Action      string
	ActionClass string
	ActionIcon  string
	Reason      string
	Risk        string
	RiskClass   string
}

// NewGenerator creates a new report generator.
func NewGenerator(categoryEmojis map[string]string, categoryOrder map[string]int, categoryNarrative map[string]string, categoryNarrScore map[string]int) (*Generator, error) {
	funcMap := template.FuncMap{
		"formatPrice": func(price float64) string {
			return fmt.Sprintf("%.2f", price)
		},
		"formatChange": func(change float64) string {
			return fmt.Sprintf("%+.2f%%", change)
		},
		"formatRSI": func(rsi float64) string {
			return fmt.Sprintf("%.1f", rsi)
		},
		"getChangeClass": func(change float64) string {
			if change > 0.01 {
				return "positive"
			} else if change < -0.01 {
				return "negative"
			}
			return "neutral"
		},
		"getChangeIcon": func(change float64) string {
			if change > 0.01 {
				return "↑"
			} else if change < -0.01 {
				return "↓"
			}
			return "→"
		},
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}

	return &Generator{
		templates:         tmpl,
		categoryEmojis:    categoryEmojis,
		categoryOrder:     categoryOrder,
		categoryNarrative: categoryNarrative,
		categoryNarrScore: categoryNarrScore,
	}, nil
}

// NewVIXData builds a VIXData from raw price and change values.
func NewVIXData(price, changePercent float64) *VIXData {
	changeClass := "neutral"
	if changePercent > 0.01 {
		changeClass = "negative" // rising VIX = bad
	} else if changePercent < -0.01 {
		changeClass = "positive" // falling VIX = good
	}

	level, levelClass := "Normal", "vix-moderate"
	switch {
	case price >= 30:
		level, levelClass = "Stress", "vix-extreme"
	case price >= 15:
		level, levelClass = "Normal", "vix-moderate"
	default:
		level, levelClass = "Calm", "vix-low"
	}

	return &VIXData{
		Price:         fmt.Sprintf("%.2f", price),
		ChangePercent: changePercent,
		Change:        fmt.Sprintf("%+.2f%%", changePercent),
		ChangeClass:   changeClass,
		Level:         level,
		LevelClass:    levelClass,
	}
}

// Generate creates an HTML report from the analysis results.
func (g *Generator) Generate(results []*models.StockResult) (string, error) {
	return g.GenerateWithAI(results, nil, "", nil, nil)
}

// GenerateWithAI creates an HTML report with optional AI analysis or manual prompt.
func (g *Generator) GenerateWithAI(results []*models.StockResult, aiAnalysis *ai.Analysis, manualPrompt string, vix *VIXData, economicEvents []EconomicEventData) (string, error) {
	data := g.prepareTemplateData(results)

	if aiAnalysis != nil {
		data.AIAnalysis = g.convertAIAnalysis(aiAnalysis)
	}
	if manualPrompt != "" {
		data.ManualPrompt = manualPrompt
	}

	data.VIX = vix
	data.EconomicEvents = economicEvents

	var buf bytes.Buffer
	if err := g.templates.ExecuteTemplate(&buf, "report.html", data); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return buf.String(), nil
}

// prepareTemplateData transforms analysis results into template-ready data.
func (g *Generator) prepareTemplateData(results []*models.StockResult) TemplateData {
	// Sort results by category order, then by name
	sort.Slice(results, func(i, j int) bool {
		orderI := g.categoryOrder[results[i].Stock.Category]
		orderJ := g.categoryOrder[results[j].Stock.Category]
		if orderI != orderJ {
			return orderI < orderJ
		}
		return results[i].Stock.Name < results[j].Stock.Name
	})

	// Earnings calendar uses ALL stocks regardless of portfolio flag.
	earningsCalendar := g.buildEarningsCalendar(results)

	// Group by category — only portfolio stocks.
	categoryMap := make(map[string][]StockRowData)
	categoryOrderList := make([]string, 0)
	oversoldCount := 0
	overboughtCount := 0
	portfolioCount := 0

	for _, result := range results {
		if !result.Stock.IsInPortfolio() {
			continue
		}
		if _, exists := categoryMap[result.Stock.Category]; !exists {
			categoryOrderList = append(categoryOrderList, result.Stock.Category)
		}

		row := g.createStockRow(result)
		categoryMap[result.Stock.Category] = append(categoryMap[result.Stock.Category], row)
		portfolioCount++

		if result.IsOversold() {
			oversoldCount++
		}
		if result.IsOverbought() {
			overboughtCount++
		}
	}

	// Sort category list by order
	sort.Slice(categoryOrderList, func(i, j int) bool {
		return g.categoryOrder[categoryOrderList[i]] < g.categoryOrder[categoryOrderList[j]]
	})

	// Build category groups
	groups := make([]CategoryGroupData, 0, len(categoryOrderList))
	for _, catName := range categoryOrderList {
		narrScore := g.categoryNarrScore[catName]
		narrScoreClass := narrativeScoreClass(narrScore)
		groups = append(groups, CategoryGroupData{
			Name:           catName,
			Emoji:          g.getCategoryEmoji(catName),
			Stocks:         categoryMap[catName],
			Narrative:      g.categoryNarrative[catName],
			NarrativeScore: narrScore,
			NarrScoreClass: narrScoreClass,
		})
	}

	return TemplateData{
		Title:            "Stock Market Report",
		GeneratedAt:      time.Now().Format("Monday, January 2, 2006"),
		CategoryGroups:   groups,
		TotalStocks:      portfolioCount,
		OversoldCount:    oversoldCount,
		OverboughtCount:  overboughtCount,
		EarningsCalendar: earningsCalendar,
	}
}

// ExtractSignals returns a map of ticker → [signal, signalNote] for all results.
func (g *Generator) ExtractSignals(results []*models.StockResult) map[string][2]string {
	out := make(map[string][2]string, len(results))
	for _, r := range results {
		_, signal, _, note := g.computeSignal(r)
		if signal != "" {
			out[r.Stock.Ticker] = [2]string{signal, note}
		}
	}
	return out
}

// computeSignal computes a composite score and trading signal from all available indicators.
func (g *Generator) computeSignal(result *models.StockResult) (score int, signal, signalClass, signalNote string) {
	// Require at least 3 of the 4 fundamental indicators to produce a signal.
	available := 0
	if result.PEGRatio > 0 {
		available++
	}
	if result.PSGRatio > 0 {
		available++
	}
	if result.EVGrossProfit > 0 {
		available++
	}
	if result.TargetPrice > 0 {
		available++
	}
	if available < 3 {
		return 0, "", "", ""
	}

	rsi := result.RSI

	// 1. RSI — timing / chase risk
	switch {
	case rsi < 35:
		score++
	case rsi < 55:
		score++
	case rsi < 65:
		// 0
	case rsi < 75:
		score--
	case rsi < 85:
		score -= 2
	default:
		score -= 3
	}

	// 2. PEG — valuation vs EPS growth
	if result.PEGRatio > 0 {
		switch {
		case result.PEGRatio < 1.0:
			score += 2
		case result.PEGRatio < 1.7:
			score++
		case result.PEGRatio < 2.5:
			// 0
		case result.PEGRatio < 4.0:
			score--
		default:
			score -= 2
		}
	}

	// 3. PSG — valuation vs revenue growth
	if result.PSGRatio > 0 {
		switch {
		case result.PSGRatio < 0.15:
			score += 2
		case result.PSGRatio < 0.30:
			score++
		case result.PSGRatio < 0.45:
			// 0
		case result.PSGRatio < 0.65:
			score--
		default:
			score -= 2
		}
	}

	// 4. EV/GP — quality of growth
	if result.EVGrossProfit > 0 {
		switch {
		case result.EVGrossProfit < 8:
			score += 2
		case result.EVGrossProfit < 15:
			score++
		case result.EVGrossProfit < 25:
			// 0
		case result.EVGrossProfit < 35:
			score--
		default:
			score -= 2
		}
	}

	// 5. Analyst target upside
	var upside float64
	if result.TargetPrice > 0 && result.CurrentPrice > 0 {
		upside = (result.TargetPrice - result.CurrentPrice) / result.CurrentPrice * 100
		switch {
		case upside > 40:
			score += 2
		case upside > 20:
			score++
		case upside > 5:
			// 0
		case upside > -10:
			score--
		default:
			score -= 2
		}
	}

	// 6. Earnings risk
	daysToEarnings := -1
	if result.NextEarningsDate != nil {
		now := time.Now()
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		d := *result.NextEarningsDate
		day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, now.Location())
		daysToEarnings = int(day.Sub(today).Hours() / 24)
		switch {
		case daysToEarnings <= 3:
			score -= 2
		case daysToEarnings <= 10:
			score--
		}
	}

	// Override rules
	// RSI > 85 + earnings < 7 days → Trim minimum
	if rsi > 85 && daysToEarnings >= 0 && daysToEarnings < 7 && score > -4 {
		score = -4
	}
	// RSI > 75 + PSG > 0.65 + EV/GP > 25 → Light Trim minimum
	if rsi > 75 && result.PSGRatio > 0.65 && result.EVGrossProfit > 25 && score > -2 {
		score = -2
	}
	// Negative upside + expensive PEG + expensive PSG → Sell minimum
	if result.TargetPrice > 0 && upside < 0 && result.PEGRatio > 2.5 && result.PSGRatio > 0.45 && score > -6 {
		score = -6
	}
	// RSI < 55 + attractive PSG + attractive EV/GP + upside > 20% → Buy minimum
	if rsi < 55 && result.PSGRatio > 0 && result.PSGRatio < 0.15 &&
		result.EVGrossProfit > 0 && result.EVGrossProfit < 8 &&
		result.TargetPrice > 0 && upside > 20 && score < 3 {
		score = 3
	}
	// RSI > 75 → no Strong Buy
	if rsi > 75 && score > 5 {
		score = 5
	}
	// Earnings < 7 days → no Strong Buy
	if daysToEarnings >= 0 && daysToEarnings < 7 && score > 5 {
		score = 5
	}
	// EV/GP > 50x + earnings < 7 days → max HOLD (speculative/unprofitable pre-earnings)
	if result.EVGrossProfit > 50 && daysToEarnings >= 0 && daysToEarnings < 7 && score > 0 {
		score = 0
	}

	// Map score to signal
	switch {
	case score >= 6:
		signal, signalClass = "STRONG BUY", "signal-strong-buy"
	case score >= 3:
		signal, signalClass = "BUY", "signal-buy"
	case score >= 1:
		signal, signalClass = "ACCUMULATE", "signal-accumulate"
	case score >= -1:
		signal, signalClass = "HOLD", "signal-hold"
	case score >= -3:
		signal, signalClass = "LIGHT TRIM", "signal-light-trim"
	case score >= -5:
		signal, signalClass = "TRIM", "signal-trim"
	case score >= -7:
		signal, signalClass = "SELL", "signal-sell"
	default:
		signal, signalClass = "STRONG SELL", "signal-strong-sell"
	}

	if score >= 1 {
		cautionDays := 7
		if result.EVGrossProfit > 25 {
			cautionDays = 21
		}
		if daysToEarnings >= 0 && daysToEarnings < cautionDays {
			signalNote = "WAIT EARNINGS"
		}
	}

	return
}

// createStockRow creates a template-ready stock row.
func (g *Generator) createStockRow(result *models.StockResult) StockRowData {
	changeClass := "neutral"
	changeIcon := "→"
	if result.ChangePercent > 0.01 {
		changeClass = "positive"
		changeIcon = "↑"
	} else if result.ChangePercent < -0.01 {
		changeClass = "negative"
		changeIcon = "↓"
	}

	var rsiClass, rsiLabel string
	switch {
	case result.RSI < 30:
		rsiClass = "rsi-strong-oversold"
		rsiLabel = "STRONG OVERSOLD"
	case result.RSI < 40:
		rsiClass = "rsi-accumulation"
		rsiLabel = "ACCUMULATION"
	case result.RSI < 55:
		rsiClass = "rsi-neutral"
		rsiLabel = "NEUTRAL"
	case result.RSI < 65:
		rsiClass = "rsi-momentum"
		rsiLabel = "MOMENTUM"
	case result.RSI < 75:
		rsiClass = "rsi-extended"
		rsiLabel = "EXTENDED"
	case result.RSI < 85:
		rsiClass = "rsi-overbought"
		rsiLabel = "OVERBOUGHT"
	default:
		rsiClass = "rsi-very-overbought"
		rsiLabel = "VERY OVERBOUGHT"
	}

	// Target price with currency
	var targetPrice, targetPctHTML string
	if result.TargetPrice > 0 && result.CurrentPrice > 0 {
		pct := (result.TargetPrice - result.CurrentPrice) / result.CurrentPrice * 100
		currency := result.Currency
		targetPrice = fmt.Sprintf("%.0f %s", result.TargetPrice, currency)
		sign := "+"
		cls := "target-up"
		if pct < 0 {
			sign = ""
			cls = "target-down"
		}
		targetPctHTML = fmt.Sprintf(`<span class="%s">%s%.1f%%</span>`, cls, sign, pct)
	}

	// PEG ratio + valuation label
	var pegStr, valuationHTML string
	if result.PEGRatio > 0 {
		pegStr = fmt.Sprintf("%.2f", result.PEGRatio)
		switch {
		case result.PEGRatio < 1:
			valuationHTML = `<span class="val-under">UNDERVALUED</span>`
		case result.PEGRatio < 1.5:
			valuationHTML = `<span class="val-reasonable">REASONABLE</span>`
		case result.PEGRatio < 2.2:
			valuationHTML = `<span class="val-fair">FAIR</span>`
		case result.PEGRatio < 3:
			valuationHTML = `<span class="val-expensive">EXPENSIVE</span>`
		default:
			valuationHTML = `<span class="val-over">OVERVALUED</span>`
		}
	}

	// EV / Gross Profit
	var evgpStr, evgpLabel, evgpClass string
	if result.EVGrossProfit > 0 {
		evgpStr = fmt.Sprintf("%.1fx", result.EVGrossProfit)
		switch {
		case result.EVGrossProfit < 8:
			evgpClass = "evgp-attractive"
			evgpLabel = "ATTRACTIVE"
		case result.EVGrossProfit < 15:
			evgpClass = "evgp-reasonable"
			evgpLabel = "REASONABLE"
		case result.EVGrossProfit < 25:
			evgpClass = "evgp-expensive"
			evgpLabel = "EXPENSIVE"
		default:
			evgpClass = "evgp-very-expensive"
			evgpLabel = "VERY EXPENSIVE"
		}
	}

	// PSG ratio
	var psgStr, psgClass, psgLabel string
	if result.PSGRatio > 0 {
		psgStr = fmt.Sprintf("%.2f", result.PSGRatio)
		switch {
		case result.PSGRatio < 0.15:
			psgClass = "psg-very-attractive"
			psgLabel = "ATTRACTIVE"
		case result.PSGRatio < 0.30:
			psgClass = "psg-reasonable"
			psgLabel = "REASONABLE"
		case result.PSGRatio < 0.50:
			psgClass = "psg-expensive"
			psgLabel = "EXPENSIVE"
		default:
			psgClass = "psg-very-expensive"
			psgLabel = "PREMIUM"
		}
	}

	// Next earnings date
	var earnings, earningsClass string
	if result.NextEarningsDate != nil {
		now := time.Now()
		paris, _ := time.LoadLocation("Europe/Paris")
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		d := *result.NextEarningsDate
		dParis := d.In(paris)
		day := time.Date(dParis.Year(), dParis.Month(), dParis.Day(), 0, 0, 0, 0, now.Location())
		daysAway := int(day.Sub(today).Hours() / 24)
		earnings = dParis.Format("Jan 2")
		if dParis.Hour() != 0 {
			earnings += dParis.Format(" · 15h04")
		}
		switch {
		case daysAway <= 7:
			earningsClass = "earnings-soon"
		case daysAway <= 30:
			earningsClass = "earnings-upcoming"
		default:
			earningsClass = "earnings-later"
		}
	}

	score, signal, signalClass, signalNote := g.computeSignal(result)
	scoreStr := fmt.Sprintf("%+d", score)

	return StockRowData{
		Name:          result.Stock.Name,
		Ticker:        result.Stock.Ticker,
		Price:         fmt.Sprintf("%.2f", result.CurrentPrice),
		Currency:      result.Currency,
		Change:        fmt.Sprintf("%+.2f%%", result.ChangePercent),
		ChangePercent: result.ChangePercent,
		ChangeClass:   changeClass,
		ChangeIcon:    changeIcon,
		RSI:           fmt.Sprintf("%.1f", result.RSI),
		RSIValue:      result.RSI,
		RSIClass:      rsiClass,
		RSILabel:      rsiLabel,
		TargetPrice:   targetPrice,
		TargetPctHTML: template.HTML(targetPctHTML),
		PEGRatio:      pegStr,
		ValuationHTML: template.HTML(valuationHTML),
		Earnings:      earnings,
		EarningsClass: earningsClass,
		PSGRatio:      psgStr,
		PSGLabel:      psgLabel,
		PSGClass:      psgClass,
		EVGPRatio:     evgpStr,
		EVGPLabel:     evgpLabel,
		EVGPClass:     evgpClass,
		Score:         score,
		ScoreStr:      scoreStr,
		Signal:        signal,
		SignalClass:   signalClass,
		SignalNote:    signalNote,
	}
}

// buildEarningsCalendar builds a sorted list of upcoming earnings events.
func (g *Generator) buildEarningsCalendar(results []*models.StockResult) []EarningsEventData {
	now := time.Now()
	paris, _ := time.LoadLocation("Europe/Paris")
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var events []EarningsEventData

	for _, r := range results {
		if r.NextEarningsDate == nil {
			continue
		}
		t := *r.NextEarningsDate
		tParis := t.In(paris)
		eventDay := time.Date(tParis.Year(), tParis.Month(), tParis.Day(), 0, 0, 0, 0, now.Location())
		daysAway := int(eventDay.Sub(today).Hours() / 24)
		if daysAway < 0 {
			continue
		}

		daysLabel := fmt.Sprintf("dans %d j", daysAway)
		if daysAway == 0 {
			daysLabel = "Aujourd'hui"
		} else if daysAway == 1 {
			daysLabel = "Demain"
		}

		urgency := "future"
		if daysAway < 7 {
			urgency = "urgent"
		} else if daysAway <= 30 {
			urgency = "upcoming"
		}

		dateStr := tParis.Format("02 Jan 2006")
		if tParis.Hour() != 0 {
			dateStr += tParis.Format(" · 15h04")
		}

		events = append(events, EarningsEventData{
			Ticker:    r.Stock.Ticker,
			Name:      r.Stock.Name,
			Date:      dateStr,
			DaysAway:  daysAway,
			DaysLabel: daysLabel,
			Urgency:   urgency,
		})
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].DaysAway < events[j].DaysAway
	})

	if len(events) > 10 {
		events = events[:10]
	}

	return events
}

// narrativeScoreClass returns a CSS class based on the narrative score (1-10).
func narrativeScoreClass(score int) string {
	if score == 0 {
		return ""
	}
	switch {
	case score >= 8:
		return "narr-score-high"
	case score >= 6:
		return "narr-score-medium"
	case score >= 4:
		return "narr-score-low"
	default:
		return "narr-score-negative"
	}
}

// getCategoryEmoji returns the emoji for a category.
func (g *Generator) getCategoryEmoji(category string) string {
	emojiMap := map[string]string{
		"1st_place_medal": "🥇",
		"coin":            "🪙",
		"zap":             "⚡",
		"us":              "🇺🇸",
		"shield":          "🛡️",
		"fr":              "🇫🇷",
		"earth_americas":  "🌍",
		"gold":            "🥇",
		"bitcoin":         "₿",
		"globe":           "🌍",
	}

	if emojiName, ok := g.categoryEmojis[category]; ok {
		if emoji, exists := emojiMap[emojiName]; exists {
			return emoji
		}
		return emojiName
	}

	return "📊"
}

// convertAIAnalysis converts AI analysis to template-ready data.
func (g *Generator) convertAIAnalysis(analysis *ai.Analysis) *AIAnalysisData {
	data := &AIAnalysisData{
		MarketSummary: analysis.MarketSummary,
	}

	// Convert top stocks
	for _, ts := range analysis.TopStocks {
		signalClass := "neutral"
		signalIcon := "→"
		switch ts.Signal {
		case "bullish":
			signalClass = "positive"
			signalIcon = "↑"
		case "bearish":
			signalClass = "negative"
			signalIcon = "↓"
		}
		data.TopStocks = append(data.TopStocks, TopStockData{
			Ticker:      ts.Ticker,
			Name:        ts.Name,
			Reasoning:   ts.Reasoning,
			Signal:      ts.Signal,
			SignalClass: signalClass,
			SignalIcon:  signalIcon,
		})
	}

	// Convert news context
	for _, news := range analysis.NewsContext {
		impactClass := "neutral"
		switch news.Impact {
		case "positive":
			impactClass = "positive"
		case "negative":
			impactClass = "negative"
		}
		affectedBy := ""
		if len(news.AffectedBy) > 0 {
			affectedBy = strings.Join(news.AffectedBy, ", ")
		}
		data.NewsContext = append(data.NewsContext, NewsItemData{
			Headline:    news.Headline,
			Impact:      news.Impact,
			ImpactClass: impactClass,
			AffectedBy:  affectedBy,
			Description: news.Description,
		})
	}

	// Convert recommendations
	for _, rec := range analysis.Recommendations {
		actionClass := "neutral"
		actionIcon := "●"
		switch rec.Action {
		case "buy":
			actionClass = "positive"
			actionIcon = "↑"
		case "sell":
			actionClass = "negative"
			actionIcon = "↓"
		case "watch":
			actionClass = "watch"
			actionIcon = "👁"
		}
		riskClass := "medium"
		switch rec.Risk {
		case "low":
			riskClass = "low"
		case "high":
			riskClass = "high"
		}
		data.Recommendations = append(data.Recommendations, RecommendationData{
			Ticker:      rec.Ticker,
			Name:        rec.Name,
			Action:      rec.Action,
			ActionClass: actionClass,
			ActionIcon:  actionIcon,
			Reason:      rec.Reason,
			Risk:        rec.Risk,
			RiskClass:   riskClass,
		})
	}

	return data
}

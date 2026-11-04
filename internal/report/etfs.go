package report

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"stock-portfolio/internal/chartcalc"
	"stock-portfolio/internal/models"
)

// ETFSeries reuses the decision histories without additional market requests.
type ETFSeries struct {
	Daily, Weekly    []chartcalc.Candle
	WeeklyTimezone   string
	WeeklySessionEnd time.Time
}

type ETFPeriodData struct {
	Change, ChangeClass string
	RSI, RSIClass       string
	Date                string
	rsi, change         *float64
	rebounding          bool
}

type ETFRowData struct {
	Ticker, Name          string
	Sector                string
	Examples, ExamplesURL string
	Daily, Weekly         ETFPeriodData
	State, StateClass     string
}

// BuildETFOverview includes the configured ETFs category, even outside the
// portfolio and without alerts. Ranking uses the last completed weekly RSI.
func BuildETFOverview(stocks []models.Stock, series map[string]ETFSeries, now time.Time) []ETFRowData {
	var rows []ETFRowData
	seen := map[string]bool{}
	for _, stock := range stocks {
		if !strings.EqualFold(strings.TrimSpace(stock.Category), "ETFs") || seen[stock.Ticker] {
			continue
		}
		seen[stock.Ticker] = true
		s := series[stock.Ticker]
		weekly := completedETFWeeks(s, now)
		row := ETFRowData{Ticker: stock.Ticker, Name: stock.Name, Sector: etfSector(stock), Daily: etfPeriod(s.Daily, now, 7), Weekly: etfPeriod(weekly, now, 14)}
		row.Examples, row.ExamplesURL = etfSectorExamples(stock.Ticker)
		if row.Weekly.Date != "" {
			loc, err := time.LoadLocation(s.WeeklyTimezone)
			if err != nil {
				loc = time.UTC
			}
			row.Weekly.Date = "Sem. du " + time.Unix(weekly[len(weekly)-1].Time, 0).In(loc).Format("02/01")
		}
		row.State, row.StateClass = etfState(row.Daily, row.Weekly)
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].Weekly.rsi, rows[j].Weekly.rsi
		if a == nil || b == nil {
			if a != nil {
				return true
			}
			if b != nil {
				return false
			}
		} else if *a != *b {
			return *a > *b
		}
		return rows[i].Ticker < rows[j].Ticker
	})
	return rows
}

func etfSector(stock models.Stock) string {
	labels := map[string]string{
		"VOO": "S&P 500", "QQQ": "Nasdaq 100", "SOXX": "Semi-conducteurs", "^SOX": "Semi-conducteurs",
		"DRAM": "Mémoire", "XLE": "Énergie", "XLV": "Santé", "WHEA.SW": "Santé mondiale",
		"XLP": "Consommation courante", "XLB": "Matériaux", "XLI": "Industrie", "XLU": "Services publics",
		"XLK": "Technologie", "MCHI": "Chine", "IBIT": "Bitcoin", "XLF": "Finance",
	}
	if label, ok := labels[stock.Ticker]; ok {
		return label
	}
	return stock.Name
}

// Representative examples, checked against State Street holdings on 2026-11-04.
// These are explanatory labels, not a dynamically updated top-three ranking.
func etfSectorExamples(ticker string) (string, string) {
	examples := map[string]struct{ tickers, sectorSlug string }{
		"XLV": {"LLY · JNJ · ABBV", "health-care"},
		"XLE": {"XOM · CVX · COP", "energy"},
		"XLP": {"WMT · COST · PG", "consumer-staples"},
		"XLB": {"LIN · NEM · VMC", "materials"},
		"XLF": {"BRK.B · JPM · V", "financial"},
		"XLU": {"NEE · SO · DUK", "utilities"},
		"XLI": {"GE · CAT · RTX", "industrial"},
		"XLK": {"NVDA · AAPL · MSFT", "technology"},
	}
	if example, ok := examples[ticker]; ok {
		return example.tickers, "https://www.ssga.com/us/en/individual/etfs/state-street-" + example.sectorSlug + "-select-sector-spdr-etf-" + strings.ToLower(ticker)
	}
	return "", ""
}

func completedETFWeeks(s ETFSeries, now time.Time) []chartcalc.Candle {
	// A week is complete from Saturday in the exchange's timezone. On Friday,
	// accept it after the exchange's reported regular-session close, when known.
	loc, err := time.LoadLocation(s.WeeklyTimezone)
	if err != nil {
		loc = time.UTC
	}
	localNow := now.In(loc)
	year, week := localNow.ISOWeek()
	sessionEnd := s.WeeklySessionEnd.In(loc)
	fridayClosed := localNow.Weekday() == time.Friday && sessionEnd.Year() == localNow.Year() && sessionEnd.YearDay() == localNow.YearDay() && !now.Before(sessionEnd)
	currentComplete := localNow.Weekday() == time.Saturday || localNow.Weekday() == time.Sunday || fridayClosed
	end := len(s.Weekly)
	for end > 0 {
		date := time.Unix(s.Weekly[end-1].Time, 0).In(loc)
		y, w := date.ISOWeek()
		if y < year || (y == year && w < week) || (y == year && w == week && currentComplete) {
			break
		}
		end--
	}
	return s.Weekly[:end]
}

func etfPeriod(candles []chartcalc.Candle, now time.Time, maxAgeDays int) ETFPeriodData {
	p := ETFPeriodData{Change: "Indisponible", RSI: "Indisponible", ChangeClass: "neutral", RSIClass: "neutral"}
	if len(candles) < 2 {
		return p
	}
	for i, c := range candles {
		if c.Close <= 0 || math.IsNaN(c.Close) || math.IsInf(c.Close, 0) || c.Time <= 0 || time.Unix(c.Time, 0).After(now.Add(5*time.Minute)) || (i > 0 && c.Time <= candles[i-1].Time) {
			return p
		}
	}
	last := candles[len(candles)-1]
	date := time.Unix(last.Time, 0).UTC()
	if date.Before(now.AddDate(0, 0, -maxAgeDays)) {
		return p
	}
	p.Date = date.Format("02/01")
	change := (last.Close/candles[len(candles)-2].Close - 1) * 100
	if !math.IsNaN(change) && !math.IsInf(change, 0) {
		p.change = &change
		p.Change = fmt.Sprintf("%+.2f%%", change)
		if change > 0 {
			p.ChangeClass = "positive"
		} else if change < 0 {
			p.ChangeClass = "negative"
		}
	}
	points := chartcalc.CalcRSI14(candles)
	if len(points) == 0 {
		return p
	}
	rsi := points[len(points)-1].Value
	if math.IsNaN(rsi) || math.IsInf(rsi, 0) || rsi < 0 || rsi > 100 {
		return p
	}
	p.rsi = &rsi
	p.RSI = fmt.Sprintf("%.1f", rsi)
	if rsi > 70 {
		p.RSIClass = "etf-rsi-high"
	} else if rsi < 40 {
		p.RSIClass = "etf-rsi-low"
	}
	// A rising RSI and a positive day after RSI < 40 in the previous 5 sessions.
	if len(points) > 1 && p.change != nil && *p.change > 0 && rsi > points[len(points)-2].Value {
		for _, point := range points[max(0, len(points)-6) : len(points)-1] {
			if point.Value < 40 {
				p.rebounding = true
				break
			}
		}
	}
	return p
}

func etfState(daily, weekly ETFPeriodData) (string, string) {
	if daily.rsi == nil || weekly.rsi == nil {
		return "Indisponible", "neutral"
	}
	d, w := *daily.rsi, *weekly.rsi
	switch {
	case d > 70 && w > 70:
		return "Surchauffe", "etf-rsi-high"
	case d > 70:
		return "Tendu", "etf-rsi-high"
	case daily.rebounding:
		return "Rebond potentiel", "positive"
	case d < 40 && w < 40:
		return "Délaissé", "etf-rsi-low"
	case d >= 40 && d <= 70 && w >= 40 && w <= 70:
		return "Neutre", "neutral"
	default:
		return "Mixte", "neutral"
	}
}

package barometer

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"stock-portfolio/internal/yahoo"
)

type Bias struct {
	Label, Class string
}

type Statistics struct {
	Name                            string
	Count, Positive, Negative, Flat int
	PositivePercent, Mean, Median   float64
	OpenMean                        float64
	OpenCount                       int
	Best, Worst                     float64
	BestYear, WorstYear             int
	Period, Coverage                string
	Bias                            Bias
}

type HistoricalContext struct {
	Title       string
	Instruments []Statistics
	Bias        Bias
	Note        string
}

// MarketContext is supplemental context; none of its values enter Evaluate's score.
type MarketContext struct {
	Sector, Barometer Bias
	SectorFactors     []Factor
	Weekday, Monthly  HistoricalContext
	Conclusion        string
}

func neutralBias() Bias     { return Bias{"Neutre", "neutral"} }
func unavailableBias() Bias { return Bias{"Indisponible", "neutral"} }

func concordance(a, b Bias) Bias {
	if a.Label == "Indisponible" || b.Label == "Indisponible" {
		return unavailableBias()
	}
	if a.Label == b.Label {
		return a
	}
	return neutralBias()
}

// EvaluateContext uses the same Nasdaq futures quote as the main barometer.
func EvaluateContext(summary *Summary, quotes map[string]yahoo.MarketQuote, history map[string][]yahoo.DailyBar, now time.Time) *MarketContext {
	loc, _ := time.LoadLocation("America/New_York")
	now = now.In(loc)
	c := &MarketContext{Barometer: unavailableBias(), Sector: unavailableBias()}
	if summary != nil {
		c.Barometer = Bias{summary.Label, summary.Class}
	}
	sectorBiases := make([]Bias, 0, 2)
	for _, instrument := range []struct{ ticker, name string }{{"NQ=F", "Futures Nasdaq 100"}, {"SOXX", "SOXX"}} {
		q, ok := quotes[instrument.ticker]
		f := Factor{Name: instrument.name, Value: "Indisponible", Class: "neutral", Observation: "Donnée absente, ancienne ou invalide"}
		b := unavailableBias()
		if !q.AsOf.IsZero() {
			f.AsOf = q.AsOf.In(loc).Format("02/01 15:04 MST")
		}
		if ok && usable(q, instrument.ticker, now) {
			b = neutralBias()
			d := direction(q.ChangePercent(), .1, .25)
			if d > 0 {
				b = Bias{"Favorable", "positive"}
			} else if d < 0 {
				b = Bias{"Défavorable", "negative"}
			}
			f.Value, f.Class = FormatPercent(q.ChangePercent()), b.Class
			f.Observation = "Variation depuis la clôture de référence"
			if instrument.ticker == "NQ=F" {
				f.Observation += " · cotation réutilisée du baromètre"
			}
		}
		c.SectorFactors = append(c.SectorFactors, f)
		sectorBiases = append(sectorBiases, b)
	}
	c.Sector = concordance(sectorBiases[0], sectorBiases[1])
	days := [...]string{"dimanche", "lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi"}
	months := [...]string{"", "janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}
	c.Weekday = HistoricalContext{Title: "Backtest du " + days[now.Weekday()], Bias: unavailableBias()}
	prefix := "Saisonnalité de "
	if now.Month() == time.April || now.Month() == time.August || now.Month() == time.October {
		prefix = "Saisonnalité d’"
	}
	c.Monthly = HistoricalContext{Title: prefix + months[now.Month()], Bias: unavailableBias()}
	weekend := now.Weekday() == time.Saturday || now.Weekday() == time.Sunday
	for _, instrument := range []struct{ ticker, name string }{{"^NDX", "Nasdaq 100"}, {"SOXX", "SOXX"}} {
		bars := history[instrument.ticker]
		if !weekend {
			c.Weekday.Instruments = append(c.Weekday.Instruments, weekdayStatistics(instrument.name, bars, now))
		}
	}
	c.Monthly.Instruments = []Statistics{monthlyStatistics("Nasdaq 100", history["^NDX"], now)}
	if weekend {
		c.Weekday.Note = "Non applicable — hors séance"
	} else {
		c.Weekday.Bias = concordance(c.Weekday.Instruments[0].Bias, c.Weekday.Instruments[1].Bias)
	}
	c.Monthly.Bias = c.Monthly.Instruments[0].Bias
	c.Conclusion = conclusion(c)
	return c
}

func validPrice(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

type observation struct {
	date  time.Time
	value float64
}

func summarize(name string, values []observation, minimum, target int) Statistics {
	s := Statistics{Name: name, Count: len(values), Bias: unavailableBias()}
	s.Coverage = fmt.Sprintf("%d/%d observations · minimum %d", s.Count, target, minimum)
	if s.Count == 0 {
		return s
	}
	ordered := make([]float64, 0, s.Count)
	s.Best, s.Worst = values[0].value, values[0].value
	s.BestYear, s.WorstYear = values[0].date.Year(), values[0].date.Year()
	for _, v := range values {
		s.Mean += v.value
		ordered = append(ordered, v.value)
		switch {
		case v.value > 0:
			s.Positive++
		case v.value < 0:
			s.Negative++
		default:
			s.Flat++
		}
		if v.value > s.Best {
			s.Best, s.BestYear = v.value, v.date.Year()
		}
		if v.value < s.Worst {
			s.Worst, s.WorstYear = v.value, v.date.Year()
		}
	}
	s.Mean /= float64(s.Count)
	s.PositivePercent = 100 * float64(s.Positive) / float64(s.Count)
	sort.Float64s(ordered)
	s.Median = ordered[s.Count/2]
	if s.Count%2 == 0 {
		s.Median = (ordered[s.Count/2-1] + s.Median) / 2
	}
	s.Period = values[len(values)-1].date.Format("02/01/2006") + " → " + values[0].date.Format("02/01/2006")
	if s.Count >= minimum {
		s.Bias = neutralBias()
		if s.PositivePercent >= 60 && s.Mean > 0 && s.Median > 0 {
			s.Bias = Bias{"Favorable", "positive"}
		} else if s.PositivePercent <= 40 && s.Mean < 0 && s.Median < 0 {
			s.Bias = Bias{"Défavorable", "negative"}
		}
	}
	return s
}

func weekdayStatistics(name string, bars []yahoo.DailyBar, now time.Time) Statistics {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var values []observation
	openSum, openCount := 0.0, 0
	for i := len(bars) - 1; i > 0 && len(values) < 8; i-- {
		b, prev := bars[i], bars[i-1]
		date := b.Date.In(now.Location())
		if !date.Before(today) || date.Weekday() != now.Weekday() || !validPrice(b.Close) || !validPrice(prev.Close) {
			continue
		}
		values = append(values, observation{date, (b.Close/prev.Close - 1) * 100})
		if validPrice(b.Open) {
			openSum += (b.Close/b.Open - 1) * 100
			openCount++
		}
	}
	s := summarize(name, values, 8, 8)
	s.OpenCount = openCount
	if openCount > 0 {
		s.OpenMean = openSum / float64(openCount)
	}
	return s
}

func monthlyStatistics(name string, bars []yahoo.DailyBar, now time.Time) Statistics {
	var values []observation
	// Require observations on both sides of each month to establish full coverage.
	for end := len(bars) - 2; end > 0 && len(values) < 15; end-- {
		date := bars[end].Date.In(now.Location())
		next := bars[end+1].Date.In(now.Location())
		if date.Month() != now.Month() || date.Year() >= now.Year() || (next.Month() == date.Month() && next.Year() == date.Year()) {
			continue
		}
		start, valid := end, true
		for start >= 0 {
			d := bars[start].Date.In(now.Location())
			if d.Month() != date.Month() || d.Year() != date.Year() {
				break
			}
			valid = valid && validPrice(bars[start].Close)
			start--
		}
		if start < 0 || !valid || !validPrice(bars[start].Close) {
			continue
		}
		previous := bars[start].Date.In(now.Location())
		monthStart := time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, now.Location())
		expected := monthStart.AddDate(0, -1, 0)
		if previous.Month() != expected.Month() || previous.Year() != expected.Year() {
			continue
		}
		expectedNext := monthStart.AddDate(0, 1, 0)
		if next.Month() != expectedNext.Month() || next.Year() != expectedNext.Year() {
			continue
		}
		values = append(values, observation{date, (bars[end].Close/bars[start].Close - 1) * 100})
	}
	return summarize(name, values, 10, 15)
}

func conclusion(c *MarketContext) string {
	if c.Barometer.Label == "Indisponible" {
		return "Baromètre actions US indisponible ; conviction non évaluable. Les statistiques restent un contexte historique."
	}
	base := "Environnement global " + strings.ToLower(c.Barometer.Label)
	biases := []Bias{c.Barometer, c.Sector, c.Weekday.Bias, c.Monthly.Bias}
	for _, b := range biases {
		if b.Label == "Indisponible" {
			return base + ", mais données complémentaires insuffisantes. Conviction limitée."
		}
	}
	if c.Barometer.Class != "neutral" && c.Barometer.Class == c.Sector.Class && c.Barometer.Class == c.Weekday.Bias.Class && c.Barometer.Class == c.Monthly.Bias.Class {
		return base + ", confirmé par le secteur et les contextes statistiques. Conviction qualitative renforcée."
	}
	if (c.Barometer.Class == "positive" && c.Weekday.Bias.Class == "negative") || (c.Barometer.Class == "negative" && c.Weekday.Bias.Class == "positive") {
		return base + ", mais biais statistique court terme opposé. Conviction modérée."
	}
	return base + ", avec des lectures sectorielles ou statistiques mitigées. Conviction modérée."
}

// FormatPercent is shared by supplemental factors and the HTML template.
func FormatPercent(value float64) string {
	return strings.ReplaceAll(fmt.Sprintf("%+.2f %%", value), ".", ",")
}

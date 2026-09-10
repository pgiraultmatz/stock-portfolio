// Package barometer summarizes observed US equity market conditions, not a return forecast.
package barometer

import (
	"fmt"
	"math"
	"time"
	_ "time/tzdata"

	"stock-portfolio/internal/yahoo"
)

var Instruments = []struct{ Ticker, Name string }{
	{"ES=F", "Futures S&P 500"},
	{"NQ=F", "Futures Nasdaq 100"},
	{"^VIX", "VIX"},
	{"BZ=F", "Brent"},
	{"^TNX", "Taux US 10 ans"},
}

type Factor struct {
	Name, Value, Observation, AsOf, Class string
}

type Summary struct {
	Label, Class, Context, Coverage string
	Factors                         []Factor
}

// Evaluate uses fixed, uncalibrated thresholds. Missing inputs are never treated as zero moves.
func Evaluate(quotes map[string]yahoo.MarketQuote, now time.Time) *Summary {
	loc, _ := time.LoadLocation("America/New_York")
	now = now.In(loc)
	s := &Summary{Label: "Indisponible", Class: "neutral", Context: "Lecture indicative des actions US, pas une prévision de clôture."}
	minute := now.Hour()*60 + now.Minute()
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday || minute >= 16*60 {
		s.Context = "Hors séance US : dernières observations disponibles, pas une prévision de la prochaine séance."
	} else if minute < 9*60+30 {
		s.Context = "Avant ouverture US : biais indicatif, pas une prévision de clôture."
	}
	valid := make(map[string]bool)
	for _, item := range Instruments {
		q, ok := quotes[item.Ticker]
		valid[item.Ticker] = ok && usable(q, item.Ticker, now)
	}
	up := valid["ES=F"] && valid["NQ=F"] && direction(quotes["ES=F"].ChangePercent(), .1, .25) > 0 && direction(quotes["NQ=F"].ChangePercent(), .1, .25) > 0
	score, count := 0, 0
	for _, item := range Instruments {
		q, exists := quotes[item.Ticker]
		f := Factor{Name: item.Name, Value: "Indisponible", Class: "neutral", Observation: "Non pris en compte"}
		if exists && !q.AsOf.IsZero() {
			f.AsOf = q.AsOf.In(loc).Format("02/01 15:04 MST")
		}
		if !valid[item.Ticker] {
			if exists {
				f.Observation = "Donnée ancienne ou invalide, non prise en compte"
			}
			s.Factors = append(s.Factors, f)
			continue
		}
		count++
		change := q.ChangePercent()
		f.Value = fmt.Sprintf("%.2f (%+.2f%%)", q.Price, change)
		points := 0
		switch item.Ticker {
		case "ES=F", "NQ=F":
			points = direction(change, .1, .25)
			f.Observation = "Direction des futures"
		case "^VIX":
			points = -direction(change, 3, 10)
			if q.Price >= 30 {
				points -= 2
			} else if q.Price >= 25 {
				points--
			}
			points = max(-2, min(2, points))
			f.Observation = "Niveau et variation de la volatilité"
		case "BZ=F":
			if change >= 2 {
				points = -1
			} else if change <= -2 && up {
				points = 1
			}
			f.Value = fmt.Sprintf("%.2f USD (%+.2f%%)", q.Price, change)
			f.Observation = "Pression énergétique ; baisse favorable seulement si les futures montent"
		case "^TNX":
			// Yahoo quotes TNX in percentage points: 4.10 to 4.15 is +5 basis points.
			bps := (q.Price - q.PreviousClose) * 100
			if bps >= 5-1e-9 {
				points = -1
			} else if bps <= -5+1e-9 && up {
				points = 1
			}
			f.Value = fmt.Sprintf("%.2f%% (%+.1f pb)", q.Price, bps)
			f.Observation = "Pression des taux ; détente favorable seulement si les futures montent"
		}
		score += points
		if points > 0 {
			f.Class = "positive"
		} else if points < 0 {
			f.Class = "negative"
		}
		f.Observation += fmt.Sprintf(" (%+d)", points)
		s.Factors = append(s.Factors, f)
	}
	s.Coverage = fmt.Sprintf("Yahoo Finance · %d/5 facteurs exploitables · variations depuis la clôture de référence", count)
	if !valid["ES=F"] || !valid["NQ=F"] || !valid["^VIX"] {
		s.Coverage += " · futures et VIX requis pour qualifier le biais"
		return s
	}
	if count < 5 {
		s.Coverage += " · couverture partielle"
	}
	switch {
	case score >= 4 && up && count == 5 && quotes["^VIX"].Price < 25 && quotes["^VIX"].ChangePercent() < 10:
		s.Label, s.Class = "Favorable", "positive"
	case score >= 2 && up:
		s.Label, s.Class = "Assez favorable", "positive"
	case score <= -4:
		s.Label, s.Class = "Défavorable", "negative"
	case score <= -2:
		s.Label, s.Class = "Assez défavorable", "negative"
	default:
		s.Label = "Mitigé"
	}
	return s
}

func direction(value, mild, strong float64) int {
	switch {
	case value >= strong-1e-9:
		return 2
	case value >= mild-1e-9:
		return 1
	case value <= -strong+1e-9:
		return -2
	case value <= -mild+1e-9:
		return -1
	default:
		return 0
	}
}

func usable(q yahoo.MarketQuote, ticker string, now time.Time) bool {
	if q.Price <= 0 || q.PreviousClose <= 0 || math.IsNaN(q.Price) || math.IsNaN(q.PreviousClose) || math.IsInf(q.Price, 0) || math.IsInf(q.PreviousClose, 0) || q.AsOf.IsZero() {
		return false
	}
	age := now.Sub(q.AsOf)
	if age < -5*time.Minute || now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		return false
	}
	if ticker == "ES=F" || ticker == "NQ=F" || ticker == "BZ=F" {
		return age <= 90*time.Minute
	}
	// Before cash trading, allow the prior weekday close with its original timestamp.
	minute := now.Hour()*60 + now.Minute()
	date := q.AsOf.In(now.Location())
	closeMinute := 15 * 60
	if ticker == "^TNX" {
		// Yahoo's final TNX tick can be just before 15:00 New York.
		closeMinute = 14*60 + 45
	}
	closingObservation := date.Hour()*60+date.Minute() >= closeMinute
	if minute < 9*60+30 {
		previous := now.AddDate(0, 0, -1)
		for previous.Weekday() == time.Saturday || previous.Weekday() == time.Sunday {
			previous = previous.AddDate(0, 0, -1)
		}
		return date.Format("2006-01-02") == now.Format("2006-01-02") || (date.Format("2006-01-02") == previous.Format("2006-01-02") && closingObservation)
	}
	if date.Format("2006-01-02") != now.Format("2006-01-02") {
		return false
	}
	return age <= 90*time.Minute || (minute >= 16*60 && closingObservation)
}

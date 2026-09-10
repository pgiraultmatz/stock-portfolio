package macro

import (
	"strings"
	"time"
)

// CalendarHighlights selects a short US macro calendar, leaving raw events in
// the cache. Inflation uses monthly releases, never relabelled annual figures.
func CalendarHighlights(events []Event, now time.Time, days int) []Event {
	var highlights []Event
	seen := make(map[string]bool)
	for _, event := range Upcoming(events, now, days) {
		family, name := majorRelease(event)
		if family == "" {
			continue
		}
		key := family + "|" + event.Date.Format(time.RFC3339)
		if seen[key] {
			continue
		}
		seen[key] = true
		event.Name, event.Category, event.Importance = name, family, "high"
		highlights = append(highlights, event)
	}
	return highlights
}

func majorRelease(event Event) (string, string) {
	if event.Category == "FOMC" {
		return "FOMC", event.Name
	}
	if event.Country != "US" {
		return "", ""
	}
	n := strings.ToLower(strings.TrimSpace(event.Name))
	switch {
	case strings.HasPrefix(n, "ppi "), strings.HasPrefix(n, "core ppi"), strings.Contains(n, "producer price"):
		if monthlyRelease(n) && !coreRelease(n) {
			return "PPI", "PPI US - MoM"
		}
	case strings.HasPrefix(n, "cpi "), strings.HasPrefix(n, "core cpi"), strings.Contains(n, "consumer price"), strings.HasPrefix(n, "inflation rate"), strings.HasPrefix(n, "core inflation"):
		if monthlyRelease(n) && !coreRelease(n) {
			return "CPI", "CPI US - MoM"
		}
	case strings.HasPrefix(n, "pce price"), strings.HasPrefix(n, "core pce price"):
		if monthlyRelease(n) {
			return "PCE", "PCE US - inflation mensuelle"
		}
	case strings.HasPrefix(n, "gdp "), strings.Contains(n, "gross domestic product"):
		return "GDP", "GDP US - produit intérieur brut"
	case strings.Contains(n, "non-farm"), strings.Contains(n, "nonfarm"), strings.Contains(n, "unemployment rate"), strings.Contains(n, "employment situation"), strings.Contains(n, "non-farm payroll"):
		return "Employment", "Emploi US - NFP / chômage"
	case strings.Contains(n, "fed funds tgt"), strings.Contains(n, "fed funds target"), strings.Contains(n, "fomc rate decision"):
		return "FOMC", "FOMC - décision de taux US"
	}
	return "", ""
}

func monthlyRelease(name string) bool {
	normalized := strings.NewReplacer(",", " ", "(", " ", ")", " ", "*", " ").Replace(name)
	for _, token := range strings.Fields(normalized) {
		switch token {
		case "mm", "mom", "m/m", "m-o-m":
			return true
		}
	}
	return false
}

func coreRelease(name string) bool {
	for _, token := range strings.Fields(name) {
		if token == "core" || strings.HasPrefix(token, "ex") {
			return true
		}
	}
	return false
}

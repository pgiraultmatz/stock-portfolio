package macro

import (
	"fmt"
	"time"
)

type CalendarAlert struct {
	Title   string
	Message string
	State   string
}

// NearbyAlerts uses Eastern calendar days, not rolling 24-hour durations.
// A passed scheduled time does not confirm that the release actually occurred.
func NearbyAlerts(events []Event, now time.Time) []CalendarAlert {
	now = now.In(easternLocation())
	today := startOfDay(now)
	var alerts []CalendarAlert
	for _, event := range CalendarHighlights(events, now, 3) {
		limit, label := 1, event.Category+" US"
		switch event.Category {
		case "CPI", "PPI":
		case "FOMC":
			limit, label = 3, "FOMC"
		default:
			continue
		}
		day := startOfDay(event.Date)
		days := -1
		for i := 0; i <= limit; i++ {
			if day.Equal(today.AddDate(0, 0, i)) {
				days = i
				break
			}
		}
		if days < 0 {
			continue
		}
		when, state := fmt.Sprintf("Dans %d jours", days), "upcoming"
		if days == 0 {
			when, state = "Aujourd'hui", "today"
		}
		if days == 1 {
			when = "Demain"
		}
		message := "Volatilité potentiellement accrue autour de la publication."
		if event.Category == "FOMC" {
			message = "Décision de taux à surveiller ; volatilité potentiellement accrue."
		}
		if !event.Date.After(now) {
			state = "elapsed"
			message = "Heure prévue passée ; publication et résultat non vérifiés."
		}
		alerts = append(alerts, CalendarAlert{
			Title:   fmt.Sprintf("%s : %s, prévu le %s à %s (New York / Toronto)", when, label, event.Date.Format("02/01"), event.Date.Format("15:04")),
			Message: message, State: state,
		})
	}
	return alerts
}

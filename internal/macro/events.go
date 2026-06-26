// Package macro fetches market-moving economic calendar events from stable sources.
package macro

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const fedFOMCCalendarURL = "https://www.federalreserve.gov/monetarypolicy/fomccalendars.htm"

var (
	tagRe       = regexp.MustCompile(`<[^>]+>`)
	spaceRe     = regexp.MustCompile(`[ \t]+`)
	fomcDateRe  = regexp.MustCompile(`^(\d{1,2})(?:-(\d{1,2}))?(\*)?$`)
	monthNumber = map[string]time.Month{
		"January": time.January, "February": time.February, "March": time.March,
		"April": time.April, "May": time.May, "June": time.June,
		"July": time.July, "August": time.August, "September": time.September,
		"October": time.October, "November": time.November, "December": time.December,
	}
)

// Event is a normalized macro event stored in the Gist and rendered in reports.
type Event struct {
	Name       string    `json:"name"`
	Date       time.Time `json:"date"`
	Category   string    `json:"category"`
	Source     string    `json:"source"`
	Importance string    `json:"importance"`
}

// Merge deduplicates events by normalized category/name/date and sorts them by date.
func Merge(groups ...[]Event) []Event {
	seen := make(map[string]bool)
	var merged []Event
	for _, group := range groups {
		for _, event := range group {
			key := strings.ToLower(event.Category + "|" + event.Name + "|" + event.Date.Format("2006-01-02"))
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, event)
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Date.Before(merged[j].Date)
	})
	return merged
}

// Upcoming filters existing events to the same forward-looking report window.
func Upcoming(events []Event, now time.Time, days int) []Event {
	if days <= 0 {
		days = 21
	}
	start := startOfDay(now)
	end := start.AddDate(0, 0, days)
	return filterAndSort(events, start, end)
}

// UpcomingOfficialEvents fetches official high-importance events for the report window.
func UpcomingOfficialEvents(ctx context.Context, httpClient *http.Client, now time.Time, days int) ([]Event, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if days <= 0 {
		days = 21
	}
	start := startOfDay(now)
	end := start.AddDate(0, 0, days)

	events, err := fetchFOMCEvents(ctx, httpClient, start.Year())
	if err != nil {
		return nil, err
	}
	if nextYear := start.AddDate(0, 0, days).Year(); nextYear != start.Year() {
		nextEvents, nextErr := fetchFOMCEvents(ctx, httpClient, nextYear)
		if nextErr == nil {
			events = append(events, nextEvents...)
		}
	}

	return filterAndSort(events, start, end), nil
}

func fetchFOMCEvents(ctx context.Context, httpClient *http.Client, year int) ([]Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fedFOMCCalendarURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating FOMC request: %w", err)
	}
	req.Header.Set("User-Agent", "stock-checker/1.0")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching FOMC calendar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("FOMC calendar returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading FOMC calendar: %w", err)
	}

	lines := htmlLines(string(body))
	return parseFOMCLines(lines, year), nil
}

func parseFOMCLines(lines []string, year int) []Event {
	header := fmt.Sprintf("%d FOMC Meetings", year)
	inYear := false
	var currentMonth time.Month
	var events []Event

	for _, line := range lines {
		if strings.Contains(line, header) {
			inYear = true
			continue
		}
		if inYear && strings.HasSuffix(line, " FOMC Meetings") && !strings.Contains(line, header) {
			break
		}
		if !inYear {
			continue
		}

		if month, ok := monthNumber[line]; ok {
			currentMonth = month
			continue
		}
		if currentMonth == 0 {
			continue
		}

		matches := fomcDateRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		dayText := matches[1]
		if matches[2] != "" {
			dayText = matches[2]
		}
		day, err := strconv.Atoi(dayText)
		if err != nil {
			continue
		}

		name := "FOMC meeting"
		if matches[3] == "*" {
			name = "FOMC meeting + Summary of Economic Projections"
		}

		events = append(events, Event{
			Name:       name,
			Date:       time.Date(year, currentMonth, day, 14, 0, 0, 0, easternLocation()),
			Category:   "FOMC",
			Source:     "Federal Reserve",
			Importance: "high",
		})
	}

	return events
}

func htmlLines(raw string) []string {
	text := tagRe.ReplaceAllString(raw, "\n")
	text = html.UnescapeString(text)
	rawLines := strings.Split(text, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = spaceRe.ReplaceAllString(strings.TrimSpace(line), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func filterAndSort(events []Event, start, end time.Time) []Event {
	filtered := make([]Event, 0, len(events))
	for _, event := range events {
		eventDay := startOfDay(event.Date)
		if eventDay.Before(start) || eventDay.After(end) {
			continue
		}
		filtered = append(filtered, event)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Date.Before(filtered[j].Date)
	})
	return filtered
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func easternLocation() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.FixedZone("ET", -5*60*60)
	}
	return loc
}

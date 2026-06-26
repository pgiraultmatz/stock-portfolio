package yahoo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	econTitleRe = regexp.MustCompile(`class="title yf-[^"]+">([^<]+)</div>`)
	econTimeRe  = regexp.MustCompile(`data-testid="event-calendar-time">([^<]+)</time>`)
)

// EconomicEvent represents a macro economic event from the Yahoo Finance calendar.
type EconomicEvent struct {
	Name   string
	Date   time.Time
	Source string
}

// GetEconomicEvents fetches high-importance economic events for the current week
// from the Yahoo Finance economic calendar page.
func (c *Client) GetEconomicEvents(ctx context.Context) ([]EconomicEvent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://finance.yahoo.com/calendar/economic", nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/87.0.4280.88 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo economic calendar returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	titles := econTitleRe.FindAllSubmatch(body, -1)
	times := econTimeRe.FindAllSubmatch(body, -1)

	// Each event appears twice in the HTML — deduplicate by name+date
	seen := make(map[string]bool)
	var events []EconomicEvent

	for i := 0; i < len(titles) && i < len(times); i++ {
		name := strings.TrimSpace(string(titles[i][1]))
		dateStr := strings.TrimSpace(string(times[i][1]))

		key := name + "|" + dateStr
		if seen[key] {
			continue
		}
		seen[key] = true

		// Parse "Apr 30, 2026, 8:30 AM EDT"
		t, err := time.Parse("Jan 2, 2006, 3:04 PM MST", dateStr)
		if err != nil {
			// Try without timezone
			t, err = time.Parse("Jan 2, 2006, 3:04 PM", strings.Join(strings.Fields(dateStr)[:4], " "))
			if err != nil {
				continue
			}
		}

		events = append(events, EconomicEvent{
			Name:   strings.TrimSuffix(strings.TrimSpace(name), " *"),
			Date:   t,
			Source: "Yahoo Finance",
		})
	}

	return events, nil
}

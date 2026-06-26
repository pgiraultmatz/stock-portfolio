package yahoo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var earningsDateRe = regexp.MustCompile(`earningsDate.{0,15}raw.{0,15}:(\d{10})`)

// captures full match: "May 11 at 5 PM EDT" → groups: date, time, AM/PM, tz
var earningsCallTimeRe = regexp.MustCompile(`(Today|Tomorrow|([A-Z][a-z]+) (\d{1,2})) at (\d+(?::\d+)?) (AM|PM) (E[DS]T|P[DS]T|C[DS]T|M[DS]T)`)

var tzOffsets = map[string]int{
	"EST": -5, "EDT": -4,
	"CST": -6, "CDT": -5,
	"MST": -7, "MDT": -6,
	"PST": -8, "PDT": -7,
}

var monthNames = map[string]time.Month{
	"Jan": time.January, "Feb": time.February, "Mar": time.March,
	"Apr": time.April, "May": time.May, "Jun": time.June,
	"Jul": time.July, "Aug": time.August, "Sep": time.September,
	"Oct": time.October, "Nov": time.November, "Dec": time.December,
}

// GetNextEarningsDate fetches the next upcoming earnings date for a ticker
// by parsing the Yahoo Finance quote page HTML.
// Returns nil, nil if no upcoming date is found.
func (c *Client) GetNextEarningsDate(ctx context.Context, ticker string) (*time.Time, error) {
	url := fmt.Sprintf("https://finance.yahoo.com/quote/%s", ticker)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		return nil, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	// Step 1: get the earnings date from the raw timestamp
	matches := earningsDateRe.FindAllSubmatch(body, -1)
	if len(matches) == 0 {
		return nil, nil
	}

	now := time.Now()
	var earningsDate *time.Time
	for _, m := range matches {
		ts, err := strconv.ParseInt(string(m[1]), 10, 64)
		if err != nil {
			continue
		}
		t := time.Unix(ts, 0)
		if t.After(now) {
			earningsDate = &t
			break
		}
	}
	if earningsDate == nil {
		return nil, nil
	}

	// Step 2: find all call-time mentions and pick the one whose date matches earningsDate.
	// We iterate all matches to avoid picking up a stale previous-quarter mention first.
	callMatches := earningsCallTimeRe.FindAllSubmatch(body, -1)
	for _, cm := range callMatches {
		dateLabel := string(cm[1]) // "Today", "Tomorrow", or "May 11"
		monthStr := string(cm[2])  // "May" (empty for Today/Tomorrow)
		dayStr := string(cm[3])    // "11"  (empty for Today/Tomorrow)
		timeStr := string(cm[4])   // "5" or "10:30"
		ampm := string(cm[5])      // "AM" or "PM"
		tz := string(cm[6])        // "EDT"

		offset, ok := tzOffsets[tz]
		if !ok {
			continue
		}
		eastern := time.FixedZone(tz, offset*3600)
		ed := earningsDate.In(eastern)

		// Verify the matched date corresponds to earningsDate's calendar day.
		switch dateLabel {
		case "Today", "Tomorrow":
			// "Today/Tomorrow" on the Yahoo page is reliable — it refers to the current call.
		default:
			mon, ok := monthNames[monthStr]
			if !ok {
				continue
			}
			day, _ := strconv.Atoi(dayStr)
			if mon != ed.Month() || day != ed.Day() {
				continue // date mismatch — skip this match (likely a past quarter mention)
			}
		}

		var hour, minute int
		parts := strings.SplitN(timeStr, ":", 2)
		hour, _ = strconv.Atoi(parts[0])
		if len(parts) == 2 {
			minute, _ = strconv.Atoi(parts[1])
		}
		if ampm == "PM" && hour != 12 {
			hour += 12
		} else if ampm == "AM" && hour == 12 {
			hour = 0
		}

		callTime := time.Date(ed.Year(), ed.Month(), ed.Day(), hour, minute, 0, 0, eastern)
		utc := callTime.UTC()
		return &utc, nil
	}

	return earningsDate, nil
}

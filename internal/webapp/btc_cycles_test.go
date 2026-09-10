package webapp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/btccycle"
)

func TestCycleOptions(t *testing.T) {
	now := time.Date(2026, 9, 10, 13, 0, 0, 0, time.UTC)
	for _, query := range []string{"asOf=2026-09-10", "asOf=2026-09-11", "horizon=731", "horizon=invalid", "referenceStart=oops", "referenceStart=2023-01-01", "targetStart=2027-01-01"} {
		if _, err := cycleOptions(httptest.NewRequest("GET", "/?"+query, nil), now); err == nil {
			t.Errorf("accepted %s", query)
		}
	}
	opts, err := cycleOptions(httptest.NewRequest("GET", "/", nil), now)
	if err != nil {
		t.Fatal(err)
	}
	if time.Unix(opts.AsOf, 0).UTC().Format("2006-01-02") != "2026-09-09" {
		t.Fatal("included incomplete daily close")
	}
}

func TestBTCCycleEndpoint(t *testing.T) {
	start, _ := btccycle.Date("2015-01-01")
	target := start + 1200*86400
	asof := target + 500*86400
	history := ChartResponse{UpdatedAt: time.Now(), Stale: true}
	for d := 0; d <= 1750; d++ {
		phase := d
		base := 100.0
		if d >= 1200 {
			phase -= 1200
			base = 200
		}
		value := base * math.Exp(.001*float64(phase)+.2*math.Sin(float64(phase)/40))
		history.Candles = append(history.Candles, ChartCandle{Time: start + int64(d)*86400, Close: value})
	}
	u := fmt.Sprintf("/api/btc-cycles?referenceStart=2015-01-01&targetStart=%s&asOf=%s&refresh=1", time.Unix(target, 0).UTC().Format("2006-01-02"), time.Unix(asof, 0).UTC().Format("2006-01-02"))
	w := httptest.NewRecorder()
	serveBTCCycles(w, httptest.NewRequest("GET", u, nil), time.Now(), func(_ context.Context, refresh bool) (ChartResponse, error) {
		if !refresh {
			t.Error("refresh not forwarded")
		}
		return history, nil
	})
	if w.Code != 200 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	var result btccycle.Result
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.AsOf != asof || len(result.Actual) != 501 || !strings.Contains(w.Body.String(), "cache non actualisé") {
		t.Fatal("wrong as-of filtering or stale state")
	}
	for _, method := range []string{"POST", "DELETE"} {
		w = httptest.NewRecorder()
		serveBTCCycles(w, httptest.NewRequest(method, u, nil), time.Now(), nil)
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatal("unsupported method accepted")
		}
	}
	w = httptest.NewRecorder()
	serveBTCCycles(w, httptest.NewRequest("GET", u, nil), time.Now(), func(context.Context, bool) (ChartResponse, error) {
		return ChartResponse{}, fmt.Errorf("provider down")
	})
	if w.Code != http.StatusBadGateway {
		t.Fatal("missing provider error state")
	}
}

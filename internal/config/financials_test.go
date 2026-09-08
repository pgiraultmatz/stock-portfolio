package config

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/macro"
	"stock-portfolio/internal/models"
)

type financialTransport func(*http.Request) (*http.Response, error)

func (f financialTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSaveFundamentalsPersistsFinancialQuality(t *testing.T) {
	t.Setenv("GIST_ID", "test")
	t.Setenv("GH_TOKEN", "test")
	previous := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = previous })
	var saved StockDataFile
	http.DefaultClient = &http.Client{Transport: financialTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPatch {
			t.Fatalf("unexpected request: %s", r.Method)
		}
		var payload struct {
			Files map[string]struct {
				Content string `json:"content"`
			} `json:"files"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(payload.Files["stock-data.json"].Content), &saved); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}
	zero := 0.0
	at := time.Now().UTC()
	r := &models.StockResult{Stock: models.Stock{Ticker: "TEST"}, PEGRatio: 1.7, FinancialQuality: &models.FinancialQuality{CollectedAt: at, TotalDebt: &zero}}
	if err := SaveFundamentals([]*models.StockResult{r}, nil, []macro.Event{}); err != nil {
		t.Fatal(err)
	}
	f := saved.Stocks["TEST"]
	if f.PEGRatio != 1.7 || f.FinancialQuality == nil || f.FinancialQuality.TotalDebt == nil || *f.FinancialQuality.TotalDebt != 0 || f.FinancialQuality.FreeCashflow != nil || !f.FinancialQuality.CollectedAt.Equal(at) {
		t.Fatalf("financial cache lost values: %+v", f)
	}
}

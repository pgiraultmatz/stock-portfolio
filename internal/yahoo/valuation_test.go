package yahoo

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"stock-portfolio/internal/config"
)

type valuationTransport func(*http.Request) (*http.Response, error)

func (f valuationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func valuationClient(t *testing.T, summary string, status int) (*Client, *int) {
	t.Helper()
	requests := new(int)
	c := NewClient(config.YahooAPIConfig{})
	c.crumbOnce.Do(func() { c.crumb = "test" })
	c.httpClient.Transport = valuationTransport(func(r *http.Request) (*http.Response, error) {
		*requests++
		body, code := summary, status
		if strings.Contains(r.URL.Path, "timeseries") {
			body, code = `{"timeseries":{"result":[{"trailingPegRatio":[{"reportedValue":{"raw":1.7}}]}]}}`, 200
		} else if !strings.Contains(r.URL.Query().Get("modules"), "financialData") {
			t.Error("missing financialData module")
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	return c, requests
}

func TestValuationIncludesQualityAndPreservesRatios(t *testing.T) {
	c, requests := valuationClient(t, `{"quoteSummary":{"result":[{
	"quoteType":{"quoteType":"EQUITY"},"summaryDetail":{"forwardPE":{"raw":20}},
	"financialData":{"financialCurrency":"USD","targetMeanPrice":{"raw":120},"grossProfits":{"raw":100},"profitMargins":{"raw":0},"freeCashflow":{"raw":-200},"totalDebt":{"raw":0},"totalCash":{"raw":300}},
	"defaultKeyStatistics":{"enterpriseValue":{"raw":1000}},
	"earningsTrend":{"trend":[{"period":"0y","revenueEstimate":{"avg":{"raw":500},"growth":{"raw":0.1}}}]}
	}]}}`, 200)
	v, err := c.GetValuation(context.Background(), "TEST")
	if err != nil {
		t.Fatal(err)
	}
	if v.PEGRatio != 1.7 || v.PSGRatio != 0.2 || v.EVGrossProfit != 10 || v.TargetPrice != 120 {
		t.Fatalf("ratios changed: %+v", v)
	}
	q := v.FinancialQuality
	if q == nil || q.CollectedAt.IsZero() || q.Currency != "USD" || q.ProfitMargin == nil || *q.ProfitMargin != 0 || q.FreeCashflow == nil || *q.FreeCashflow != -200 || q.TotalDebt == nil || *q.TotalDebt != 0 || q.OperatingCashflow != nil {
		t.Fatalf("missing/zero/negative semantics lost: %+v", q)
	}
	if *requests != 2 {
		t.Fatalf("extra collection circuit: %d requests", *requests)
	}
}

func TestValuationUnavailableSummary(t *testing.T) {
	for _, body := range []string{`invalid`, `{"quoteSummary":{"result":[]}}`, `{"quoteSummary":{"error":{"code":"Unauthorized"}}}`} {
		c, _ := valuationClient(t, body, 200)
		v, err := c.GetValuation(context.Background(), "TEST")
		if err == nil || v.FinancialQuality != nil {
			t.Fatal("invalid summary accepted")
		}
	}
	c, _ := valuationClient(t, `{}`, 429)
	if _, err := c.GetValuation(context.Background(), "TEST"); err == nil {
		t.Fatal("HTTP failure ignored")
	}
}

func TestValuationNonEquitySkipsPEG(t *testing.T) {
	c, requests := valuationClient(t, `{"quoteSummary":{"result":[{"quoteType":{"quoteType":"ETF"}}]}}`, 200)
	v, err := c.GetValuation(context.Background(), "ETF")
	if err != nil || v.FinancialQuality.QuoteType != "ETF" || *requests != 1 {
		t.Fatalf("non-equity handling: %+v %v requests=%d", v, err, *requests)
	}
}

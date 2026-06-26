package yahoo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type valuationResponse struct {
	QuoteSummary struct {
		Result []struct {
			FinancialData struct {
				TargetMeanPrice struct {
					Raw float64 `json:"raw"`
				} `json:"targetMeanPrice"`
				GrossProfits struct {
					Raw float64 `json:"raw"`
				} `json:"grossProfits"`
			} `json:"financialData"`
			DefaultKeyStatistics struct {
				EnterpriseValue struct {
					Raw float64 `json:"raw"`
				} `json:"enterpriseValue"`
			} `json:"defaultKeyStatistics"`
			EarningsTrend struct {
				Trend []struct {
					Period          string `json:"period"`
					RevenueEstimate struct {
						Avg struct {
							Raw float64 `json:"raw"`
						} `json:"avg"`
						Growth struct {
							Raw float64 `json:"raw"`
						} `json:"growth"`
					} `json:"revenueEstimate"`
				} `json:"trend"`
			} `json:"earningsTrend"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"quoteSummary"`
}

type pegTimeseriesResponse struct {
	Timeseries struct {
		Result []struct {
			TrailingPegRatio []struct {
				ReportedValue struct {
					Raw float64 `json:"raw"`
				} `json:"reportedValue"`
			} `json:"trailingPegRatio"`
		} `json:"result"`
		Error *struct {
			Description string `json:"description"`
		} `json:"error"`
	} `json:"timeseries"`
}

// ValuationData holds all fetched valuation metrics for a ticker.
type ValuationData struct {
	TargetPrice   float64
	PEGRatio      float64
	PSGRatio      float64 // EV/fwdRevenue ÷ fwdRevenueGrowth%
	EVGrossProfit float64 // EV / Gross Profit (TTM)
}

// GetValuation fetches target price, trailing PEG, and PSG ratio for a ticker.
func (c *Client) GetValuation(ctx context.Context, ticker string) (ValuationData, error) {
	var result ValuationData

	if err := c.initCrumb(ctx); err != nil {
		return result, fmt.Errorf("init crumb: %w", err)
	}

	// Single quoteSummary call: financialData + defaultKeyStatistics + earningsTrend
	url := fmt.Sprintf(
		"https://query2.finance.yahoo.com/v10/finance/quoteSummary/%s?modules=financialData,defaultKeyStatistics,earningsTrend&crumb=%s",
		ticker, c.crumb,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return result, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return result, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		var v valuationResponse
		if json.Unmarshal(body, &v) == nil && v.QuoteSummary.Error == nil && len(v.QuoteSummary.Result) > 0 {
			r := v.QuoteSummary.Result[0]
			result.TargetPrice = r.FinancialData.TargetMeanPrice.Raw

			ev := r.DefaultKeyStatistics.EnterpriseValue.Raw

			// EV / Gross Profit (TTM)
			if gp := r.FinancialData.GrossProfits.Raw; ev > 0 && gp > 0 {
				result.EVGrossProfit = ev / gp
			}

			// PSG = (EV / fwdRevenue) / (fwdRevenueGrowth * 100)
			for _, t := range r.EarningsTrend.Trend {
				if t.Period == "0y" {
					fwdRev := t.RevenueEstimate.Avg.Raw
					fwdGrowth := t.RevenueEstimate.Growth.Raw // e.g. 0.2047
					if ev > 0 && fwdRev > 0 && fwdGrowth > 0 {
						evSales := ev / fwdRev
						result.PSGRatio = evSales / (fwdGrowth * 100)
					}
					break
				}
			}
		}
	}

	// Fetch trailingPegRatio (5yr expected, IBES consensus) from fundamentals timeseries
	now := time.Now()
	period1 := now.AddDate(0, -3, 0).Unix()
	period2 := now.Unix()
	tsURL := fmt.Sprintf(
		"https://query2.finance.yahoo.com/ws/fundamentals-timeseries/v1/finance/timeseries/%s?type=trailingPegRatio&period1=%d&period2=%d&crumb=%s",
		ticker, period1, period2, c.crumb,
	)
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, tsURL, nil)
	if err != nil {
		return result, nil
	}
	req2.Header.Set("User-Agent", c.config.UserAgent)
	req2.Header.Set("Accept", "application/json")

	resp2, err := c.httpClient.Do(req2)
	if err != nil {
		return result, nil
	}
	defer resp2.Body.Close()

	if resp2.StatusCode == http.StatusOK {
		body2, _ := io.ReadAll(resp2.Body)
		var ts pegTimeseriesResponse
		if json.Unmarshal(body2, &ts) == nil && ts.Timeseries.Error == nil && len(ts.Timeseries.Result) > 0 {
			entries := ts.Timeseries.Result[0].TrailingPegRatio
			if len(entries) > 0 {
				result.PEGRatio = entries[len(entries)-1].ReportedValue.Raw
			}
		}
	}

	return result, nil
}

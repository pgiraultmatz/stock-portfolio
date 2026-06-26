package main

import (
	"time"

	"stock-portfolio/internal/models"
)

func ptr(t time.Time) *time.Time { return &t }

// mockStockResults returns fake stock results for report generation testing.
func mockStockResults() []*models.StockResult {
	return []*models.StockResult{
		{
			Stock:            models.Stock{Ticker: "AAPL", Name: "Apple Inc.", Category: "Tech"},
			CurrentPrice:     189.45,
			ChangePercent:    1.23,
			RSI:              58.4,
			Currency:         "USD",
			TargetPrice:      230.00,
			PEGRatio:         2.8,
			PSGRatio:         0.42,
			EVGrossProfit:    18.5,
			NextEarningsDate: ptr(time.Now().AddDate(0, 0, 3)),
		},
		{
			Stock:            models.Stock{Ticker: "MSFT", Name: "Microsoft Corp.", Category: "Tech"},
			CurrentPrice:     415.20,
			ChangePercent:    -0.87,
			RSI:              72.1,
			Currency:         "USD",
			TargetPrice:      500.00,
			PEGRatio:         1.4,
			PSGRatio:         0.22,
			EVGrossProfit:    12.3,
			NextEarningsDate: ptr(time.Now().AddDate(0, 0, 18)),
		},
		{
			Stock:            models.Stock{Ticker: "NVDA", Name: "NVIDIA Corp.", Category: "Tech"},
			CurrentPrice:     875.30,
			ChangePercent:    3.45,
			RSI:              78.9,
			Currency:         "USD",
			TargetPrice:      1000.00,
			PEGRatio:         0.7,
			PSGRatio:         0.11,
			EVGrossProfit:    32.4,
			NextEarningsDate: ptr(time.Now().AddDate(0, 0, 60)),
		},
		{
			Stock:         models.Stock{Ticker: "GOOGL", Name: "Alphabet Inc.", Category: "Tech"},
			CurrentPrice:  165.80,
			ChangePercent: 0.52,
			RSI:           55.2,
			Currency:      "USD",
			TargetPrice:   210.00,
			PEGRatio:      1.1,
			EVGrossProfit: 6.8,
		},
		{
			Stock:         models.Stock{Ticker: "JPM", Name: "JPMorgan Chase", Category: "Finance"},
			CurrentPrice:  198.60,
			ChangePercent: -1.34,
			RSI:           42.7,
			Currency:      "USD",
			TargetPrice:   185.00,
			PEGRatio:      1.9,
			EVGrossProfit: 9.1,
		},
		{
			Stock:         models.Stock{Ticker: "GS", Name: "Goldman Sachs", Category: "Finance"},
			CurrentPrice:  452.10,
			ChangePercent: -2.10,
			RSI:           28.3,
			Currency:      "USD",
			TargetPrice:   520.00,
			PEGRatio:      0.9,
			EVGrossProfit: 5.2,
		},
		{
			Stock:         models.Stock{Ticker: "XOM", Name: "ExxonMobil Corp.", Category: "Energy"},
			CurrentPrice:  112.45,
			ChangePercent: 0.78,
			RSI:           50.1,
			Currency:      "USD",
			TargetPrice:   130.00,
			PEGRatio:      3.2,
			EVGrossProfit: 27.8,
		},
		{
			Stock:         models.Stock{Ticker: "BTC-USD", Name: "Bitcoin", Category: "Crypto"},
			CurrentPrice:  68420.00,
			ChangePercent: 4.20,
			RSI:           65.3,
			Currency:      "USD",
		},
		// Override test: strong fundamentals + earnings < 7d → should cap at BUY (not STRONG BUY)
		{
			Stock:            models.Stock{Ticker: "VST-T", Name: "VST Test (earnings cap)", Category: "Tech"},
			CurrentPrice:     100.00,
			ChangePercent:    1.5,
			RSI:              42.0,
			Currency:         "USD",
			TargetPrice:      180.00,
			PEGRatio:         0.6,
			PSGRatio:         0.10,
			EVGrossProfit:    6.0,
			NextEarningsDate: ptr(time.Now().AddDate(0, 0, 5)),
		},
		// Override test: high EV/GP + earnings < 7d → should cap at HOLD
		{
			Stock:            models.Stock{Ticker: "SMR-T", Name: "SMR Test (evgp cap)", Category: "Energy"},
			CurrentPrice:     100.00,
			ChangePercent:    2.0,
			RSI:              45.0,
			Currency:         "USD",
			TargetPrice:      155.00,
			PSGRatio:         0.10,
			EVGrossProfit:    223.8,
			NextEarningsDate: ptr(time.Now().AddDate(0, 0, 4)),
		},
	}
}

package news

import (
	"context"
	"strings"
	"time"

	"stock-portfolio/internal/models"
)

func DefaultCryptoConfig() Config {
	return Config{
		Enabled: true, MaxArticles: 6, MaxPerStock: 2, LookbackHours: 72,
		Themes: []Theme{{Name: "Crypto", Keywords: []string{"crypto", "cryptocurrency", "cryptocurrencies", "blockchain", "stablecoin", "stablecoins", "DeFi"}}},
	}
}

// CryptoUniverse retains actual holding flags; default market benchmarks are only watched.
func CryptoUniverse(stocks []models.Stock) (companies, crypto []models.Stock) {
	watch := false
	crypto = []models.Stock{
		{Ticker: "BTC-USD", Name: "Bitcoin", Category: "Cryptos", InPortfolio: &watch},
		{Ticker: "ETH-USD", Name: "Ethereum", Category: "Cryptos", InPortfolio: &watch},
		{Ticker: "SOL-USD", Name: "Solana", Category: "Cryptos", InPortfolio: &watch},
	}
	indices := map[string]int{"BTC-USD": 0, "ETH-USD": 1, "SOL-USD": 2}
	for _, stock := range stocks {
		ticker := strings.ToUpper(strings.TrimSpace(stock.Ticker))
		index, known := indices[ticker]
		if !known && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(stock.Category)), "crypto") {
			companies = append(companies, stock)
			continue
		}
		stock.Ticker = ticker
		if known {
			crypto[index] = stock
		} else {
			indices[ticker] = len(crypto)
			crypto = append(crypto, stock)
		}
	}
	return companies, crypto
}

// CollectSections gives each section its own article budget and availability status.
func (c *Client) CollectSections(ctx context.Context, stocks []models.Stock, cfg, cryptoCfg Config, now time.Time) (portfolio, crypto *Digest) {
	companies, cryptoStocks := CryptoUniverse(stocks)
	if !cryptoCfg.Enabled {
		companies = stocks
	}
	if cfg.Enabled {
		digest := c.Collect(ctx, companies, cfg, now)
		portfolio = &digest
	}
	if cryptoCfg.Enabled {
		aliases := make(map[string][]string, len(cryptoCfg.Aliases)+len(cryptoStocks))
		for ticker, names := range cryptoCfg.Aliases {
			aliases[ticker] = append([]string(nil), names...)
		}
		for _, stock := range cryptoStocks {
			base, quote, found := strings.Cut(stock.Ticker, "-")
			if found && (quote == "USD" || quote == "EUR" || quote == "CAD") {
				// Use ticker matching rules rather than case-insensitive aliases for short symbols.
				if name := map[string]string{"BTC": "Bitcoin", "ETH": "Ethereum", "SOL": "Solana"}[base]; name != "" {
					aliases[stock.Ticker] = append(aliases[stock.Ticker], name)
				}
			}
		}
		cryptoCfg.Aliases = aliases
		digest := c.Collect(ctx, cryptoStocks, cryptoCfg, now)
		crypto = &digest
	}
	if portfolio != nil && crypto != nil {
		urls, titles := map[string]bool{}, map[string]bool{}
		for _, article := range crypto.Articles {
			urls[canonicalURL(article.URL)] = true
			titles[normalize(article.Title)] = true
		}
		filtered := portfolio.Articles[:0]
		for _, article := range portfolio.Articles {
			if !urls[canonicalURL(article.URL)] && !titles[normalize(article.Title)] {
				filtered = append(filtered, article)
			}
		}
		portfolio.Articles = filtered
	}
	return portfolio, crypto
}

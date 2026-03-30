# Stock Portfolio Editor

A lightweight web app to manage a stock/ETF watchlist, organized by categories. Built with Go (single binary, no dependencies) and a vanilla JS frontend.

## Features

- Browse stocks grouped by category (Metals, Cryptos, Energy, USA, Defense, France, Others, ...)
- Add stocks with ticker search powered by Yahoo Finance autocomplete
- Delete stocks and manage categories (add, remove, reorder)
- Export the updated `config.json` directly from the UI
- All state is held in memory and persisted back to `config.json` on export

## Requirements

- Go 1.22+

## Getting started

```bash
# Build and run (default: http://localhost:8080)
make

# Or run directly from source
go run main.go
```

Then open http://localhost:8080 in your browser.

## Options

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.json` | Path to the portfolio config file |
| `--addr` | `:8080` | Listen address |

```bash
make run ADDR=:9000
make run CONFIG=/path/to/other-config.json
```

## Configuration

The app reads and writes `config.json`. Key sections:

```jsonc
{
  "stocks": [
    { "ticker": "NVDA", "name": "NVIDIA", "category": "USA" }
  ],
  "categories": [
    { "name": "USA", "emoji": "us", "order": 4 }
  ],
  "ai": { ... },       // AI integration settings (Gemini, etc.)
  "alerts": { ... },   // Price alert thresholds
  "yahoo_api": { ... } // Yahoo Finance fetch settings
}
```

Tickers follow Yahoo Finance conventions (e.g. `MC.PA` for Euronext Paris, `BTC-USD` for crypto).

## API endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/portfolio` | Get all stocks and categories |
| `POST` | `/api/stocks` | Add a stock |
| `DELETE` | `/api/stocks/{ticker}` | Remove a stock |
| `PATCH` | `/api/stocks/{ticker}` | Update a stock's category |
| `PUT` | `/api/stocks` | Replace the full stock list |
| `POST` | `/api/categories` | Add a category |
| `DELETE` | `/api/categories/{name}` | Remove a category |
| `PUT` | `/api/categories` | Replace the full category list |
| `GET` | `/api/search?q={query}` | Search tickers via Yahoo Finance |
| `GET` | `/api/export` | Download the current state as `config.json` |

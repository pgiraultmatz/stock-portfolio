# Stock Portfolio Editor

A lightweight web app to manage a stock/ETF watchlist, organized by categories. Built with Go (single binary, no dependencies) and a vanilla JS frontend.

## Features

- Browse stocks grouped by category (Metals, Cryptos, Energy, USA, Defense, France, Others, ...)
- Add stocks with ticker search powered by Yahoo Finance autocomplete
- Delete stocks and manage categories (add, remove, reorder)
- Save the portfolio config directly to a GitHub Gist from the UI
- All state is held in memory and persisted to a GitHub Gist on save

## Requirements

- Go 1.22+
- A GitHub personal access token with **Gist** scope
- A GitHub Gist containing a file named **`stock-config.json`**

## Getting started

### 1. Create the GitHub Gist

1. Go to [gist.github.com](https://gist.github.com) and create a new Gist
2. Name the file **`stock-config.json`** (required — the app looks for this exact name)
3. Paste the content of [`config-example.json`](./config-example.json) as the initial content
4. Copy the Gist ID from the URL: `https://gist.github.com/<user>/<GIST_ID>`

### 2. Create a `.env` file

Create a `.env` file at the root of the project:

```
GIST_ID=<your-gist-id>
GITHUB_TOKEN=<your-github-token>
```

- **`GIST_ID`** — the ID copied from your Gist URL in step 1
- **`GITHUB_TOKEN`** — a [GitHub personal access token](https://github.com/settings/tokens) with the `gist` scope (classic token) or `Gists: read/write` permission (fine-grained token)

> `.env` is listed in `.gitignore` and will never be committed.

### 3. Build and run

```bash
# Build and run (default: http://localhost:8080)
make

# Or run directly from source
go run main.go
```

Then open http://localhost:8080 in your browser.

Environment variables set in the shell always take precedence over the `.env` file:

```bash
GITHUB_TOKEN=ghp_xxx go run main.go
```

## Options

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `:8080` | Listen address |

```bash
make run ADDR=:9000
```

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
| `POST` | `/api/save` | Save the current state to the GitHub Gist |

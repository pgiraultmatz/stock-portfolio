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
GH_TOKEN=<your-github-token>
```

- **`GIST_ID`** — the ID copied from your Gist URL in step 1
- **`GH_TOKEN`** — a [GitHub personal access token](https://github.com/settings/tokens) with the `gist` scope (classic token) or `Gists: read/write` permission (fine-grained token)

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
GH_TOKEN=ghp_xxx go run main.go
```

## Options

### Market signal digest priorities

The market signal digest replaces its bullish/bearish counts with up to three
buy/reinforcement candidates and three trim/exit candidates. Buy candidates include
holdings and watchlist entries; trim/exit candidates only include holdings
(`inPortfolio: false` excludes a stock; an omitted flag retains the existing
held-by-default convention). Detailed technical tables remain below the top lists.

To show up to five candidates per side:

```bash
go run ./cmd/stock-checker -check-market-digest -market-digest-timeframe both -market-digest-top 5
```

The existing workflow needs no change for the default top three. Add
`-market-digest-top 5` to its stock-checker invocation to select five instead.

Ranking groups EMA crossings and breakouts into a structure family, and RSI,
MACD and divergences into a momentum family. Each family contributes at most once
per timeframe. Directional candidates require both families; daily/weekly
agreement receives a bonus, contrary signals reduce the score, and contrary
weekly structure excludes the candidate. EMA proximity alone does not qualify.
Recent signals break score ties; weaker configurations do not fill empty slots.

Technical signals expire after 7 calendar days (daily) or 21 (weekly); divergence
pivots expire after 14 or 42 days respectively. Missing and future timestamps are
excluded from ranking. An overbought bearish RSI divergence with positive structure
and no bullish momentum confirmation can instead produce a trim candidate.
Each candidate shows its evidence and date, not an inflated indicator count.

This is a technical review heuristic, not a calibrated probability or an automatic
trading instruction. Financial metrics do not change this ranking; position size
and tax considerations are not included.

### Standalone financial review

**Financial Review** is a separate report intended for a weekly review, triggered
manually through `.github/workflows/financial-review.yml` (no scheduled runs).
In GitHub Actions, select **Financial Review**, then **Run workflow**; the default
is up to 50 candidates (3, 5, 10, 20 and 30 are also available). Optionally enable email
delivery. Fewer candidates are shown if fewer qualify. The HTML is always
uploaded as an artifact. It uses the existing Gist secrets; email additionally
uses the existing email secrets.

To generate only this report locally, without technical chart checks or email:

```bash
go run ./cmd/stock-checker -check-financial-digest -financial-digest-top 50 -financial-digest-output financial-review.html
```

The financial review scans the entire portfolio and watchlist, not the technical
candidates. Only Yahoo-confirmed equities are eligible: cryptocurrencies, ETFs
and other instruments never appear in its rankings. The Market Signal Digest
remains technical-only and does not fetch or display this financial review.

The financial screen requires a positive PEG, net margin, revenue growth,
operating cash flow and free cash flow, plus known nonnegative debt and cash.
It compares eligible stocks by relative rank: PEG (lower, 40%), net margin
(higher, 20%), revenue growth (higher, 20%), and net debt divided by operating cash
flow (lower, 20%; net cash is treated as zero leverage). Ties receive equal rank;
final ties use PEG then ticker. This is an explicit, uncalibrated screening
heuristic, not fair value, a probability, or a sector-adjusted comparison.
It does not rank turnarounds with negative earnings or cash flow as opportunities.
Coverage and excluded incomplete/old data counts are displayed; empty slots
are never filled with ineligible stocks.

The section reuses cached PEG, PSG and EV/gross-profit ratios from
`stock-data.json`. The existing Yahoo valuation request also collects trailing
and forward P/E, net and operating margins, revenue growth, operating and free
cash flow, debt, cash and financial currency. Daily report runs persist these new
fields under `financial_quality`; older Gists remain compatible.

The financial review reuses snapshots collected within 24 hours, refreshing older or missing
data throughout the configured universe through the same Yahoo client. This
enrichment has a 120-second budget within the overall command timeout, uses the
configured concurrency capped at four, and never writes to the Gist. Failed
refreshes retain cached values but missing, undated, future or older-than-24-hour
required data cannot enter the financial ranking. Missing values are N/D, not zero.
Collection dates are retrieval times, not statement dates: the Yahoo fields have
different reporting/forecast periods and must not be treated as synchronized
financial statements. Monetary values use millions or billions of the financial currency,
which may differ from the stock's trading currency. Existing market workflows
remain unchanged; only the new financial workflow produces this report.

### Portfolio news in daily reports

A separate **Veille cryptos** section is enabled by default via `crypto_news`.
It uses Yahoo RSS for BTC-USD, ETH-USD and SOL-USD plus assets in categories
starting with `Crypto` (case-insensitive). Configured holding flags are preserved;
the three default assets are otherwise treated as watched, not owned.
Each section has its own limits: crypto defaults to 6 articles, 2 per asset,
and 72 hours. `crypto_news` accepts the same aliases, themes and extra RSS feeds
as `news`. Matching crypto articles are not repeated in the company section.
Set `crypto_news.enabled` to `false` to pause only crypto coverage. No API key
or workflow change is needed. This does not re-enable prompts, tweets or positions.

Daily reports currently show news, VIX and the earnings/macro calendars.
The position recap, AI/manual prompts and tweet collection are paused by default,
including for older Gist configurations. The optional `report` settings
`show_positions`, `enable_prompts` and `fetch_tweets` can re-enable them.
Prompts and tweets also require their respective `ai.enabled`/`twitter.enabled`
settings. The daily workflow explicitly skips Twitter and pauses the prompt email.

The stock-checker report includes recent Yahoo Finance RSS articles without an
API key. Articles mentioning portfolio companies come first, followed by watchlist
companies and configured themes. Defaults: 72 hours, 12 articles, at most 2 per
company. Set `news.enabled` to `false` to disable collection in full reports.

The `news` section in `config-example.json` supports company `aliases`, theme
`keywords`, and additional RSS `feeds`. Themes filter the collected feeds; they
do not initiate a separate web search. Company matching uses names, aliases and
explicit tickers, so ambiguous names can still require adjustment.

Articles retain the source excerpt and original language, publication time in
UTC, and link. Deduplication uses canonical URLs and identical normalized titles;
different articles about the same event are not yet grouped semantically. Feed
failures are shown as partial coverage. Collected excerpts also feed the existing
manual/AI analysis prompt; news display itself does not require an AI call.

To collect news only (no stock quote requests, AI calls, or Gist writes):

```bash
go run ./cmd/stock-checker -news-only -output news.html
```

Like the daily report, this reads the configured Gist when `GIST_ID` is set,
otherwise the local `-config` file. RSS availability and coverage vary by company.

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

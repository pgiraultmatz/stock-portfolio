// Stock Checker - A production-grade stock market analysis tool.
//
// This application fetches real-time stock data from Yahoo Finance,
// calculates RSI indicators, and generates HTML reports.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"time"

	"stock-portfolio/internal/ai"
	"stock-portfolio/internal/alerts"
	"stock-portfolio/internal/config"
	"stock-portfolio/internal/divergencealerts"
	"stock-portfolio/internal/macro"
	"stock-portfolio/internal/models"
	"stock-portfolio/internal/report"
	"stock-portfolio/internal/twitter"
	"stock-portfolio/internal/yahoo"
)

// loadDotEnv reads a .env file and sets variables that are not already set in the environment.
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // .env is optional
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}

func main() {
	loadDotEnv(".env")

	// Parse command line flags
	configPath := flag.String("config", "config.json", "Path to configuration file")
	twitterPromptPath := flag.String("twitter-prompt", "twitter_prompt.txt", "Path to Twitter-only prompt template file")
	outputPath := flag.String("output", "", "Path to output HTML file (defaults to stdout)")
	promptOutput := flag.String("prompt-output", "", "Path to write the generated prompt as plain text (optional)")
	promptHTMLOutput := flag.String("prompt-html-output", "", "Path to write the generated prompt as a standalone HTML email (optional)")
	check := flag.Bool("check", false, "Check a single stock (use -ticker to specify, random otherwise)")
	ticker := flag.String("ticker", "", "Ticker symbol to check (implies -check)")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	timeout := flag.Duration("timeout", 5*time.Minute, "Timeout for the entire operation")
	mock := flag.Bool("mock", false, "Use mock data instead of fetching from APIs (for testing report generation)")
	noTwitter := flag.Bool("no-twitter", false, "Skip Twitter fetching")
	twitterOnly := flag.Bool("twitter-only", false, "Fetch tweets and output a standalone analysis prompt (no Yahoo Finance)")
	checkAlerts := flag.Bool("check-alerts", false, "Check intraday price alerts and write report if any are triggered")
	alertsOutput := flag.String("alerts-output", "alerts.html", "Path to write the alerts HTML report")
	cryptoOnly := flag.Bool("crypto-only", false, "Restrict alert checks to crypto assets only (for off-market-hours runs)")
	checkDivergences := flag.Bool("check-divergences", false, "Check RSI divergence alerts and write report if any are triggered")
	divergencesOutput := flag.String("divergences-output", "divergences.html", "Path to write the RSI divergences HTML report")
	divergencesState := flag.String("divergences-state", ".divergence-state.json", "Path to RSI divergence alert state")
	flag.Parse()

	// Setup logging
	logLevel := slog.LevelInfo
	if *verbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	// Load configuration (from Gist if GIST_ID is set, otherwise local file)
	cfg, err := config.LoadAuto(*configPath)
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}
	logger.Info("configuration loaded", "stocks", len(cfg.Stocks), "concurrency", cfg.Concurrency)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Price alert mode: check intraday variations, write report only if new alerts triggered
	if *checkAlerts {
		if err := runAlerts(ctx, cfg, *alertsOutput, *cryptoOnly, logger); err != nil {
			logger.Error("alert check failed", "error", err)
			os.Exit(1)
		}
		return
	}

	if *checkDivergences {
		if err := runDivergenceAlerts(ctx, cfg, *divergencesOutput, *divergencesState, logger); err != nil {
			logger.Error("divergence check failed", "error", err)
			os.Exit(1)
		}
		return
	}

	// Twitter-only mode: fetch tweets and output a standalone prompt, skip Yahoo Finance entirely
	if *twitterOnly {
		if err := runTwitterPrompt(ctx, cfg, *twitterPromptPath, *promptOutput, logger); err != nil {
			logger.Error("twitter prompt failed", "error", err)
			os.Exit(1)
		}
		return
	}

	// Mock report mode: skip all API calls
	if *mock {
		if err := runMockReport(*outputPath, *promptHTMLOutput, logger); err != nil {
			logger.Error("mock report failed", "error", err)
			os.Exit(1)
		}
		return
	}

	// Fetch tweets if configured
	var xGroups []ai.XGroupSection
	if !*noTwitter {
		xGroups = fetchAllXGroups(ctx, cfg, logger)
	}

	// Single stock check mode (triggered by -check or -ticker)
	if *check || *ticker != "" {
		if err := runSingleCheck(ctx, cfg, *ticker, logger); err != nil {
			logger.Error("check failed", "error", err)
			os.Exit(1)
		}
		return
	}

	// Full report mode
	if err := runFullReport(ctx, cfg, *outputPath, *promptOutput, *promptHTMLOutput, xGroups, logger); err != nil {
		logger.Error("analysis failed", "error", err)
		os.Exit(1)
	}
}

func runMockReport(outputPath, promptHTMLOutput string, logger *slog.Logger) error {
	logger.Info("generating mock report with manual prompt (no API calls)")

	results := mockStockResults()

	promptTemplate, err := config.LoadPrompt()
	if err != nil {
		logger.Warn("failed to load prompt template", "error", err)
	}

	const mockSep = "────────────────────────────────────────────────────────────"
	mockXGroups := []ai.XGroupSection{
		{
			Name: "Cryptos Traders",
			Content: mockSep + `
Tweets récents de l'utilisateur @CryptoBull
` + mockSep + `

1. [07 May 2026 22:14]
BTC holding 68k strong. Next resistance at 72k, support at 65k. ETF inflows still very positive — accumulating on any dip under 67k.

2. [07 May 2026 18:45]
ETH/BTC ratio bouncing off lows. Ethereum rotation incoming? Layer 2 activity at ATH this week.

3. [07 May 2026 11:02]
Altseason not dead — just waiting for BTC dominance to peak around 57-58%. Watch MATIC and SOL closely.

` + mockSep + `
Tweets récents de l'utilisateur @MacroCryptoFR
` + mockSep + `

1. [08 May 2026 07:30]
Fed meeting demain — si Powell reste dovish, BTC pourrait tester les 70k rapidement. Risk-on mode activé sur les marchés asiatiques ce matin.

2. [07 May 2026 20:15]
Liquidations massives de shorts hier soir. $85M en 4h. Le marché a nettoyé les positions faibles, bonne base pour la suite.
`,
		},
		{
			Name: "Tech & Semi Analysts",
			Content: mockSep + `
Tweets récents de l'utilisateur @SemiWatcher
` + mockSep + `

1. [08 May 2026 08:55]
NVDA supply chain checks positive for Q3. TSMC N3 allocations mostly going to NVDA and Apple. AMD losing share at hyperscalers — their MI300X ramp slower than expected.

2. [07 May 2026 17:30]
Intel foundry customers still thin. TSMC moat widening. Watch for any Broadcom/AVGO custom silicon announcements at Google Next next week.

3. [07 May 2026 14:00]
Photonic interconnects becoming the real bottleneck for 2027 AI clusters. Ayar Labs, Celestial AI and Marvell all moving fast. $MRVL undervalued here.

` + mockSep + `
Tweets récents de l'utilisateur @AICapitalParis
` + mockSep + `

1. [08 May 2026 09:10]
Kalray obtient un LOI d'un opérateur télécom européen pour déployer son processeur MPPA dans des applications edge 5G. Taille du deal pas divulguée mais pourrait être transformateur.

2. [07 May 2026 21:45]
Microsoft capex guidance Q3 > +40% YoY. Toute la chaîne data center en profite — $VRT, $ETN, $EATON en surveillance. Aussi regarder les équipementiers refroidissement liquide.
`,
		},
	}

	manualPrompt, err := ai.BuildPromptFromContent(results, promptTemplate, ai.PromptContext{XGroups: mockXGroups})
	if err != nil {
		logger.Warn("failed to build manual prompt, continuing without it", "error", err)
	} else {
		logger.Info("manual prompt generated for copy-paste")
	}

	generator, err := report.NewGenerator(
		map[string]string{"Tech": "zap", "Finance": "shield", "Energy": "us", "Crypto": "bitcoin"},
		map[string]int{"Tech": 1, "Finance": 2, "Energy": 3, "Crypto": 4},
		map[string]string{
			"Tech":    "AI infrastructure buildout driving exceptional growth in data centers, semiconductors and cloud.",
			"Finance": "Rate environment stabilizing; big banks well-capitalized but NIM pressure persists.",
		},
		map[string]int{"Tech": 8, "Finance": 6},
	)
	if err != nil {
		return fmt.Errorf("creating report generator: %w", err)
	}

	htmlReport, err := generator.GenerateWithAI(results, nil, manualPrompt, nil, nil)
	if err != nil {
		return fmt.Errorf("generating mock report: %w", err)
	}

	if outputPath != "" {
		if err := os.WriteFile(outputPath, []byte(htmlReport), 0644); err != nil {
			return fmt.Errorf("writing mock report to file: %w", err)
		}
		logger.Info("mock report written", "path", outputPath)
	} else {
		fmt.Println(htmlReport)
	}

	if promptHTMLOutput != "" && manualPrompt != "" {
		if err := os.WriteFile(promptHTMLOutput, []byte(buildPromptHTML(manualPrompt)), 0644); err != nil {
			return fmt.Errorf("writing prompt HTML to file: %w", err)
		}
		logger.Info("prompt HTML written", "path", promptHTMLOutput)
	}

	return nil
}

func runSingleCheck(ctx context.Context, cfg *config.Config, ticker string, logger *slog.Logger) error {
	// Find the stock to check
	var stock models.Stock

	if ticker == "" {
		// Pick a random stock
		idx := rand.Intn(len(cfg.Stocks))
		stock = cfg.Stocks[idx]
	} else {
		// Find by ticker, or create a minimal stock entry if not in config
		for _, s := range cfg.Stocks {
			if s.Ticker == ticker {
				stock = s
				break
			}
		}
		if stock.Ticker == "" {
			stock = models.Stock{Ticker: ticker, Name: ticker, Category: "Unknown"}
		}
	}

	// Analyze the stock
	analyzer := yahoo.NewAnalyzer(cfg, logger)
	result := analyzer.AnalyzeStock(ctx, stock)

	if result.Error != nil {
		return fmt.Errorf("failed to analyze %s: %w", stock.Ticker, result.Error)
	}

	// Output to console
	printStockResult(result)
	return nil
}

func printStockResult(r *models.StockResult) {
	// Determine trend indicator
	var trend string
	if r.IsPositive() {
		trend = "\033[32m▲\033[0m" // Green up arrow
	} else if r.IsNegative() {
		trend = "\033[31m▼\033[0m" // Red down arrow
	} else {
		trend = "\033[33m●\033[0m" // Yellow dot
	}

	// Determine RSI status
	var rsiStatus string
	if r.IsOversold() {
		rsiStatus = " \033[31m[OVERSOLD]\033[0m"
	} else if r.IsOverbought() {
		rsiStatus = " \033[32m[OVERBOUGHT]\033[0m"
	}

	// Format change with color
	var changeStr string
	if r.ChangePercent >= 0 {
		changeStr = fmt.Sprintf("\033[32m+%.2f%%\033[0m", r.ChangePercent)
	} else {
		changeStr = fmt.Sprintf("\033[31m%.2f%%\033[0m", r.ChangePercent)
	}

	fmt.Println()
	fmt.Printf("  %s %s (%s)\n", trend, r.Stock.Name, r.Stock.Ticker)
	fmt.Printf("  %-12s %s\n", "Category:", r.Stock.Category)
	fmt.Printf("  %-12s %.2f\n", "Price:", r.CurrentPrice)
	fmt.Printf("  %-12s %s\n", "Change:", changeStr)
	fmt.Printf("  %-12s %.1f%s\n", "RSI:", r.RSI, rsiStatus)
	fmt.Println()
}

func runFullReport(ctx context.Context, cfg *config.Config, outputPath, promptOutput, promptHTMLOutput string, xGroups []ai.XGroupSection, logger *slog.Logger) error {
	logger.Info("starting stock analysis",
		"stocks", len(cfg.Stocks),
		"concurrency", cfg.Concurrency,
	)

	// Create analyzer and fetch stock data
	analyzer := yahoo.NewAnalyzer(cfg, logger)

	startTime := time.Now()
	results := analyzer.AnalyzeAll(ctx, cfg.Stocks)
	elapsed := time.Since(startTime)

	logger.Info("analysis complete",
		"successful", len(results),
		"failed", len(cfg.Stocks)-len(results),
		"duration", elapsed.Round(time.Millisecond),
	)

	// Pre-populate earnings dates from existing Gist data to avoid re-fetching
	existing := config.LoadFundamentals()
	now := time.Now()
	skipped := 0
	for _, r := range results {
		if f, ok := existing[r.Stock.Ticker]; ok && f.NextEarnings != "" {
			t, err := time.Parse(time.RFC3339, f.NextEarnings)
			if err == nil && t.After(now) {
				r.NextEarningsDate = &t
				skipped++
			}
		}
	}
	logger.Info("fetching earnings dates", "stocks", len(results)-skipped, "skipped", skipped)
	analyzer.FetchEarningsDates(ctx, results)
	logger.Info("earnings dates fetched")

	if len(results) == 0 {
		return fmt.Errorf("no stocks were successfully analyzed")
	}

	// Fetch VIX
	var vixData *report.VIXData
	yahooClient := yahoo.NewClient(cfg.YahooAPI)
	if vix, err := yahooClient.GetIntradayPrice(ctx, "^VIX"); err != nil {
		logger.Warn("failed to fetch VIX, continuing without it", "error", err)
	} else {
		vixData = report.NewVIXData(vix.CurrentPrice, vix.ChangePercent)
	}

	// Load prompt template from Gist
	promptTemplate, err := config.LoadPrompt()
	if err != nil {
		logger.Warn("failed to load prompt template, continuing without it", "error", err)
	}

	// Run AI analysis if enabled
	var aiAnalysis *ai.Analysis
	var manualPrompt string
	if cfg.AI.Enabled {
		if cfg.AI.Mode == "manual_prompt" {
			promptCtx := ai.PromptContext{XGroups: xGroups}
			if vixData != nil {
				promptCtx.VIXLine = fmt.Sprintf("- VIX: %s (%s) — %s\n", vixData.Price, vixData.Change, vixData.Level)
			}
			manualPrompt, err = ai.BuildPromptFromContent(results, promptTemplate, promptCtx)
			if err != nil {
				logger.Warn("failed to build manual prompt, continuing without it", "error", err)
			} else {
				logger.Info("manual prompt mode: prompt generated for copy-paste")
				if promptOutput != "" {
					if err := os.WriteFile(promptOutput, []byte(manualPrompt), 0644); err != nil {
						logger.Warn("failed to write prompt to file", "path", promptOutput, "error", err)
					} else {
						logger.Info("prompt written", "path", promptOutput)
					}
				}
				if promptHTMLOutput != "" {
					html := buildPromptHTML(manualPrompt)
					if err := os.WriteFile(promptHTMLOutput, []byte(html), 0644); err != nil {
						logger.Warn("failed to write prompt HTML to file", "path", promptHTMLOutput, "error", err)
					} else {
						logger.Info("prompt HTML written", "path", promptHTMLOutput)
					}
				}
			}
		} else {
			apiKey, provider := getAICredentials(cfg.AI.Provider)
			if apiKey != "" {
				logger.Info("running AI analysis", "provider", provider)

				aiClient := ai.NewClient(ai.ClientConfig{
					Provider: provider,
					APIKey:   apiKey,
					Model:    cfg.AI.Model,
				})
				aiAnalyzer := ai.NewAnalyzer(aiClient, promptTemplate)

				var err error
				aiAnalysis, err = aiAnalyzer.Analyze(ctx, results, ai.FormatXGroups(xGroups))
				if err != nil {
					logger.Warn("AI analysis failed, continuing without it", "error", err)
				} else {
					logger.Info("AI analysis complete")
				}
			} else {
				logger.Warn("AI analysis enabled but no API key found", "provider", cfg.AI.Provider)
			}
		}
	}

	// Fetch and persist macro events. Official sources cover critical dates like FOMC;
	// Yahoo remains a best-effort supplement.
	var officialEvents []macro.Event
	if events, err := macro.UpcomingOfficialEvents(ctx, nil, time.Now(), 21); err != nil {
		logger.Warn("failed to fetch official macro events, continuing with supplements", "error", err)
	} else {
		officialEvents = events
		logger.Info("official macro events fetched", "count", len(officialEvents))
	}

	var yahooEvents []macro.Event
	if events, err := yahooClient.GetEconomicEvents(ctx); err != nil {
		logger.Warn("failed to fetch yahoo economic events, continuing without yahoo supplement", "error", err)
	} else {
		for _, e := range events {
			yahooEvents = append(yahooEvents, macro.Event{
				Name:       e.Name,
				Date:       e.Date,
				Category:   "Economic",
				Source:     e.Source,
				Importance: "unknown",
			})
		}
		logger.Info("yahoo economic events fetched", "count", len(yahooEvents))
	}

	var cachedEvents []macro.Event
	if existing := config.LoadStockData(); existing != nil {
		cachedEvents = macro.Upcoming(existing.MacroEvents, time.Now(), 21)
	}
	macroEvents := macro.Merge(officialEvents, yahooEvents, cachedEvents)
	if len(macroEvents) == 0 {
		macroEvents = cachedEvents
		if len(macroEvents) > 0 {
			logger.Warn("using cached macro events because fresh fetch returned none", "count", len(macroEvents))
		}
	}
	var economicEvents []report.EconomicEventData
	for _, e := range macroEvents {
		economicEvents = append(economicEvents, report.EconomicEventData{
			Name: e.Name,
			Date: e.Date.Format("Mon 02 Jan, 15:04"),
		})
	}
	// Generate HTML report
	generator, err := report.NewGenerator(cfg.GetCategoryEmoji(), cfg.GetCategoryOrder(), cfg.GetCategoryNarrative(), cfg.GetCategoryNarrativeScore())
	if err != nil {
		return fmt.Errorf("creating report generator: %w", err)
	}

	htmlReport, err := generator.GenerateWithAI(results, aiAnalysis, manualPrompt, vixData, economicEvents)
	if err != nil {
		return fmt.Errorf("generating report: %w", err)
	}

	// Save fundamentals to Gist so stock-portfolio can read them without re-fetching
	signals := generator.ExtractSignals(results)
	if err := config.SaveFundamentals(results, signals, macroEvents); err != nil {
		logger.Warn("failed to save stock data to gist, continuing", "error", err)
	} else {
		logger.Info("stock data saved to gist", "macro_events", len(macroEvents))
	}

	// Output report
	if outputPath != "" {
		if err := os.WriteFile(outputPath, []byte(htmlReport), 0644); err != nil {
			return fmt.Errorf("writing report to file: %w", err)
		}
		logger.Info("report written", "path", outputPath)
	} else {
		fmt.Println(htmlReport)
	}

	return nil
}

// runAlerts checks intraday price variations against configured thresholds.
// It writes an HTML report to outputPath only when new (not yet sent today) alerts are triggered.
func runAlerts(ctx context.Context, cfg *config.Config, outputPath string, cryptoOnly bool, logger *slog.Logger) error {
	if !cfg.Alerts.Enabled {
		logger.Info("alerts disabled in config, skipping")
		return nil
	}

	statePath := cfg.Alerts.StatePath
	if statePath == "" {
		statePath = ".alert-state.json"
	}

	// Build lookup map and ticker list (override or all stocks)
	stockMap := make(map[string]models.Stock, len(cfg.Stocks))
	for _, s := range cfg.Stocks {
		stockMap[s.Ticker] = s
	}
	tickers := cfg.Alerts.Tickers
	if len(tickers) == 0 {
		tickers = make([]string, len(cfg.Stocks))
		for i, s := range cfg.Stocks {
			tickers[i] = s.Ticker
		}
	}

	// Filter to crypto-only when explicitly requested or on weekends
	isWeekend := time.Now().Weekday() == time.Saturday || time.Now().Weekday() == time.Sunday
	if cryptoOnly || isWeekend {
		filtered := tickers[:0]
		for _, t := range tickers {
			if s, ok := stockMap[t]; ok && s.Category == "Cryptos" {
				filtered = append(filtered, t)
			}
		}
		tickers = filtered
		if cryptoOnly {
			logger.Info("crypto-only mode: checking crypto assets only", "tickers", tickers)
		} else {
			logger.Info("weekend: checking crypto only", "tickers", tickers)
		}
	}

	// Fetch intraday prices concurrently
	client := yahoo.NewClient(cfg.YahooAPI)
	type result struct {
		price *yahoo.IntradayPrice
		err   error
	}
	ch := make(chan result, len(tickers))
	for _, ticker := range tickers {
		go func(t string) {
			p, err := client.GetIntradayPrice(ctx, t)
			ch <- result{p, err}
		}(ticker)
	}

	prices := make([]alerts.IntradayPrice, 0, len(tickers))
	for range tickers {
		r := <-ch
		if r.err != nil {
			logger.Warn("failed to fetch intraday price", "error", r.err)
			continue
		}
		if r.price.Stale {
			logger.Debug("skipping stale data (market not yet open)", "ticker", r.price.Ticker)
			continue
		}
		stock := stockMap[r.price.Ticker]
		prices = append(prices, alerts.IntradayPrice{
			Stock:         stock,
			OpenPrice:     r.price.OpenPrice,
			CurrentPrice:  r.price.CurrentPrice,
			ChangePercent: r.price.ChangePercent,
		})
	}

	// Load state and check for new alerts
	state, err := alerts.LoadState(statePath)
	if err != nil {
		return fmt.Errorf("loading alert state: %w", err)
	}

	triggered := alerts.Check(prices, cfg.Alerts.Thresholds, state)

	// Always save state (even if no new alerts, to persist MarkSent calls)
	if err := state.Save(statePath); err != nil {
		logger.Warn("failed to save alert state", "error", err)
	}

	if len(triggered) == 0 {
		logger.Info("no new price alerts triggered")
		return nil
	}

	logger.Info("price alerts triggered", "count", len(triggered))
	for _, a := range triggered {
		logger.Info("alert", "ticker", a.Stock.Ticker, "change", fmt.Sprintf("%.2f%%", a.ChangePercent), "threshold", a.Threshold)
	}

	// Write HTML report
	html := alerts.GenerateReport(triggered)
	if err := os.WriteFile(outputPath, []byte(html), 0644); err != nil {
		return fmt.Errorf("writing alerts report: %w", err)
	}
	logger.Info("alerts report written", "path", outputPath)
	return nil
}

// runDivergenceAlerts checks recent RSI bullish/bearish divergences using the
// shared stock-portfolio chart engine. It writes a separate HTML report only
// when a new divergence is detected for the current day.
func runDivergenceAlerts(ctx context.Context, cfg *config.Config, outputPath string, statePath string, logger *slog.Logger) error {
	if statePath == "" {
		statePath = ".divergence-state.json"
	}
	state, err := divergencealerts.LoadState(statePath)
	if err != nil {
		return fmt.Errorf("loading divergence state: %w", err)
	}

	client := divergencealerts.NewClient(cfg.YahooAPI)
	var triggered []divergencealerts.Alert
	for _, stock := range cfg.Stocks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		alertsForStock, err := client.Check(ctx, stock)
		if err != nil {
			logger.Warn("failed to check divergences", "ticker", stock.Ticker, "error", err)
			continue
		}
		for _, alert := range alertsForStock {
			key := divergencealerts.Key(alert.Stock.Ticker, alert.Divergence)
			if state.Has(key) {
				continue
			}
			state.Mark(key)
			triggered = append(triggered, alert)
		}
	}

	if err := state.Save(statePath); err != nil {
		logger.Warn("failed to save divergence state", "error", err)
	}

	if len(triggered) == 0 {
		logger.Info("no new RSI divergences triggered")
		return nil
	}

	logger.Info("RSI divergences triggered", "count", len(triggered))
	for _, alert := range triggered {
		logger.Info("divergence", "ticker", alert.Stock.Ticker, "kind", alert.Divergence.Kind, "from", alert.Divergence.FromTime, "to", alert.Divergence.ToTime)
	}

	html := divergencealerts.GenerateReport(triggered)
	if err := os.WriteFile(outputPath, []byte(html), 0644); err != nil {
		return fmt.Errorf("writing divergence report: %w", err)
	}
	logger.Info("divergence report written", "path", outputPath)
	return nil
}

// runTwitterPrompt fetches tweets from all xGroups and writes a standalone prompt to promptOutput (or stdout).
func runTwitterPrompt(ctx context.Context, cfg *config.Config, templatePath string, promptOutput string, logger *slog.Logger) error {
	groups := fetchAllXGroups(ctx, cfg, logger)
	if len(groups) == 0 {
		return fmt.Errorf("no tweets fetched — check xGroups in twitter config")
	}

	tmpl, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("reading twitter prompt template %q: %w", templatePath, err)
	}

	var sb strings.Builder
	sb.Write(tmpl)
	for _, g := range groups {
		sb.WriteString(fmt.Sprintf("\n\n## %s\n\n", g.Name))
		sb.WriteString(g.Content)
	}
	prompt := sb.String()

	if promptOutput != "" {
		if err := os.WriteFile(promptOutput, []byte(prompt), 0644); err != nil {
			return fmt.Errorf("writing twitter prompt to file: %w", err)
		}
		logger.Info("twitter prompt written", "path", promptOutput)
	} else {
		fmt.Println(prompt)
	}
	return nil
}

// fetchAllXGroups fetches tweets for every xGroup defined in the Twitter config.
func fetchAllXGroups(ctx context.Context, cfg *config.Config, logger *slog.Logger) []ai.XGroupSection {
	if !cfg.Twitter.Enabled {
		logger.Info("twitter disabled, skipping")
		return nil
	}
	if len(cfg.XGroups) == 0 {
		logger.Warn("twitter enabled but no xGroups configured")
		return nil
	}
	logger.Info("fetching tweets", "groups", len(cfg.XGroups))

	maxTweets := cfg.Twitter.MaxTweets
	if maxTweets <= 0 {
		maxTweets = 4
	}

	fetcher, err := twitter.NewFetcher(cfg.Twitter.Provider, os.Getenv("TWITTER_BEARER_TOKEN"), cfg.Twitter.NitterInstances)
	if err != nil {
		logger.Warn("twitter fetcher init failed, skipping", "error", err)
		return nil
	}

	delay := time.Duration(cfg.Twitter.RequestDelaySeconds) * time.Second
	if delay <= 0 {
		delay = 1 * time.Second
	}

	var result []ai.XGroupSection
	first := true
	for _, group := range cfg.XGroups {
		for _, account := range group.Accounts {
			account = strings.TrimSpace(account)
			if account == "" {
				continue
			}
			if !first {
				time.Sleep(delay)
			}
			first = false

			tweets, err := fetcher.GetRecentTweets(ctx, account, maxTweets)
			if err != nil {
				logger.Warn("failed to fetch tweets", "user", account, "error", err)
				continue
			}
			logger.Info("tweets fetched", "group", group.Name, "user", account, "total", len(tweets))
			tweets = twitter.FilterRecent(tweets)
			logger.Info("tweets after filter", "group", group.Name, "user", account, "recent", len(tweets))
			if content := twitter.FormatTweets(account, tweets); content != "" {
				result = append(result, ai.XGroupSection{Name: group.Name, Accounts: []string{account}, Content: content})
			}
		}
	}
	return result
}

// getAICredentials returns the API key and provider based on config and environment.
// It checks environment variables in order: configured provider first, then fallbacks.
func getAICredentials(configuredProvider string) (string, ai.Provider) {
	// Map of providers to their environment variable names
	providerEnvVars := map[ai.Provider]string{
		ai.ProviderGemini:    "GEMINI_API_KEY",
		ai.ProviderAnthropic: "ANTHROPIC_API_KEY",
	}

	// Try configured provider first
	provider := ai.Provider(configuredProvider)
	if envVar, ok := providerEnvVars[provider]; ok {
		if apiKey := os.Getenv(envVar); apiKey != "" {
			return apiKey, provider
		}
	}

	// Fallback: try all providers in order
	fallbackOrder := []ai.Provider{ai.ProviderGemini, ai.ProviderAnthropic}
	for _, p := range fallbackOrder {
		if apiKey := os.Getenv(providerEnvVars[p]); apiKey != "" {
			return apiKey, p
		}
	}

	return "", ai.Provider(configuredProvider)
}

type promptSection struct {
	title   string
	content string
}

func splitPromptSections(prompt string) []promptSection {
	lines := strings.Split(prompt, "\n")
	var sections []promptSection
	currentTitle := "Section 1 — Données & Instructions"
	var currentLines []string

	i := 0
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "════") {
			content := strings.TrimSpace(strings.Join(currentLines, "\n"))
			if content != "" {
				sections = append(sections, promptSection{title: currentTitle, content: content})
			}
			i++
			if i < len(lines) {
				currentTitle = strings.TrimSpace(lines[i])
			}
			i++ // skip closing ════ line
			currentLines = nil
		} else {
			currentLines = append(currentLines, line)
		}
		i++
	}
	if content := strings.TrimSpace(strings.Join(currentLines, "\n")); content != "" {
		sections = append(sections, promptSection{title: currentTitle, content: content})
	}
	return sections
}

func buildPromptHTML(prompt string) string {
	escaper := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	sections := splitPromptSections(prompt)

	var sb strings.Builder
	for i, s := range sections {
		id := fmt.Sprintf("sec%d", i)
		sb.WriteString(fmt.Sprintf(`<div class="section">
<div class="sec-hdr">
<span class="sec-title">%s</span>
</div>
<pre id="%s">%s</pre>
</div>
`, escaper.Replace(s.title), id, escaper.Replace(s.content)))
	}

	return `<!DOCTYPE html>
<html lang="fr">
<head>
<meta charset="UTF-8">
<style>
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Arial,sans-serif;background:#f6f8fa;padding:20px;margin:0}
.wrap{max-width:960px;margin:0 auto}
.hdr{background:linear-gradient(135deg,#6e40c9 0%,#9a6dd7 100%);color:#fff;padding:24px;text-align:center;border-radius:12px;margin-bottom:20px}
.hdr h2{font-size:20px;font-weight:700;margin:0 0 8px}
.hdr p{font-size:13px;opacity:.9;margin:0}
.section{background:#fff;border:1px solid #d0d7de;border-radius:12px;overflow:hidden;margin-bottom:16px}
.sec-hdr{padding:12px 16px;background:#f0eafa;border-bottom:1px solid #d0d7de}
.sec-title{font-size:13px;font-weight:700;color:#6e40c9;letter-spacing:.03em}
pre{background:#1e1e2e;color:#cdd6f4;font-family:'SF Mono',Monaco,'Inconsolata',monospace;font-size:12px;line-height:1.6;padding:20px;white-space:pre-wrap;word-break:break-word;margin:0}
</style>
</head>
<body>
<div class="wrap">
<div class="hdr">
<h2>Prompt pour analyse IA</h2>
<p>Copiez chaque section et collez-la dans un modèle IA.</p>
</div>
` + sb.String() + `</div>
</body>
</html>`
}

// Command snapshot produces a point-in-time snapshot of all T-Invest accounts
// as timestamped JSON and CSV files. See config.example.json and README.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dmitry/tinvest-snapshot/internal/config"
	"github.com/dmitry/tinvest-snapshot/internal/model"
	"github.com/dmitry/tinvest-snapshot/internal/report"
	"github.com/dmitry/tinvest-snapshot/internal/tinvest"
)

func main() {
	os.Exit(run())
}

func run() int {
	cfgPath := flag.String("config", "config.json", "path to the JSON config file")
	targetCurrency := flag.String("target-currency", "", "override target currency for converted totals (e.g. usd)")
	modeOverride := flag.String("mode", "", "override mode: prod or sandbox")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка конфигурации: %v\n", err)
		return 2
	}
	if *modeOverride != "" {
		cfg.Mode = *modeOverride
		if cfg.Mode != config.ModeProd && cfg.Mode != config.ModeSandbox {
			fmt.Fprintf(os.Stderr, "Ошибка: неизвестный режим %q\n", cfg.Mode)
			return 2
		}
	}
	target := cfg.TargetCurrency
	if *targetCurrency != "" {
		target = *targetCurrency
	}

	token, err := cfg.ResolveToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка: %v\n", err)
		return 2
	}

	logf := func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	client := tinvest.New(cfg.BaseURL(), token, cfg.AppName, cfg.Retries, time.Duration(cfg.RetryDelayMs)*time.Millisecond, logf)
	client.Sandbox = cfg.Sandbox()

	fmt.Printf("Режим: %s. Получение данных из T-Invest API...\n", cfg.Mode)
	now := time.Now()
	ctx := context.Background()
	snap, err := client.Collect(ctx, cfg.Mode, target, now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Не удалось получить снимок портфеля: %v\n", err)
		return 1
	}

	jsonPath, csvPath, err := report.Write(cfg.ReportsDir, now, snap)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Не удалось сохранить отчёт: %v\n", err)
		return 1
	}

	printSummary(snap, jsonPath, csvPath)
	return 0
}

func printSummary(snap *model.Snapshot, jsonPath, csvPath string) {
	fmt.Println()
	fmt.Println("=== Сводка ===")
	fmt.Printf("Счетов обработано: %d\n", len(snap.Accounts))
	fmt.Println("Суммарная стоимость:")
	for _, t := range snap.GrandTotals {
		fmt.Printf("  %s %s\n", t.Amount, upper(t.Currency))
	}
	if snap.GrandConverted != nil {
		fmt.Printf("  ≈ %s %s (пересчёт)\n", snap.GrandConverted.Amount, upper(snap.GrandConverted.Currency))
	}
	fmt.Printf("Файлы:\n  %s\n  %s\n", jsonPath, csvPath)
}

func upper(s string) string { return strings.ToUpper(s) }

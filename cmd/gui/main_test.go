//go:build !ci

package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/dmitry/tinvest-snapshot/internal/catalog"
	"github.com/dmitry/tinvest-snapshot/internal/config"
	"github.com/dmitry/tinvest-snapshot/internal/model"
)

func TestCopyableErrorMessagePreservesFullSelectableText(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	const message = "Первая строка\nВторая строка: подробности ошибки"
	view := copyableErrorMessage(message)
	scroll, ok := view.(*container.Scroll)
	if !ok {
		t.Fatalf("error view type = %T, want scroll container", view)
	}
	label, ok := scroll.Content.(*widget.Label)
	if !ok {
		t.Fatalf("scroll content type = %T, want label", scroll.Content)
	}
	if label.Text != message {
		t.Fatalf("message = %q, want %q", label.Text, message)
	}
	if !label.Selectable {
		t.Fatal("error message must be selectable")
	}
}

func TestShowErrorTextMatchesFyneErrorFormatting(t *testing.T) {
	// Fyne's standard error dialog capitalizes the first rune; the shared
	// copyable dialog retains that established presentation.
	err := errors.New("ошибка\nс подробностями")
	if got, want := errorDialogText(err), "Ошибка\nс подробностями"; got != want {
		t.Fatalf("error dialog text = %q, want %q", got, want)
	}
}

func TestInstrumentRowsShowBondSchedules(t *testing.T) {
	d := &desktop{cfg: &config.Config{Language: "ru"}}
	d.loadText()
	items := []catalog.Instrument{
		{Type: "bond", UID: "u1", Amortized: true,
			MaturityDate:      "2027-04-11T00:00:00Z",
			AmortizationDates: []string{"2026-06-25", "2026-12-22"},
			OfferDates:        []string{"2027-01-23"}, LastPrice: "98.7%"},
		{Type: "bond", UID: "u2"},
	}
	_, rows := instrumentRows(items, d)

	amortized, plain := rows[0], rows[1]
	if got := fieldOf(amortized, "maturity_date"); got != "2027-04-11" {
		t.Errorf("maturity date = %q, want the day only", got)
	}
	if got := fieldOf(amortized, "amortization"); got != d.tr("yes") {
		t.Errorf("amortization = %q, want yes", got)
	}
	if got := fieldOf(amortized, "amortization_dates"); got != "2026-06-25, 2026-12-22" {
		t.Errorf("amortization dates = %q", got)
	}
	if got := fieldOf(amortized, "offer_dates"); got != "2027-01-23" {
		t.Errorf("offer dates = %q", got)
	}
	if got := fieldOf(amortized, "last_price"); got != "98.7%" {
		t.Errorf("last price = %q", got)
	}

	if got := fieldOf(plain, "amortization"); got != d.tr("no") {
		t.Errorf("plain bond amortization = %q, want no", got)
	}
	for _, field := range []string{"amortization_dates", "offer_dates", "maturity_date"} {
		if got := fieldOf(plain, field); got != d.tr("na") {
			t.Errorf("plain bond %s = %q, want н/д", field, got)
		}
	}
}

// The save dialog must pre-fill the name the exporter used to generate itself.
func TestDefaultViewNameKeepsNamingRule(t *testing.T) {
	got := defaultViewName(time.Date(2026, 7, 14, 15, 4, 5, 0, time.UTC))
	if got != "table_20260714_150405.csv" {
		t.Errorf("defaultViewName = %q", got)
	}
}

func TestColumnHeadersAreLocalized(t *testing.T) {
	var fields []string
	fields = append(fields, portfolioFields...)
	fields = append(fields, operationFields...)
	fields = append(fields, instrumentFields...)
	for _, lang := range []string{"ru", "en"} {
		d := &desktop{cfg: &config.Config{Language: lang}}
		d.loadText()
		for _, f := range fields {
			key := "col_" + f
			if d.tr(key) == key {
				t.Errorf("%s: no translation for %s", lang, key)
			}
		}
	}
}

func TestTargetCurrencyRoundTrip(t *testing.T) {
	d := &desktop{cfg: &config.Config{Language: "ru"}}
	d.loadText()
	if got := targetCurrencyValue(d.targetCurrencyLabel(""), d); got != "" {
		t.Errorf("empty target currency must stay empty, got %q", got)
	}
	for _, code := range baseCurrencies {
		if got := targetCurrencyValue(d.targetCurrencyLabel(code), d); got != code {
			t.Errorf("round trip %q -> %q", code, got)
		}
	}
}

func TestCurrencyFilterTreatsAllAsNoFilter(t *testing.T) {
	d := &desktop{cfg: &config.Config{Language: "ru"}}
	d.loadText()
	for _, label := range []string{"", d.tr("all")} {
		if got := currencyFilter(label, d); got != "" {
			t.Errorf("currencyFilter(%q) = %q, want empty", label, got)
		}
	}
	if got := currencyFilter("USD", d); got != "usd" {
		t.Errorf(`currencyFilter("USD") = %q, want "usd"`, got)
	}
}

func TestInstrumentEnrichmentKeyDistinguishesCandidateSets(t *testing.T) {
	withDividends := true
	bonds := catalog.Filter{Type: "bond", Dividends: &withDividends}
	all := catalog.Filter{Dividends: &withDividends}
	if instrumentEnrichmentKey(bonds) == instrumentEnrichmentKey(all) {
		t.Fatal("different candidate sets must not share an enrichment key")
	}
	if instrumentEnrichmentKey(bonds) != instrumentEnrichmentKey(bonds) {
		t.Fatal("identical filter must retain a stable enrichment key")
	}
}

func TestBondFilterRequestsEnrichmentAfterReset(t *testing.T) {
	share := catalog.Filter{Type: "share"}
	bond := catalog.Filter{Type: "bond"}
	if needsInstrumentEnrichment(share) {
		t.Fatal("share-only catalog refresh must not enrich bonds")
	}
	if !needsInstrumentEnrichment(bond) {
		t.Fatal("returning to bonds after a non-bond refresh must enrich rate and coupon data")
	}
	maturity := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	if !needsInstrumentEnrichment(catalog.Filter{MaturityTo: &maturity}) {
		t.Fatal("a maturity range must enrich bonds even when instrument type is all")
	}
}

func TestCacheFreshHonorsTTLBoundary(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	if !cacheFresh(now.Add(-23*time.Hour), 24, now) {
		t.Fatal("cache one hour before TTL must be fresh")
	}
	if cacheFresh(now.Add(-24*time.Hour), 24, now) {
		t.Fatal("cache at TTL must be stale")
	}
}

func TestInstrumentRefreshTimeoutComesFromConfig(t *testing.T) {
	if refreshTimeout != 30*time.Second {
		t.Fatalf("ordinary refresh timeout = %s, want 30s", refreshTimeout)
	}
	absent := &config.Config{Mode: "sandbox", TokenEnv: "T"}
	if err := absent.Save(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatal(err)
	}
	d := &desktop{cfg: absent}
	if got := d.instrumentRefreshTimeout(); got != 600*time.Second {
		t.Fatalf("default instrument load timeout = %s, want 600s", got)
	}
	d.cfg.InstrumentLoadTimeoutSeconds = 600
	if got := d.instrumentRefreshTimeout(); got != 600*time.Second {
		t.Fatalf("configured instrument load timeout = %s, want 600s", got)
	}
}

func TestInstrumentLoadTimeoutSaveNormalizesInvalidInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, input := range []string{"", "abc", "0", "-1"} {
		c := config.Config{Mode: "sandbox", TokenEnv: "T", InstrumentLoadTimeoutSeconds: atoi(input)}
		if err := c.Save(path); err != nil {
			t.Fatalf("save %q: %v", input, err)
		}
		if c.InstrumentLoadTimeoutSeconds != 600 {
			t.Fatalf("input %q normalized to %d, want 600", input, c.InstrumentLoadTimeoutSeconds)
		}
		loaded, err := config.Load(path)
		if err != nil {
			t.Fatalf("load after %q: %v", input, err)
		}
		if loaded.InstrumentLoadTimeoutSeconds != 600 {
			t.Fatalf("reloaded %q value = %d, want 600", input, loaded.InstrumentLoadTimeoutSeconds)
		}
	}
	for _, keep := range []int{30, 600} {
		c := config.Config{Mode: "sandbox", TokenEnv: "T", InstrumentLoadTimeoutSeconds: keep}
		if err := c.Save(path); err != nil {
			t.Fatal(err)
		}
		loaded, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.InstrumentLoadTimeoutSeconds != keep {
			t.Fatalf("positive value %d changed to %d", keep, loaded.InstrumentLoadTimeoutSeconds)
		}
	}
}

func TestFreshInstrumentCacheUsesImmediateLocalPath(t *testing.T) {
	called := make(chan bool, 1)
	d := &desktop{
		cfg:   &config.Config{Mode: "sandbox", CatalogTTLHours: 24},
		scache: &catalog.SegmentedCache{Mode: "sandbox", Bond: &catalog.Segment{UpdatedAt: time.Now(), Instruments: []catalog.Instrument{{UID: "bond"}}}},
		loadInstruments: func(force bool) {
			called <- force
		},
	}
	started := time.Now()
	d.showInstruments()
	select {
	case force := <-called:
		if force {
			t.Fatal("fresh cache must not request a network refresh")
		}
	case <-time.After(time.Second):
		t.Fatal("fresh cache path waited instead of completing locally")
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("fresh cache path took %s", elapsed)
	}
}

func TestSnapshotCachesKeepPortfolioAndOperationRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operations.json")
	cache := snapshotCache{UpdatedAt: time.Now(), Mode: "sandbox", From: "2026-07-01T00:00:00Z", To: "2026-07-16T23:59:59Z", Snapshot: &model.Snapshot{Operations: []model.Operation{{ID: "op"}}}}
	if err := saveSnapshotCache(path, cache); err != nil {
		t.Fatal(err)
	}
	got, err := loadSnapshotCache(path)
	if err != nil || got.Mode != cache.Mode || got.From != cache.From || len(got.Snapshot.Operations) != 1 {
		t.Fatalf("cache round trip: %#v, %v", got, err)
	}
}

func TestOperationCacheRangeUsesLocalCalendarDate(t *testing.T) {
	loc := time.FixedZone("UTC+4", 4*60*60)
	now := time.Date(2026, 7, 16, 1, 0, 0, 0, loc)
	from, to := operationCacheRange("", "", now)
	if from != "2026-07-16" || to != "2026-07-16" {
		t.Fatalf("default range = %q..%q", from, to)
	}
	from, to = operationCacheRange("2026-07-01", "2026-07-16", now)
	if from != "2026-07-01" || to != "2026-07-16" {
		t.Fatalf("explicit range = %q..%q", from, to)
	}
}

func TestCatalogEnrichedRequiresEveryInstrument(t *testing.T) {
	if catalogEnriched([]catalog.Instrument{{Enriched: true}, {Enriched: false}}) {
		t.Fatal("partial catalog must not be published as complete")
	}
	if !catalogEnriched([]catalog.Instrument{{Enriched: true}}) {
		t.Fatal("complete catalog rejected")
	}
}

func TestEnrichmentCandidatesIgnoreRawMaturityRange(t *testing.T) {
	to := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	filter := catalog.Filter{Type: "bond", MaturityTo: &to}
	items := []catalog.Instrument{{Type: "bond", Ticker: "RU000A102LF6", MaturityDate: "2030-12-13"}}
	if got := catalog.Search(items, filter); len(got) != 0 {
		t.Fatalf("raw maturity filter unexpectedly retained %#v", got)
	}
	if got := catalog.Search(items, enrichmentFilter(filter)); len(got) != 1 || got[0].Ticker != "RU000A102LF6" {
		t.Fatalf("enrichment candidates = %#v, want the bond before effective maturity is calculated", got)
	}
}

func TestCurrencyOptionsCoverCatalogAndConfigured(t *testing.T) {
	scache := &catalog.SegmentedCache{Share: &catalog.Segment{Instruments: []catalog.Instrument{{Currency: "sek"}, {Currency: "usd"}, {Currency: ""}}}}
	options := currencyOptions(scache, "gel")
	for _, want := range []string{"RUB", "USD", "SEK", "GEL"} {
		if !slices.Contains(options, want) {
			t.Errorf("currencyOptions missing %s: %v", want, options)
		}
	}
	if slices.Contains(options, "") {
		t.Errorf("currencyOptions must not offer an empty code: %v", options)
	}
	if !slices.IsSorted(options) {
		t.Errorf("currencyOptions must be sorted: %v", options)
	}
}

func TestTypeAPIRoundTripsLocalizedLabels(t *testing.T) {
	for _, lang := range []string{"ru", "en"} {
		d := &desktop{cfg: &config.Config{Language: lang}}
		d.loadText()
		for _, raw := range instrumentTypes {
			label := d.tr("type_" + raw)
			if label == "type_"+raw {
				t.Errorf("%s: no translation for type_%s", lang, raw)
			}
			if got := typeAPI(label, d); got != raw {
				t.Errorf("%s: typeAPI(%q)=%q want %q", lang, label, got, raw)
			}
		}
	}
}

func TestPortfolioRowsIncludeCashAndTotals(t *testing.T) {
	snap := &model.Snapshot{Accounts: []model.Account{{ID: "a1", Name: "Broker", Positions: []model.Position{{Ticker: "SBER"}}, Cash: []model.CashBalance{{Currency: "rub", Amount: "42"}}, Total: model.Total{Currency: "rub", Amount: "100"}, TotalConverted: &model.Converted{Currency: "usd", Amount: "1", Rate: "0.01"}}}, GrandTotals: []model.Total{{Currency: "rub", Amount: "100"}}, GrandConverted: &model.Converted{Currency: "usd", Amount: "1", Rate: "0.01"}}
	cols, rows := portfolioRows(snap, func(k string) string { return k })
	if len(rows) != 5 {
		t.Fatalf("expected position, cash, account total and two grand totals; got %d", len(rows))
	}
	for i, row := range rows {
		if len(row) != len(cols) {
			t.Fatalf("row %d has %d columns, want %d", i, len(row), len(cols))
		}
	}
	want := []string{"position", "cash", "account_total", "grand_total", "grand_converted"}
	for i, v := range want {
		if rows[i][0] != v {
			t.Fatalf("row %d kind=%q want %q", i, rows[i][0], v)
		}
	}
	if rows[1][8] != "42" || rows[2][25] != "100" || rows[2][26] != "1" || rows[2][28] != "0.01" {
		t.Fatalf("cash/totals missing: %#v %#v", rows[1], rows[2])
	}
}

func TestTranslationFilesHaveSameKeys(t *testing.T) {
	load := func(name string) map[string]string {
		b, e := translations.ReadFile("i18n/" + name + ".json")
		if e != nil {
			t.Fatal(e)
		}
		var m map[string]string
		if e = json.Unmarshal(b, &m); e != nil {
			t.Fatal(e)
		}
		return m
	}
	ru, en := load("ru"), load("en")
	for k := range ru {
		if en[k] == "" {
			t.Errorf("missing en key %s", k)
		}
	}
	for k := range en {
		if ru[k] == "" {
			t.Errorf("missing ru key %s", k)
		}
	}
}

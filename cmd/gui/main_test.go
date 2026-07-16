//go:build !ci

package main

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/dmitry/tinvest-snapshot/internal/catalog"
	"github.com/dmitry/tinvest-snapshot/internal/config"
	"github.com/dmitry/tinvest-snapshot/internal/model"
)

func TestInstrumentRowsShowBondSchedules(t *testing.T) {
	d := &desktop{cfg: &config.Config{Language: "ru"}}
	d.loadText()
	items := []catalog.Instrument{
		{Type: "bond", UID: "u1", Amortized: true,
			MaturityDate:      "2027-04-11T00:00:00Z",
			AmortizationDates: []string{"2026-06-25", "2026-12-22"},
			OfferDates:        []string{"2027-01-23"}},
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

func TestCurrencyOptionsCoverCatalogAndConfigured(t *testing.T) {
	cache := &catalog.Cache{Instruments: []catalog.Instrument{{Currency: "sek"}, {Currency: "usd"}, {Currency: ""}}}
	options := currencyOptions(cache, "gel")
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

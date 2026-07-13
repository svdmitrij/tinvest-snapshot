package main

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/dmitry/tinvest-snapshot/internal/catalog"
	"github.com/dmitry/tinvest-snapshot/internal/config"
	"github.com/dmitry/tinvest-snapshot/internal/model"
)

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

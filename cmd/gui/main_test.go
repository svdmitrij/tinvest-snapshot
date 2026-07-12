package main

import (
	"encoding/json"
	"testing"

	"github.com/dmitry/tinvest-snapshot/internal/model"
)

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

package report

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dmitry/tinvest-snapshot/internal/model"
)

func sampleSnapshot() *model.Snapshot {
	bond := model.NewBondInfo()
	bond.CouponFrequency = "4"
	return &model.Snapshot{
		GeneratedAt: "2026-07-09T10:00:00Z",
		Mode:        "sandbox",
		Accounts: []model.Account{{
			ID: "acc1", Name: "Брокерский", Type: "broker",
			Positions: []model.Position{{
				InstrumentType: "bond", Ticker: "SU26240", ISIN: "RU000A101",
				Name: "ОФЗ", Currency: "rub", Quantity: "10",
				AvgPrice: "900", CurrentPrice: "950", CurrentValue: "9500",
				PnLAbs: "500", PnLPct: "5.55", Bond: bond,
			}},
			Cash:  []model.CashBalance{{Currency: "rub", Amount: "1000"}},
			Total: model.Total{Currency: "rub", Amount: "10500"},
		}},
		GrandTotals: []model.Total{{Currency: "rub", Amount: "10500"}},
	}
}

func TestWriteCreatesBothFiles(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	jp, cp, err := Write(dir, ts, sampleSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jp, "20260709_100000") || !strings.HasSuffix(jp, ".json") {
		t.Errorf("unexpected json path %q", jp)
	}
	if !strings.HasSuffix(cp, ".csv") {
		t.Errorf("unexpected csv path %q", cp)
	}

	var back model.Snapshot
	raw, _ := os.ReadFile(jp)
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("json invalid: %v", err)
	}
	if len(back.Accounts) != 1 || back.Accounts[0].Positions[0].Bond.CouponFrequency != "4" {
		t.Errorf("json round-trip mismatch: %+v", back)
	}

	f, _ := os.Open(cp)
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("csv invalid: %v", err)
	}
	if len(rows) != 5 { // header + position + cash + account_total + grand_total
		t.Errorf("csv rows = %d, want 5", len(rows))
	}
	if rows[1][15] != model.NA { // coupon_rate_pct defaults to NA
		t.Errorf("expected NA coupon rate, got %q", rows[1][15])
	}
}

func TestWriteDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	jp1, cp1, err := Write(dir, ts, sampleSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	jp2, cp2, err := Write(dir, ts, sampleSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if jp1 == jp2 || cp1 == cp2 {
		t.Errorf("second run overwrote files: %q/%q vs %q/%q", jp1, cp1, jp2, cp2)
	}
	for _, p := range []string{jp1, cp1, jp2, cp2} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file to exist: %q", p)
		}
	}
}

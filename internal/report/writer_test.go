package report

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dmitry/tinvest-snapshot/internal/model"
	"github.com/xuri/excelize/v2"
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
		GrandTotals:      []model.Total{{Currency: "rub", Amount: "10500"}},
		OperationsPeriod: &model.OperationsPeriod{From: "2026-01-01T00:00:00Z", To: "2026-07-09T10:00:00Z"},
		Operations: []model.Operation{{
			ID: "op1", AccountID: "acc1", AccountName: "Брокерский",
			DateTime: "2026-03-15T12:00:00Z", Type: "Покупка ЦБ",
			InstrumentType: "bond", Ticker: "SU26240", ISIN: "RU000A101", Name: "ОФЗ",
			Quantity: "10", PaymentAmount: "-9000", PaymentCurrency: "rub", State: "исполнена",
		}},
	}
}

func TestWriteCreatesFourFiles(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	paths, err := Write(dir, ts, sampleSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(paths.JSON, "20260709_100000") || !strings.HasSuffix(paths.JSON, ".json") {
		t.Errorf("unexpected json path %q", paths.JSON)
	}
	if !strings.HasPrefix(filepath.Base(paths.OperationsCSV), "operations_") || !strings.HasSuffix(paths.OperationsCSV, ".csv") {
		t.Errorf("unexpected operations csv path %q", paths.OperationsCSV)
	}
	if !strings.HasSuffix(paths.XLSX, ".xlsx") {
		t.Errorf("unexpected xlsx path %q", paths.XLSX)
	}
	for _, p := range paths.All() {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file to exist: %q", p)
		}
	}

	var back model.Snapshot
	raw, _ := os.ReadFile(paths.JSON)
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("json invalid: %v", err)
	}
	if len(back.Accounts) != 1 || back.Accounts[0].Positions[0].Bond.CouponFrequency != "4" {
		t.Errorf("json round-trip mismatch: %+v", back)
	}
	if back.OperationsPeriod == nil || len(back.Operations) != 1 || back.Operations[0].ID != "op1" {
		t.Errorf("json missing operations: %+v", back)
	}

	pf, _ := os.Open(paths.PortfolioCSV)
	defer pf.Close()
	rows, err := csv.NewReader(pf).ReadAll()
	if err != nil {
		t.Fatalf("portfolio csv invalid: %v", err)
	}
	if len(rows) != 5 { // header + position + cash + account_total + grand_total
		t.Errorf("portfolio csv rows = %d, want 5", len(rows))
	}
	if rows[1][15] != model.NA { // coupon_rate_pct defaults to NA
		t.Errorf("expected NA coupon rate, got %q", rows[1][15])
	}

	of, _ := os.Open(paths.OperationsCSV)
	defer of.Close()
	oprows, err := csv.NewReader(of).ReadAll()
	if err != nil {
		t.Fatalf("operations csv invalid: %v", err)
	}
	if len(oprows) != 2 { // header + one operation
		t.Fatalf("operations csv rows = %d, want 2", len(oprows))
	}
	if oprows[1][0] != "op1" || oprows[1][10] != "-9000" || oprows[1][12] != "исполнена" {
		t.Errorf("unexpected operation row: %v", oprows[1])
	}
}

// TestXLSXMatchesCSV verifies the two xlsx sheets equal the CSV files row for row.
func TestXLSXMatchesCSV(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	paths, err := Write(dir, ts, sampleSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenFile(paths.XLSX)
	if err != nil {
		t.Fatalf("open xlsx: %v", err)
	}
	defer f.Close()

	check := func(sheet, csvPath string) {
		want := readCSV(t, csvPath)
		got, err := f.GetRows(sheet)
		if err != nil {
			t.Fatalf("get rows %q: %v", sheet, err)
		}
		if len(got) != len(want) {
			t.Fatalf("sheet %q rows = %d, csv = %d", sheet, len(got), len(want))
		}
		// GetRows trims trailing empty cells; a missing cell reads as empty and
		// is equivalent to an empty CSV field.
		for i := range want {
			for j := range want[i] {
				if cell(got, i, j) != want[i][j] {
					t.Errorf("sheet %q cell [%d][%d] = %q, want %q", sheet, i, j, cell(got, i, j), want[i][j])
				}
			}
		}
	}
	check("Портфель", paths.PortfolioCSV)
	check("Операции", paths.OperationsCSV)
}

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func cell(rows [][]string, i, j int) string {
	if i < len(rows) && j < len(rows[i]) {
		return rows[i][j]
	}
	return ""
}

func TestWriteDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	p1, err := Write(dir, ts, sampleSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Write(dir, ts, sampleSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if p1.JSON == p2.JSON || p1.PortfolioCSV == p2.PortfolioCSV ||
		p1.OperationsCSV == p2.OperationsCSV || p1.XLSX == p2.XLSX {
		t.Errorf("second run overwrote files: %+v vs %+v", p1, p2)
	}
	for _, p := range append(p1.All(), p2.All()...) {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file to exist: %q", p)
		}
	}
}

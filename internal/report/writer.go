// Package report writes portfolio snapshots to timestamped JSON and CSV files.
package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dmitry/tinvest-snapshot/internal/model"
)

// Write serialises the snapshot to a JSON and a CSV file in dir, named with
// the run timestamp so prior runs are never overwritten. It returns the two
// paths. Files are written atomically (temp file + rename); on any error no
// partial output file is left behind.
func Write(dir string, ts time.Time, snap *model.Snapshot) (jsonPath, csvPath string, err error) {
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("create reports dir: %w", err)
	}
	stamp := ts.Format("20060102_150405")

	jsonPath = uniquePath(dir, "portfolio_"+stamp, ".json")
	if err = writeAtomic(jsonPath, func(f *os.File) error {
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(snap)
	}); err != nil {
		return "", "", fmt.Errorf("write JSON: %w", err)
	}

	csvPath = uniquePath(dir, "portfolio_"+stamp, ".csv")
	if err = writeAtomic(csvPath, func(f *os.File) error {
		return writeCSV(f, snap)
	}); err != nil {
		_ = os.Remove(jsonPath) // keep the pair consistent: no lone JSON on CSV failure
		return "", "", fmt.Errorf("write CSV: %w", err)
	}
	return jsonPath, csvPath, nil
}

// uniquePath returns dir/base+ext, appending _1, _2 … if the file exists.
func uniquePath(dir, base, ext string) string {
	p := filepath.Join(dir, base+ext)
	for i := 1; ; i++ {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s_%d%s", base, i, ext))
	}
}

// writeAtomic writes via a temp file in the same directory then renames it,
// removing the temp file if fn fails so no truncated output survives.
func writeAtomic(path string, fn func(*os.File) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := fn(tmp); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

var csvHeader = []string{
	"record_type", "account_id", "account_name", "account_type",
	"instrument_type", "ticker", "isin", "name", "currency",
	"quantity", "avg_price", "current_price", "current_value", "pnl_abs", "pnl_pct",
	"coupon_rate_pct", "current_yield", "ytm", "coupon_frequency",
	"next_coupon_date", "next_coupon_amount", "issuer_rating",
	"last_dividend_amount", "div_frequency", "next_div_date", "next_div_amount",
	"cash_currency", "cash_amount",
	"total_currency", "total_amount", "converted_currency", "converted_amount",
}

func writeCSV(f *os.File, snap *model.Snapshot) error {
	w := csv.NewWriter(f)
	if err := w.Write(csvHeader); err != nil {
		return err
	}
	for _, acc := range snap.Accounts {
		for _, p := range acc.Positions {
			if err := w.Write(positionRow(acc, p)); err != nil {
				return err
			}
		}
		for _, c := range acc.Cash {
			if err := w.Write(cashRow(acc, c)); err != nil {
				return err
			}
		}
		if err := w.Write(accountTotalRow(acc)); err != nil {
			return err
		}
	}
	if err := w.Write(grandTotalRows(snap)); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

func row() []string {
	r := make([]string, len(csvHeader))
	return r
}

func positionRow(acc model.Account, p model.Position) []string {
	r := row()
	r[0] = "position"
	r[1], r[2], r[3] = acc.ID, acc.Name, acc.Type
	r[4], r[5], r[6], r[7], r[8] = p.InstrumentType, p.Ticker, p.ISIN, p.Name, p.Currency
	r[9], r[10], r[11], r[12], r[13], r[14] = p.Quantity, p.AvgPrice, p.CurrentPrice, p.CurrentValue, p.PnLAbs, p.PnLPct
	if p.Bond != nil {
		r[15], r[16], r[17], r[18] = p.Bond.CouponRatePct, p.Bond.CurrentYield, p.Bond.YieldToMaturity, p.Bond.CouponFrequency
		r[19], r[20], r[21] = p.Bond.NextCouponDate, p.Bond.NextCouponAmount, p.Bond.IssuerRating
	}
	if p.Share != nil {
		r[22], r[23], r[24], r[25] = p.Share.LastDividendAmount, p.Share.Frequency, p.Share.NextPaymentDate, p.Share.NextPaymentAmount
	}
	return r
}

func cashRow(acc model.Account, c model.CashBalance) []string {
	r := row()
	r[0] = "cash"
	r[1], r[2], r[3] = acc.ID, acc.Name, acc.Type
	r[26], r[27] = c.Currency, c.Amount
	return r
}

func accountTotalRow(acc model.Account) []string {
	r := row()
	r[0] = "account_total"
	r[1], r[2], r[3] = acc.ID, acc.Name, acc.Type
	r[28], r[29] = acc.Total.Currency, acc.Total.Amount
	if acc.TotalConverted != nil {
		r[30], r[31] = acc.TotalConverted.Currency, acc.TotalConverted.Amount
	}
	return r
}

func grandTotalRows(snap *model.Snapshot) []string {
	// A single summary row; multiple currencies are joined in total_amount.
	r := row()
	r[0] = "grand_total"
	r[1] = "ALL"
	cur, amt := "", ""
	for i, t := range snap.GrandTotals {
		if i > 0 {
			cur += "; "
			amt += "; "
		}
		cur += t.Currency
		amt += t.Amount
	}
	r[28], r[29] = cur, amt
	if snap.GrandConverted != nil {
		r[30], r[31] = snap.GrandConverted.Currency, snap.GrandConverted.Amount
	}
	return r
}

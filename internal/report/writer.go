// Package report writes portfolio snapshots to timestamped JSON, CSV and XLSX
// files. Every run writes a fresh set of files and never overwrites prior runs.
package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dmitry/tinvest-snapshot/internal/model"
	"github.com/xuri/excelize/v2"
)

// Paths holds the four files produced by a single run.
type Paths struct {
	JSON          string
	PortfolioCSV  string
	OperationsCSV string
	XLSX          string
}

// All returns the paths in a stable order for reporting to the user.
func (p Paths) All() []string {
	return []string{p.JSON, p.PortfolioCSV, p.OperationsCSV, p.XLSX}
}

// Write serialises the snapshot to a JSON file, a portfolio CSV, an operations
// CSV and an XLSX workbook in dir, named with the run timestamp so prior runs
// are never overwritten. Each file is written atomically (temp file + rename);
// on any error every file produced by this run is removed so no partial output
// survives.
func Write(dir string, ts time.Time, snap *model.Snapshot) (Paths, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Paths{}, fmt.Errorf("create reports dir: %w", err)
	}
	stamp := ts.Format("20060102_150405")

	var written []string
	fail := func(err error) (Paths, error) {
		for _, p := range written {
			_ = os.Remove(p)
		}
		return Paths{}, err
	}

	var paths Paths

	paths.JSON = uniquePath(dir, "portfolio_"+stamp, ".json")
	if err := writeAtomic(paths.JSON, func(f *os.File) error {
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(snap)
	}); err != nil {
		return fail(fmt.Errorf("write JSON: %w", err))
	}
	written = append(written, paths.JSON)

	paths.PortfolioCSV = uniquePath(dir, "portfolio_"+stamp, ".csv")
	if err := writeAtomic(paths.PortfolioCSV, func(f *os.File) error {
		return writeMatrixCSV(f, portfolioMatrix(snap))
	}); err != nil {
		return fail(fmt.Errorf("write portfolio CSV: %w", err))
	}
	written = append(written, paths.PortfolioCSV)

	paths.OperationsCSV = uniquePath(dir, "operations_"+stamp, ".csv")
	if err := writeAtomic(paths.OperationsCSV, func(f *os.File) error {
		return writeMatrixCSV(f, operationsMatrix(snap))
	}); err != nil {
		return fail(fmt.Errorf("write operations CSV: %w", err))
	}
	written = append(written, paths.OperationsCSV)

	paths.XLSX = uniquePath(dir, "portfolio_"+stamp, ".xlsx")
	if err := writeXLSX(paths.XLSX, snap); err != nil {
		return fail(fmt.Errorf("write XLSX: %w", err))
	}
	written = append(written, paths.XLSX)

	return paths, nil
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

func writeMatrixCSV(f *os.File, matrix [][]string) error {
	w := csv.NewWriter(f)
	if err := w.WriteAll(matrix); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

// writeXLSX writes the portfolio and operations matrices to two sheets of one
// workbook. Cells are written as text so the sheets match the CSV files row for
// row. The file is written atomically.
func writeXLSX(path string, snap *model.Snapshot) error {
	f := excelize.NewFile()
	defer f.Close()

	const portfolioSheet = "Портфель"
	if err := f.SetSheetName("Sheet1", portfolioSheet); err != nil {
		return err
	}
	if err := writeSheet(f, portfolioSheet, portfolioMatrix(snap)); err != nil {
		return err
	}

	const operationsSheet = "Операции"
	if _, err := f.NewSheet(operationsSheet); err != nil {
		return err
	}
	if err := writeSheet(f, operationsSheet, operationsMatrix(snap)); err != nil {
		return err
	}

	return writeAtomic(path, func(file *os.File) error {
		return f.Write(file)
	})
}

func writeSheet(f *excelize.File, sheet string, matrix [][]string) error {
	for r, rowVals := range matrix {
		for cIdx, val := range rowVals {
			cell, err := excelize.CoordinatesToCellName(cIdx+1, r+1)
			if err != nil {
				return err
			}
			if err := f.SetCellStr(sheet, cell, val); err != nil {
				return err
			}
		}
	}
	return nil
}

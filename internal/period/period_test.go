package period

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var now = time.Date(2026, 7, 9, 15, 4, 5, 0, time.Local)

func TestResolveFromFlagTakesPriority(t *testing.T) {
	dir := t.TempDir()
	// A prior file exists, but the flag must win.
	touch(t, dir, "portfolio_20260601_120000.json")

	w, err := Resolve("2026-01-01", "", dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if w.GlobalFrom == nil {
		t.Fatal("expected GlobalFrom from flag")
	}
	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	if !w.GlobalFrom.Equal(want) {
		t.Errorf("from = %v, want %v", *w.GlobalFrom, want)
	}
	if !w.To.Equal(now) {
		t.Errorf("to = %v, want now %v", w.To, now)
	}
}

func TestResolveToEndOfDay(t *testing.T) {
	w, err := Resolve("2026-01-01", "2026-03-31", t.TempDir(), now)
	if err != nil {
		t.Fatal(err)
	}
	wantTo := time.Date(2026, 3, 31, 23, 59, 59, 0, time.Local)
	if !w.To.Equal(wantTo) {
		t.Errorf("to = %v, want %v", w.To, wantTo)
	}
}

func TestResolveAutodetectLatestStamp(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "portfolio_20260601_120000.json")
	touch(t, dir, "operations_20260705_093000.csv") // latest
	touch(t, dir, "portfolio_20260610_080000.xlsx")
	touch(t, dir, "unrelated.txt")

	w, err := Resolve("", "", dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if w.GlobalFrom == nil {
		t.Fatal("expected autodetected GlobalFrom")
	}
	want := time.Date(2026, 7, 5, 0, 0, 0, 0, time.Local) // 00:00 of latest date
	if !w.GlobalFrom.Equal(want) {
		t.Errorf("from = %v, want %v", *w.GlobalFrom, want)
	}
}

func TestResolveNoFlagNoFiles(t *testing.T) {
	w, err := Resolve("", "", t.TempDir(), now)
	if err != nil {
		t.Fatal(err)
	}
	if w.GlobalFrom != nil {
		t.Errorf("expected nil GlobalFrom (per-account), got %v", *w.GlobalFrom)
	}
}

func TestResolveInvalidDates(t *testing.T) {
	cases := []struct{ from, to string }{
		{"01.05.2026", ""},           // wrong format
		{"", "2026-13-01"},           // impossible month
		{"2026-05-01", "2026-01-01"}, // from after to
	}
	for _, c := range cases {
		if _, err := Resolve(c.from, c.to, t.TempDir(), now); err == nil {
			t.Errorf("Resolve(%q,%q) = nil error, want error", c.from, c.to)
		}
	}
}

func touch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

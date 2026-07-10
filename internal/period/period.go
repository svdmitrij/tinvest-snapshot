// Package period resolves the operations export window from the --from/--to
// flags and, when --from is absent, from the timestamps of prior report files.
package period

import (
	"fmt"
	"os"
	"regexp"
	"time"
)

// dateLayout is the accepted flag date format (ГГГГ-ММ-ДД).
const dateLayout = "2006-01-02"

// stampRe matches report file names carrying a run timestamp, e.g.
// portfolio_20260709_120000.json or operations_20260709_120000_1.csv.
var stampRe = regexp.MustCompile(`^(?:portfolio|operations)_(\d{8})_(\d{6})(?:_\d+)?\.[A-Za-z0-9]+$`)

// Window is the resolved export period. GlobalFrom is nil when the start bound
// is per-account (each account starts from its own opening date).
type Window struct {
	GlobalFrom *time.Time
	To         time.Time
}

// Resolve computes the export window. Flag dates are interpreted in local time
// (from = 00:00:00, to = 23:59:59). A malformed date or from later than to is
// a configuration error (the caller maps it to exit code 2), reported without
// contacting the API.
func Resolve(fromFlag, toFlag, reportsDir string, now time.Time) (Window, error) {
	var w Window

	if toFlag != "" {
		d, err := time.ParseInLocation(dateLayout, toFlag, time.Local)
		if err != nil {
			return Window{}, fmt.Errorf("неверный формат --to %q: ожидается ГГГГ-ММ-ДД", toFlag)
		}
		w.To = endOfDay(d)
	} else {
		w.To = now
	}

	switch {
	case fromFlag != "":
		d, err := time.ParseInLocation(dateLayout, fromFlag, time.Local)
		if err != nil {
			return Window{}, fmt.Errorf("неверный формат --from %q: ожидается ГГГГ-ММ-ДД", fromFlag)
		}
		f := startOfDay(d)
		w.GlobalFrom = &f
	default:
		if ts, ok := latestStamp(reportsDir); ok {
			f := startOfDay(ts)
			w.GlobalFrom = &f
		}
	}

	if w.GlobalFrom != nil && w.GlobalFrom.After(w.To) {
		return Window{}, fmt.Errorf("--from (%s) позже --to (%s)",
			w.GlobalFrom.Format(dateLayout), w.To.Format(dateLayout))
	}
	return w, nil
}

// latestStamp returns the most recent timestamp encoded in report file names
// in dir. A missing or empty directory yields ok=false.
func latestStamp(dir string) (time.Time, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, false
	}
	var latest time.Time
	found := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := stampRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		t, err := time.ParseInLocation("20060102_150405", m[1]+"_"+m[2], time.Local)
		if err != nil {
			continue
		}
		if !found || t.After(latest) {
			latest, found = t, true
		}
	}
	return latest, found
}

func startOfDay(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, d.Location())
}

func endOfDay(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 23, 59, 59, 0, d.Location())
}

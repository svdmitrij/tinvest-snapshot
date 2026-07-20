// Package catalog provides searchable, persistently cached instrument data.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Instrument struct {
	UID                     string   `json:"uid"`
	FIGI                    string   `json:"figi"`
	Type                    string   `json:"type"`
	Ticker                  string   `json:"ticker"`
	Name                    string   `json:"name"`
	ISIN                    string   `json:"isin"`
	Currency                string   `json:"currency"`
	Exchange                string   `json:"exchange"`
	Sector                  string   `json:"sector"`
	RiskLevel               string   `json:"risk_level"`
	CouponFrequency         int      `json:"coupon_frequency"`
	FloatingCoupon          bool     `json:"floating_coupon"`
	ForQualInvestor         *bool    `json:"for_qual_investor,omitempty"`
	CouponRatePct           *float64 `json:"coupon_rate_pct,omitempty"`
	NextCouponDate          string   `json:"next_coupon_date"`
	HasDividends            bool     `json:"has_dividends"`
	Enriched                bool     `json:"enriched,omitempty"`
	Nominal                 string   `json:"nominal,omitempty"`
	MaturityDate            string   `json:"maturity_date,omitempty"`
	ContractualMaturityDate string   `json:"contractual_maturity_date,omitempty"`
	LastPrice               string   `json:"last_price,omitempty"`

	// Amortized is known from the bond directory; the schedules below are only
	// filled once the instrument is enriched with its bond events.
	Amortized         bool     `json:"amortized,omitempty"`
	AmortizationDates []string `json:"amortization_dates,omitempty"`
	OfferDates        []string `json:"offer_dates,omitempty"`
}

// Segment holds one instrument type with its own freshness timestamp.
type Segment struct {
	UpdatedAt   time.Time    `json:"updated_at"`
	Instruments []Instrument `json:"instruments"`
}

// SegmentedCache is the on-disk per-type instrument cache.  Each segment
// (bond, share, etf, currency, future) is independently tracked and
// atomically replaceable.
type SegmentedCache struct {
	Mode     string   `json:"mode,omitempty"`
	Bond     *Segment `json:"bond,omitempty"`
	Share    *Segment `json:"share,omitempty"`
	Etf      *Segment `json:"etf,omitempty"`
	Currency *Segment `json:"currency,omitempty"`
	Future   *Segment `json:"future,omitempty"`
}

// segmentFor returns a pointer to the segment field for the given kind.
func (c *SegmentedCache) segmentFor(kind string) **Segment {
	switch kind {
	case "bond":
		return &c.Bond
	case "share":
		return &c.Share
	case "etf":
		return &c.Etf
	case "currency":
		return &c.Currency
	case "future":
		return &c.Future
	}
	return nil
}

// Segment returns the segment for a kind, or nil if not loaded.
func (c *SegmentedCache) Segment(kind string) *Segment {
	if s := c.segmentFor(kind); s != nil {
		return *s
	}
	return nil
}

// Set replaces (or creates) the segment for kind with the given instruments
// and timestamp.
func (c *SegmentedCache) Set(kind string, items []Instrument, updatedAt time.Time) {
	s := c.segmentFor(kind)
	if s == nil {
		return
	}
	*s = &Segment{UpdatedAt: updatedAt, Instruments: items}
}

// Fresh reports whether the given type has been loaded and is younger than ttl.
func (c *SegmentedCache) Fresh(kind string, ttl time.Duration, now time.Time) bool {
	s := c.Segment(kind)
	return s != nil && !s.UpdatedAt.IsZero() && now.Sub(s.UpdatedAt) < ttl
}

// AllInstruments returns the concatenation of all loaded segments.
func (c *SegmentedCache) AllInstruments() []Instrument {
	var all []Instrument
	for _, sp := range []*Segment{c.Bond, c.Share, c.Etf, c.Currency, c.Future} {
		if sp != nil {
			all = append(all, sp.Instruments...)
		}
	}
	return all
}

// LoadedTypes returns the kinds that have a non-nil segment.
func (c *SegmentedCache) LoadedTypes() []string {
	var kinds []string
	for _, t := range []string{"bond", "share", "etf", "currency", "future"} {
		if c.Segment(t) != nil {
			kinds = append(kinds, t)
		}
	}
	return kinds
}

// LoadSegmented reads a segmented cache from disk.  If the file is in the
// legacy flat format, it is migrated into a bond-only segment.
func LoadSegmented(path string) (*SegmentedCache, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var c SegmentedCache
	if e = json.Unmarshal(b, &c); e != nil {
		return nil, e
	}
	// Migration: legacy flat cache → bond segment.
	if c.Bond == nil && c.Share == nil && c.Etf == nil && c.Currency == nil && c.Future == nil {
		var legacy struct {
			UpdatedAt   time.Time    `json:"updated_at"`
			Mode        string       `json:"mode,omitempty"`
			Instruments []Instrument `json:"instruments"`
		}
		if e := json.Unmarshal(b, &legacy); e == nil && len(legacy.Instruments) > 0 {
			c.Mode = legacy.Mode
			c.Bond = &Segment{UpdatedAt: legacy.UpdatedAt, Instruments: legacy.Instruments}
		}
	}
	return &c, nil
}

// SaveSegmented writes the segmented cache atomically.
func (c *SegmentedCache) SaveSegmented(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	t, e := os.CreateTemp(filepath.Dir(path), ".catalog-*")
	if e != nil {
		return e
	}
	n := t.Name()
	defer os.Remove(n)
	if _, e = t.Write(b); e == nil {
		e = t.Close()
	} else {
		_ = t.Close()
	}
	if e != nil {
		return e
	}
	return os.Rename(n, path)
}

// Deprecated: Cache and its methods exist for backward compatibility with
// tests and non-GUI code.
type Cache struct {
	UpdatedAt   time.Time    `json:"updated_at"`
	Mode        string       `json:"mode,omitempty"`
	Instruments []Instrument `json:"instruments"`
}

func Load(path string) (*Cache, error) {
	sc, err := LoadSegmented(path)
	if err != nil {
		return nil, err
	}
	return &Cache{
		UpdatedAt:   time.Time{},
		Mode:        sc.Mode,
		Instruments: sc.AllInstruments(),
	}, nil
}
func (c *Cache) Fresh(ttl time.Duration, now time.Time) bool {
	return !c.UpdatedAt.IsZero() && now.Sub(c.UpdatedAt) < ttl
}
func (c *Cache) Save(path string) error {
	sc := &SegmentedCache{Mode: c.Mode}
	sc.Set("bond", c.Instruments, c.UpdatedAt)
	return sc.SaveSegmented(path)
}

type Filter struct {
	Type, Query, Currency, Exchange, Sector, Risk, CouponType string
	Frequency, CouponMonth                                    int
	RateFrom, RateTo                                          *float64
	MaturityFrom, MaturityTo                                  *time.Time
	Dividends                                                 *bool
	SortBy                                                    string
	Desc                                                      bool
}

func Search(all []Instrument, f Filter) []Instrument {
	q := strings.ToLower(strings.TrimSpace(f.Query))
	out := make([]Instrument, 0, len(all))
	for _, v := range all {
		if f.Type != "" && v.Type != f.Type || q != "" && !strings.Contains(strings.ToLower(v.Ticker+"\x00"+v.Name+"\x00"+v.ISIN), q) || f.Currency != "" && !strings.EqualFold(v.Currency, f.Currency) || f.Exchange != "" && !strings.EqualFold(v.Exchange, f.Exchange) || f.Sector != "" && !strings.EqualFold(v.Sector, f.Sector) || f.Risk != "" && v.RiskLevel != f.Risk || f.Frequency > 0 && v.CouponFrequency != f.Frequency || f.Dividends != nil && v.HasDividends != *f.Dividends {
			continue
		}
		if f.CouponType == "fixed" && v.FloatingCoupon || f.CouponType == "floating" && !v.FloatingCoupon {
			continue
		}
		if f.CouponMonth > 0 {
			d, e := time.Parse(time.RFC3339, v.NextCouponDate)
			if e != nil {
				d, e = time.Parse("2006-01-02", v.NextCouponDate)
			}
			if e != nil || int(d.Month()) != f.CouponMonth {
				continue
			}
		}
		if f.RateFrom != nil || f.RateTo != nil {
			if v.CouponRatePct == nil {
				continue
			}
			if f.RateFrom != nil && *v.CouponRatePct < *f.RateFrom || f.RateTo != nil && *v.CouponRatePct > *f.RateTo {
				continue
			}
		}
		if f.MaturityFrom != nil || f.MaturityTo != nil {
			maturity, err := parseDate(v.MaturityDate)
			if err != nil || f.MaturityFrom != nil && maturity.Before(*f.MaturityFrom) || f.MaturityTo != nil && maturity.After(*f.MaturityTo) {
				continue
			}
		}
		out = append(out, v)
	}
	key := func(v Instrument) string {
		switch f.SortBy {
		case "name":
			return v.Name
		case "currency":
			return v.Currency
		case "rate":
			if v.CouponRatePct != nil {
				return fmt.Sprintf("%020.8f", *v.CouponRatePct)
			}
			return ""
		default:
			return v.Ticker
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if f.Desc {
			return strings.ToLower(key(out[i])) > strings.ToLower(key(out[j]))
		}
		return strings.ToLower(key(out[i])) < strings.ToLower(key(out[j]))
	})
	return out
}

func parseDate(raw string) (time.Time, error) {
	if d, err := time.Parse(time.RFC3339, raw); err == nil {
		return d.UTC().Truncate(24 * time.Hour), nil
	}
	return time.Parse("2006-01-02", raw)
}

// Date parses a user-entered ISO calendar date. Invalid and empty values do
// not activate a filter, matching Float's behaviour for numeric filters.
func Date(s string) *time.Time {
	if s == "" {
		return nil
	}
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &d
}
func Float(s string) *float64 {
	if s == "" {
		return nil
	}
	v, e := strconv.ParseFloat(s, 64)
	if e != nil {
		return nil
	}
	return &v
}

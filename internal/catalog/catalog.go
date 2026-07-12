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
	UID             string   `json:"uid"`
	FIGI            string   `json:"figi"`
	Type            string   `json:"type"`
	Ticker          string   `json:"ticker"`
	Name            string   `json:"name"`
	ISIN            string   `json:"isin"`
	Currency        string   `json:"currency"`
	Exchange        string   `json:"exchange"`
	Sector          string   `json:"sector"`
	RiskLevel       string   `json:"risk_level"`
	CouponFrequency int      `json:"coupon_frequency"`
	FloatingCoupon  bool     `json:"floating_coupon"`
	CouponRatePct   *float64 `json:"coupon_rate_pct,omitempty"`
	NextCouponDate  string   `json:"next_coupon_date"`
	HasDividends    bool     `json:"has_dividends"`
	Enriched        bool     `json:"enriched,omitempty"`
	Nominal         string   `json:"nominal,omitempty"`
	MaturityDate    string   `json:"maturity_date,omitempty"`
}

type Cache struct {
	UpdatedAt   time.Time    `json:"updated_at"`
	Instruments []Instrument `json:"instruments"`
}

func Load(path string) (*Cache, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var c Cache
	if e = json.Unmarshal(b, &c); e != nil {
		return nil, e
	}
	return &c, nil
}
func (c *Cache) Fresh(ttl time.Duration, now time.Time) bool {
	return !c.UpdatedAt.IsZero() && now.Sub(c.UpdatedAt) < ttl
}
func (c *Cache) Save(path string) error {
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

type Filter struct {
	Type, Query, Currency, Exchange, Sector, Risk, CouponType string
	Frequency, CouponMonth                                    int
	RateFrom, RateTo                                          *float64
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
		less := strings.ToLower(key(out[i])) < strings.ToLower(key(out[j]))
		if f.Desc {
			return !less
		}
		return less
	})
	return out
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

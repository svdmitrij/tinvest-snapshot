package catalog

import (
	"testing"
	"time"
)

func TestSearchCombinesBondFilters(t *testing.T) {
	r12, r9 := 12.0, 9.0
	items := []Instrument{{Type: "bond", Ticker: "OK", Currency: "rub", RiskLevel: "LOW", CouponFrequency: 4, CouponRatePct: &r12}, {Type: "bond", Ticker: "FLOAT", Currency: "rub", RiskLevel: "LOW", CouponFrequency: 4, FloatingCoupon: true, CouponRatePct: &r12}, {Type: "bond", Ticker: "LOW", Currency: "rub", RiskLevel: "LOW", CouponFrequency: 4, CouponRatePct: &r9}, {Type: "bond", Ticker: "NA", Currency: "rub", RiskLevel: "LOW", CouponFrequency: 4}}
	from, to := 10.0, 15.0
	got := Search(items, Filter{Type: "bond", Currency: "rub", Risk: "LOW", Frequency: 4, CouponType: "fixed", RateFrom: &from, RateTo: &to})
	if len(got) != 1 || got[0].Ticker != "OK" {
		t.Fatalf("got %#v", got)
	}
}

func TestSearchTextCaseInsensitive(t *testing.T) {
	got := Search([]Instrument{{Ticker: "SBER", Name: "Сбербанк", ISIN: "RU1"}}, Filter{Query: "sbe"})
	if len(got) != 1 {
		t.Fatal(got)
	}
}

func TestCachePreservesAPIMode(t *testing.T) {
	cache := Cache{Mode: "sandbox"}
	if cache.Mode != "sandbox" {
		t.Fatal("catalog cache must retain API mode")
	}
}

func TestSegmentedCacheSetAndGet(t *testing.T) {
	sc := &SegmentedCache{Mode: "sandbox"}
	now := time.Now()
	bonds := []Instrument{{UID: "b1", Type: "bond", Ticker: "B1"}}
	shares := []Instrument{{UID: "s1", Type: "share", Ticker: "S1"}}

	sc.Set("bond", bonds, now)
	sc.Set("share", shares, now.Add(time.Minute))

	if s := sc.Segment("bond"); s == nil || len(s.Instruments) != 1 || s.Instruments[0].Ticker != "B1" {
		t.Fatal("bond segment missing or wrong")
	}
	if s := sc.Segment("share"); s == nil || len(s.Instruments) != 1 || s.Instruments[0].Ticker != "S1" {
		t.Fatal("share segment missing or wrong")
	}
	if sc.Segment("etf") != nil {
		t.Fatal("unloaded etf should be nil")
	}
}

func TestSegmentedCacheAllInstruments(t *testing.T) {
	sc := &SegmentedCache{Mode: "prod"}
	now := time.Now()
	sc.Set("bond", []Instrument{{UID: "b1", Type: "bond"}}, now)
	sc.Set("etf", []Instrument{{UID: "e1", Type: "etf"}, {UID: "e2", Type: "etf"}}, now)

	all := sc.AllInstruments()
	if len(all) != 3 {
		t.Fatalf("AllInstruments: got %d, want 3", len(all))
	}
}

func TestSegmentedCacheFresh(t *testing.T) {
	sc := &SegmentedCache{Mode: "sandbox"}
	now := time.Now()
	sc.Set("bond", []Instrument{{UID: "b1", Type: "bond"}}, now.Add(-10*time.Minute))

	if sc.Fresh("bond", 5*time.Minute, now) {
		t.Fatal("10-min-old bond should NOT be fresh for 5-min TTL")
	}
	if !sc.Fresh("bond", 15*time.Minute, now) {
		t.Fatal("10-min-old bond should be fresh for 15-min TTL")
	}
	if sc.Fresh("share", time.Hour, now) {
		t.Fatal("unloaded share should not be fresh")
	}
}

func TestSegmentedCacheLoadedTypes(t *testing.T) {
	sc := &SegmentedCache{Mode: "sandbox"}
	now := time.Now()
	sc.Set("bond", []Instrument{{UID: "b1", Type: "bond"}}, now)
	sc.Set("future", []Instrument{{UID: "f1", Type: "future"}}, now)

	types := sc.LoadedTypes()
	if len(types) != 2 {
		t.Fatalf("LoadedTypes: got %d, want 2", len(types))
	}
}

func TestSegmentedCacheSetReplaces(t *testing.T) {
	sc := &SegmentedCache{Mode: "sandbox"}
	now := time.Now()
	sc.Set("bond", []Instrument{{UID: "b1", Type: "bond", Ticker: "OLD"}}, now)
	sc.Set("bond", []Instrument{{UID: "b2", Type: "bond", Ticker: "NEW"}}, now.Add(time.Hour))

	s := sc.Segment("bond")
	if len(s.Instruments) != 1 || s.Instruments[0].Ticker != "NEW" {
		t.Fatal("replace failed")
	}
	if !s.UpdatedAt.Equal(now.Add(time.Hour)) {
		t.Fatal("timestamp not updated")
	}
	// Share must remain nil.
	if sc.Segment("share") != nil {
		t.Fatal("share should still be nil after bond replace")
	}
}

func TestSegmentedCacheRoundTrip(t *testing.T) {
	sc := &SegmentedCache{Mode: "prod"}
	now := time.Now().Truncate(time.Second)
	sc.Set("bond", []Instrument{{UID: "b1", Type: "bond", Ticker: "BOND1"}}, now)
	sc.Set("share", []Instrument{{UID: "s1", Type: "share", Ticker: "SHARE1"}}, now.Add(time.Minute))

	path := t.TempDir() + "/cache.json"
	if err := sc.SaveSegmented(path); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadSegmented(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mode != "prod" {
		t.Fatalf("mode: got %q, want prod", loaded.Mode)
	}
	if s := loaded.Segment("bond"); s == nil || len(s.Instruments) != 1 || s.Instruments[0].Ticker != "BOND1" {
		t.Fatal("bond round-trip failed")
	}
	if s := loaded.Segment("share"); s == nil || len(s.Instruments) != 1 || s.Instruments[0].Ticker != "SHARE1" {
		t.Fatal("share round-trip failed")
	}
}

func TestSegmentedCacheLegacyMigration(t *testing.T) {
	// Write old-format flat cache.
	old := Cache{
		UpdatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Mode:        "sandbox",
		Instruments: []Instrument{{UID: "b1", Type: "bond", Ticker: "LEGACY"}},
	}
	path := t.TempDir() + "/cache.json"
	if err := old.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSegmented(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mode != "sandbox" {
		t.Fatalf("mode: got %q", loaded.Mode)
	}
	s := loaded.Segment("bond")
	if s == nil || len(s.Instruments) != 1 || s.Instruments[0].Ticker != "LEGACY" {
		t.Fatal("legacy migration failed")
	}
	if !s.UpdatedAt.Equal(old.UpdatedAt) {
		t.Fatalf("legacy timestamp: got %v, want %v", s.UpdatedAt, old.UpdatedAt)
	}
}

func TestSearchFiltersMaturityRangeInclusively(t *testing.T) {
	from := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2027, 12, 31, 0, 0, 0, 0, time.UTC)
	items := []Instrument{
		{Ticker: "BEFORE", Type: "bond", MaturityDate: "2026-12-31"},
		{Ticker: "FROM", Type: "bond", MaturityDate: "2027-01-01T00:00:00Z"},
		{Ticker: "MIDDLE", Type: "bond", MaturityDate: "2027-06-01"},
		{Ticker: "TO", Type: "bond", MaturityDate: "2027-12-31"},
		{Ticker: "AFTER", Type: "bond", MaturityDate: "2028-01-01"},
		{Ticker: "UNKNOWN", Type: "bond"},
	}
	got := Search(items, Filter{Type: "bond", MaturityFrom: &from, MaturityTo: &to})
	if len(got) != 3 || got[0].Ticker != "FROM" || got[1].Ticker != "MIDDLE" || got[2].Ticker != "TO" {
		t.Fatalf("range result = %#v", got)
	}
	got = Search(items, Filter{Type: "bond", MaturityFrom: &from})
	if len(got) != 4 || got[0].Ticker != "AFTER" || got[1].Ticker != "FROM" || got[3].Ticker != "TO" {
		t.Fatalf("lower-bound result = %#v", got)
	}
	got = Search(items, Filter{Type: "bond"})
	if len(got) != 6 {
		t.Fatalf("unfiltered result = %#v", got)
	}
}

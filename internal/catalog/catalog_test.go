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

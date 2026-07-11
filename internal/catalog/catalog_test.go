package catalog

import "testing"

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

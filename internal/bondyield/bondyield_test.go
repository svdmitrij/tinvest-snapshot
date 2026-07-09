package bondyield

import (
	"math"
	"testing"
)

func TestCurrentYield(t *testing.T) {
	y, ok := CurrentYield(80, 950)
	if !ok {
		t.Fatal("expected ok")
	}
	// 80 / 950 * 100 = 8.421...
	if math.Abs(y-8.42105) > 1e-4 {
		t.Errorf("CurrentYield = %v, want ~8.42105", y)
	}
	if _, ok := CurrentYield(0, 950); ok {
		t.Error("expected not ok for zero coupon")
	}
	if _, ok := CurrentYield(80, 0); ok {
		t.Error("expected not ok for zero price")
	}
}

func TestYTMZeroCoupon(t *testing.T) {
	// Price 909.09 today, 1000 in one year => 10% yield.
	y, ok := YTM(909.0909, []CashFlow{{Years: 1, Amount: 1000}})
	if !ok {
		t.Fatal("expected ok")
	}
	if math.Abs(y-10) > 1e-2 {
		t.Errorf("YTM = %v, want ~10", y)
	}
}

func TestYTMParBondEqualsCouponRate(t *testing.T) {
	// Two annual 100 coupons + 1000 redemption, priced at par 1000 => YTM 10%.
	flows := []CashFlow{{Years: 1, Amount: 100}, {Years: 2, Amount: 1100}}
	y, ok := YTM(1000, flows)
	if !ok {
		t.Fatal("expected ok")
	}
	if math.Abs(y-10) > 1e-2 {
		t.Errorf("YTM = %v, want ~10", y)
	}
}

func TestYTMDiscountBondAboveCoupon(t *testing.T) {
	// Same coupons but bought at a discount (950) => YTM must exceed 10%.
	flows := []CashFlow{{Years: 1, Amount: 100}, {Years: 2, Amount: 1100}}
	y, ok := YTM(950, flows)
	if !ok || y <= 10 {
		t.Errorf("YTM = %v (ok=%v), want > 10 for a discounted bond", y, ok)
	}
}

func TestYTMRejectsBadInput(t *testing.T) {
	if _, ok := YTM(0, []CashFlow{{Years: 1, Amount: 1000}}); ok {
		t.Error("expected not ok for zero price")
	}
	if _, ok := YTM(1000, nil); ok {
		t.Error("expected not ok for no cashflows")
	}
}

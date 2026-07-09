// Package bondyield computes bond yields from raw T-Invest data
// (coupon schedule, price, nominal, accrued interest, maturity), since the
// API does not expose current yield or yield-to-maturity directly.
package bondyield

import "math"

// CashFlow is a single future payment: Years from the valuation date and the
// per-bond Amount (coupons plus the redemption at maturity), in the bond
// currency.
type CashFlow struct {
	Years  float64
	Amount float64
}

// CurrentYield = annual coupon income / current (clean) price, in percent.
// ok is false when inputs are non-positive (e.g. price or coupon unknown).
func CurrentYield(annualCoupon, cleanPrice float64) (float64, bool) {
	if annualCoupon <= 0 || cleanPrice <= 0 {
		return 0, false
	}
	return annualCoupon / cleanPrice * 100, true
}

// YTM returns the effective annual yield to maturity, in percent, solving
//
//	dirtyPrice = Σ CF_i / (1+y)^{t_i}
//
// by bisection. dirtyPrice is the clean price plus accrued interest (НКД).
// ok is false when the inputs cannot bracket a solution.
func YTM(dirtyPrice float64, flows []CashFlow) (float64, bool) {
	if dirtyPrice <= 0 || len(flows) == 0 {
		return 0, false
	}
	var total float64
	for _, f := range flows {
		if f.Years > 0 {
			total += f.Amount
		}
	}
	if total <= 0 {
		return 0, false
	}

	pv := func(y float64) float64 {
		var s float64
		for _, f := range flows {
			if f.Years <= 0 {
				continue
			}
			s += f.Amount / math.Pow(1+y, f.Years)
		}
		return s - dirtyPrice
	}

	lo, hi := -0.9, 10.0
	flo, fhi := pv(lo), pv(hi)
	if flo == 0 {
		return lo * 100, true
	}
	if fhi == 0 {
		return hi * 100, true
	}
	if flo*fhi > 0 {
		// No sign change in the search range: cannot solve reliably.
		return 0, false
	}
	for i := 0; i < 200; i++ {
		mid := (lo + hi) / 2
		fmid := pv(mid)
		if math.Abs(fmid) < 1e-9 || (hi-lo) < 1e-12 {
			return mid * 100, true
		}
		if flo*fmid < 0 {
			hi = mid
		} else {
			lo, flo = mid, fmid
		}
	}
	return (lo + hi) / 2 * 100, true
}

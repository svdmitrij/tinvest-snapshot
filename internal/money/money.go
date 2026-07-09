// Package money implements exact handling of T-Invest monetary values,
// which are transferred as an integer part (units) plus a nano fraction
// (nano, in 1e-9). Using units/nano avoids binary floating point rounding
// for storage and formatting; float64 is used only for derived ratios.
package money

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Quotation mirrors the T-Invest Quotation type: value = units + nano*1e-9.
type Quotation struct {
	Units int64 `json:"units,string"`
	Nano  int32 `json:"nano"`
}

// Money is a Quotation carrying an ISO currency code (lowercase, e.g. "rub").
type Money struct {
	Units    int64  `json:"units,string"`
	Nano     int32  `json:"nano"`
	Currency string `json:"currency"`
}

// Float returns the value as float64. Intended for derived ratios and
// cross-currency conversion, not for lossless storage.
func (q Quotation) Float() float64 { return float64(q.Units) + float64(q.Nano)/1e9 }

// String renders the value as a plain decimal string with no rounding.
func (q Quotation) String() string { return decimal(q.Units, q.Nano) }

// Float returns the monetary amount as float64.
func (m Money) Float() float64 { return float64(m.Units) + float64(m.Nano)/1e9 }

// String renders the amount as a plain decimal string (currency not included).
func (m Money) String() string { return decimal(m.Units, m.Nano) }

// Quotation drops the currency, keeping the numeric value.
func (m Money) Quotation() Quotation { return Quotation{Units: m.Units, Nano: m.Nano} }

// decimal formats a units/nano pair as a decimal string without loss.
// Per the T-Invest spec units and nano share the same sign.
func decimal(units int64, nano int32) string {
	if nano == 0 {
		return strconv.FormatInt(units, 10)
	}
	neg := units < 0 || nano < 0
	u := units
	if u < 0 {
		u = -u
	}
	n := nano
	if n < 0 {
		n = -n
	}
	frac := strings.TrimRight(fmt.Sprintf("%09d", n), "0")
	s := strconv.FormatInt(u, 10) + "." + frac
	if neg {
		s = "-" + s
	}
	return s
}

// FromFloat builds a Quotation from a float64, rounding to nano precision.
func FromFloat(v float64) Quotation {
	units := int64(v)
	nano := int32(math.Round((v - float64(units)) * 1e9))
	return Quotation{Units: units, Nano: nano}
}

// Round2 rounds a float64 to two decimal places (for converted totals).
func Round2(v float64) float64 { return math.Round(v*100) / 100 }

// Package model defines the portfolio snapshot structures shared by the
// collector and the report writers.
package model

// NA is the placeholder used whenever the T-Invest API does not provide a value.
const NA = "н/д"

// Snapshot is the full result of a single run.
type Snapshot struct {
	GeneratedAt    string     `json:"generated_at"`
	Mode           string     `json:"mode"`
	TargetCurrency string     `json:"target_currency,omitempty"`
	Accounts       []Account  `json:"accounts"`
	GrandTotals    []Total    `json:"grand_totals"`
	GrandConverted *Converted `json:"grand_total_converted,omitempty"`
}

// Account holds one brokerage/IIS account with its positions and cash.
type Account struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Type           string        `json:"type"`
	Positions      []Position    `json:"positions"`
	Cash           []CashBalance `json:"cash"`
	Total          Total         `json:"total"`
	TotalConverted *Converted    `json:"total_converted,omitempty"`
}

// Total is a monetary amount in a single currency (decimal string).
type Total struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

// Converted is a total additionally expressed in the target currency.
type Converted struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
	Rate     string `json:"rate,omitempty"`
}

// CashBalance is free cash on an account for one currency.
type CashBalance struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

// Position is a single instrument holding.
type Position struct {
	InstrumentType string     `json:"instrument_type"`
	Ticker         string     `json:"ticker"`
	ISIN           string     `json:"isin"`
	Name           string     `json:"name"`
	Currency       string     `json:"currency"`
	Quantity       string     `json:"quantity"`
	AvgPrice       string     `json:"avg_price"`
	CurrentPrice   string     `json:"current_price"`
	CurrentValue   string     `json:"current_value"`
	PnLAbs         string     `json:"pnl_abs"`
	PnLPct         string     `json:"pnl_pct"`
	Bond           *BondInfo  `json:"bond,omitempty"`
	Share          *ShareInfo `json:"share,omitempty"`
}

// BondInfo holds bond-specific fields (п.6). Missing values are NA.
type BondInfo struct {
	CouponRatePct    string `json:"coupon_rate_pct"`
	CurrentYield     string `json:"current_yield"`
	YieldToMaturity  string `json:"yield_to_maturity"`
	CouponFrequency  string `json:"coupon_frequency"`
	NextCouponDate   string `json:"next_coupon_date"`
	NextCouponAmount string `json:"next_coupon_amount"`
	IssuerRating     string `json:"issuer_rating"`
}

// NewBondInfo returns a BondInfo with every field initialised to NA.
func NewBondInfo() *BondInfo {
	return &BondInfo{NA, NA, NA, NA, NA, NA, NA}
}

// ShareInfo holds share-specific fields (п.7). Missing values are NA.
type ShareInfo struct {
	LastDividendAmount string `json:"last_dividend_amount"`
	Frequency          string `json:"frequency"`
	NextPaymentDate    string `json:"next_payment_date"`
	NextPaymentAmount  string `json:"next_payment_amount"`
}

// NewShareInfo returns a ShareInfo with every field initialised to NA.
func NewShareInfo() *ShareInfo {
	return &ShareInfo{NA, NA, NA, NA}
}

package report

import "github.com/dmitry/tinvest-snapshot/internal/model"

var portfolioHeader = []string{
	"record_type", "account_id", "account_name", "account_type",
	"instrument_type", "ticker", "isin", "name", "currency",
	"quantity", "avg_price", "current_price", "current_value", "pnl_abs", "pnl_pct",
	"coupon_rate_pct", "current_yield", "ytm", "coupon_frequency",
	"next_coupon_date", "next_coupon_amount", "issuer_rating",
	"last_dividend_amount", "div_frequency", "next_div_date", "next_div_amount",
	"cash_currency", "cash_amount",
	"total_currency", "total_amount", "converted_currency", "converted_amount",
}

var operationsHeader = []string{
	"operation_id", "account_id", "account_name", "datetime", "type",
	"instrument_type", "ticker", "isin", "name", "quantity",
	"payment_amount", "payment_currency", "state",
}

// portfolioMatrix returns the portfolio snapshot as a header row followed by
// one row per position, cash balance, account total and the grand total.
func portfolioMatrix(snap *model.Snapshot) [][]string {
	matrix := [][]string{portfolioHeader}
	for _, acc := range snap.Accounts {
		for _, p := range acc.Positions {
			matrix = append(matrix, positionRow(acc, p))
		}
		for _, c := range acc.Cash {
			matrix = append(matrix, cashRow(acc, c))
		}
		matrix = append(matrix, accountTotalRow(acc))
	}
	matrix = append(matrix, grandTotalRow(snap))
	return matrix
}

// operationsMatrix returns the operations as a header row followed by one row
// per operation.
func operationsMatrix(snap *model.Snapshot) [][]string {
	matrix := [][]string{operationsHeader}
	for _, op := range snap.Operations {
		matrix = append(matrix, operationRow(op))
	}
	return matrix
}

func portfolioRow() []string { return make([]string, len(portfolioHeader)) }

func positionRow(acc model.Account, p model.Position) []string {
	r := portfolioRow()
	r[0] = "position"
	r[1], r[2], r[3] = acc.ID, acc.Name, acc.Type
	r[4], r[5], r[6], r[7], r[8] = p.InstrumentType, p.Ticker, p.ISIN, p.Name, p.Currency
	r[9], r[10], r[11], r[12], r[13], r[14] = p.Quantity, p.AvgPrice, p.CurrentPrice, p.CurrentValue, p.PnLAbs, p.PnLPct
	if p.Bond != nil {
		r[15], r[16], r[17], r[18] = p.Bond.CouponRatePct, p.Bond.CurrentYield, p.Bond.YieldToMaturity, p.Bond.CouponFrequency
		r[19], r[20], r[21] = p.Bond.NextCouponDate, p.Bond.NextCouponAmount, p.Bond.IssuerRating
	}
	if p.Share != nil {
		r[22], r[23], r[24], r[25] = p.Share.LastDividendAmount, p.Share.Frequency, p.Share.NextPaymentDate, p.Share.NextPaymentAmount
	}
	return r
}

func cashRow(acc model.Account, c model.CashBalance) []string {
	r := portfolioRow()
	r[0] = "cash"
	r[1], r[2], r[3] = acc.ID, acc.Name, acc.Type
	r[26], r[27] = c.Currency, c.Amount
	return r
}

func accountTotalRow(acc model.Account) []string {
	r := portfolioRow()
	r[0] = "account_total"
	r[1], r[2], r[3] = acc.ID, acc.Name, acc.Type
	r[28], r[29] = acc.Total.Currency, acc.Total.Amount
	if acc.TotalConverted != nil {
		r[30], r[31] = acc.TotalConverted.Currency, acc.TotalConverted.Amount
	}
	return r
}

func grandTotalRow(snap *model.Snapshot) []string {
	// A single summary row; multiple currencies are joined in total_amount.
	r := portfolioRow()
	r[0] = "grand_total"
	r[1] = "ALL"
	cur, amt := "", ""
	for i, t := range snap.GrandTotals {
		if i > 0 {
			cur += "; "
			amt += "; "
		}
		cur += t.Currency
		amt += t.Amount
	}
	r[28], r[29] = cur, amt
	if snap.GrandConverted != nil {
		r[30], r[31] = snap.GrandConverted.Currency, snap.GrandConverted.Amount
	}
	return r
}

func operationRow(op model.Operation) []string {
	return []string{
		op.ID, op.AccountID, op.AccountName, op.DateTime, op.Type,
		op.InstrumentType, op.Ticker, op.ISIN, op.Name, op.Quantity,
		op.PaymentAmount, op.PaymentCurrency, op.State,
	}
}

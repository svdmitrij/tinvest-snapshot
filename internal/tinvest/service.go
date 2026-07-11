package tinvest

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/dmitry/tinvest-snapshot/internal/catalog"
)

// Catalog downloads the five read-only instrument directories. Bond coupon
// dates/rates and share dividends are enriched with bounded concurrent calls.
func (c *Client) Catalog(ctx context.Context, now time.Time) ([]catalog.Instrument, error) {
	types := []struct{ method, kind string }{{"Shares", "share"}, {"Bonds", "bond"}, {"Etfs", "etf"}, {"Currencies", "currency"}, {"Futures", "future"}}
	var out []catalog.Instrument
	for _, typ := range types {
		var resp instrumentsResponse
		if err := c.call(ctx, "InstrumentsService", typ.method, map[string]string{"instrumentStatus": "INSTRUMENT_STATUS_BASE"}, &resp); err != nil {
			return nil, err
		}
		for _, v := range resp.Instruments {
			x := catalog.Instrument{UID: v.UID, FIGI: v.Figi, Type: typ.kind, Ticker: v.Ticker, Name: v.Name, ISIN: v.ISIN, Currency: v.Currency, Exchange: v.Exchange, Sector: v.Sector, RiskLevel: v.RiskLevel, CouponFrequency: v.CouponQuantityPerYear, FloatingCoupon: v.FloatingCouponFlag, MaturityDate: v.MaturityDate}
			if v.Nominal.Currency != "" {
				x.Nominal = v.Nominal.String() + " " + v.Nominal.Currency
			}
			out = append(out, x)
		}
	}
	sem := make(chan struct{}, 8)
	done := make(chan struct{}, len(out))
	count := 0
	for i := range out {
		if out[i].Type != "bond" && out[i].Type != "share" {
			continue
		}
		count++
		go func(i int) {
			sem <- struct{}{}
			defer func() { <-sem; done <- struct{}{} }()
			if out[i].Type == "bond" {
				ev, e := c.Coupons(ctx, out[i].UID, now.AddDate(-1, 0, 0), now.AddDate(2, 0, 0))
				if e == nil {
					if n, ok := nextCoupon(ev, now); ok {
						out[i].NextCouponDate = n.CouponDate
						if out[i].CouponFrequency > 0 {
							nom := parseAmount(out[i].Nominal)
							if nom > 0 {
								r := n.PayOneBond.Float() * float64(out[i].CouponFrequency) / nom * 100
								out[i].CouponRatePct = &r
							}
						}
					}
				}
			} else {
				d, e := c.Dividends(ctx, out[i].UID, now.AddDate(-1, 0, 0), now)
				out[i].HasDividends = e == nil && len(d) > 0
			}
		}(i)
	}
	for i := 0; i < count; i++ {
		<-done
	}
	return out, nil
}

func parseAmount(s string) float64 {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return v
}

// Accounts returns all user accounts (sandbox or production).
func (c *Client) Accounts(ctx context.Context) ([]apiAccount, error) {
	var resp getAccountsResponse
	if c.Sandbox {
		if err := c.call(ctx, "SandboxService", "GetSandboxAccounts", struct{}{}, &resp); err != nil {
			return nil, err
		}
		return resp.Accounts, nil
	}
	if err := c.call(ctx, "UsersService", "GetAccounts", struct{}{}, &resp); err != nil {
		return nil, err
	}
	return resp.Accounts, nil
}

// Portfolio returns the portfolio for one account.
func (c *Client) Portfolio(ctx context.Context, accountID string) (*portfolioResponse, error) {
	req := portfolioRequest{AccountID: accountID}
	var resp portfolioResponse
	service, method := "OperationsService", "GetPortfolio"
	if c.Sandbox {
		service, method = "SandboxService", "GetSandboxPortfolio"
	}
	if err := c.call(ctx, service, method, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// InstrumentByUID returns reference data (ticker/name/isin/currency) for a uid.
func (c *Client) InstrumentByUID(ctx context.Context, uid string) (*instrumentShort, error) {
	req := instrumentRequest{IDType: "INSTRUMENT_ID_TYPE_UID", ID: uid}
	var resp getInstrumentResponse
	if err := c.call(ctx, "InstrumentsService", "GetInstrumentBy", req, &resp); err != nil {
		return nil, err
	}
	return &resp.Instrument, nil
}

// BondByUID returns bond-specific reference data.
func (c *Client) BondByUID(ctx context.Context, uid string) (*bond, error) {
	req := instrumentRequest{IDType: "INSTRUMENT_ID_TYPE_UID", ID: uid}
	var resp bondResponse
	if err := c.call(ctx, "InstrumentsService", "BondBy", req, &resp); err != nil {
		return nil, err
	}
	return &resp.Instrument, nil
}

// Coupons returns coupon events for a bond within [from, to].
func (c *Client) Coupons(ctx context.Context, instrumentID string, from, to time.Time) ([]couponEvent, error) {
	req := couponsRequest{InstrumentID: instrumentID, From: rfc3339(from), To: rfc3339(to)}
	var resp couponsResponse
	if err := c.call(ctx, "InstrumentsService", "GetBondCoupons", req, &resp); err != nil {
		return nil, err
	}
	return resp.Events, nil
}

// Dividends returns dividend events for a share within [from, to].
func (c *Client) Dividends(ctx context.Context, instrumentID string, from, to time.Time) ([]dividend, error) {
	req := dividendsRequest{InstrumentID: instrumentID, From: rfc3339(from), To: rfc3339(to)}
	var resp dividendsResponse
	if err := c.call(ctx, "InstrumentsService", "GetDividends", req, &resp); err != nil {
		return nil, err
	}
	return resp.Dividends, nil
}

// Currencies returns all currency instruments (for FX conversion).
func (c *Client) Currencies(ctx context.Context) ([]currency, error) {
	req := map[string]string{"instrumentStatus": "INSTRUMENT_STATUS_BASE"}
	var resp currenciesResponse
	if err := c.call(ctx, "InstrumentsService", "Currencies", req, &resp); err != nil {
		return nil, err
	}
	return resp.Instruments, nil
}

// LastPrices returns last prices for the given instrument uids.
func (c *Client) LastPrices(ctx context.Context, uids []string) ([]lastPrice, error) {
	req := lastPricesRequest{InstrumentID: uids}
	var resp lastPricesResponse
	if err := c.call(ctx, "MarketDataService", "GetLastPrices", req, &resp); err != nil {
		return nil, err
	}
	return resp.LastPrices, nil
}

// Operations returns all operations on an account within [from, to],
// following the API's cursor pagination so multi-year history is complete.
func (c *Client) Operations(ctx context.Context, accountID string, from, to time.Time) ([]operationItem, error) {
	var all []operationItem
	cursor := ""
	for {
		req := operationsByCursorRequest{
			AccountID: accountID,
			From:      rfc3339(from),
			To:        rfc3339(to),
			Cursor:    cursor,
			Limit:     1000,
		}
		var resp operationsByCursorResponse
		if err := c.call(ctx, "OperationsService", "GetOperationsByCursor", req, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Items...)
		if !resp.HasNext || resp.NextCursor == "" || resp.NextCursor == cursor {
			return all, nil
		}
		cursor = resp.NextCursor
	}
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

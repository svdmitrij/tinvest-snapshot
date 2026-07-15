package tinvest

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dmitry/tinvest-snapshot/internal/catalog"
)

// Catalog downloads the five read-only instrument directories. Expensive
// coupon/dividend enrichment is deferred until local filters narrow the set.
func (c *Client) Catalog(ctx context.Context, now time.Time) ([]catalog.Instrument, error) {
	types := []struct{ method, kind string }{{"Shares", "share"}, {"Bonds", "bond"}, {"Etfs", "etf"}, {"Currencies", "currency"}, {"Futures", "future"}}
	var out []catalog.Instrument
	for _, typ := range types {
		var resp instrumentsResponse
		if err := c.call(ctx, "InstrumentsService", typ.method, map[string]string{"instrumentStatus": "INSTRUMENT_STATUS_BASE"}, &resp); err != nil {
			return nil, err
		}
		for _, v := range resp.Instruments {
			x := catalog.Instrument{UID: v.UID, FIGI: v.Figi, Type: typ.kind, Ticker: v.Ticker, Name: v.Name, ISIN: v.ISIN, Currency: v.Currency, Exchange: v.Exchange, Sector: v.Sector, RiskLevel: v.RiskLevel, CouponFrequency: v.CouponQuantityPerYear, FloatingCoupon: v.FloatingCouponFlag, Amortized: v.AmortizationFlag, MaturityDate: v.MaturityDate}
			if v.Nominal.Currency != "" {
				x.Nominal = v.Nominal.String() + " " + v.Nominal.Currency
			}
			out = append(out, x)
		}
	}
	return out, nil
}

// EnrichCatalog fills coupon and dividend fields for a locally narrowed set.
func (c *Client) EnrichCatalog(ctx context.Context, items []catalog.Instrument, now time.Time) []catalog.Instrument {
	out := append([]catalog.Instrument(nil), items...)
	sem := make(chan struct{}, 6)
	done := make(chan struct{}, len(out))
	for i := range out {
		go func(i int) {
			sem <- struct{}{}
			defer func() { <-sem; done <- struct{}{} }()
			if out[i].Enriched {
				return
			}
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
				// A bond may amortize over decades: ask for the whole life span,
				// not the default nearest-period window.
				if events, e := c.BondEvents(ctx, out[i].UID, now.AddDate(-10, 0, 0), now.AddDate(30, 0, 0)); e == nil {
					out[i].AmortizationDates, out[i].OfferDates = redemptionSchedule(events)
				}
			} else if out[i].Type == "share" {
				d, e := c.Dividends(ctx, out[i].UID, now.AddDate(-1, 0, 0), now)
				out[i].HasDividends = e == nil && len(d) > 0
			}
			out[i].Enriched = true
		}(i)
	}
	for i := 0; i < len(out); i++ {
		<-done
	}
	return out
}

// redemptionSchedule splits bond events into the amortization schedule and the
// call (оферта) dates. The API has no amortization event type: an amortized
// bond repays its nominal through several EVENT_TYPE_MTY events, so more than
// one of them *is* the schedule, while a single one is just the final maturity.
func redemptionSchedule(events []bondEvent) (amortization, offers []string) {
	var redemptions []string
	for _, e := range events {
		date := eventDay(e)
		if date == "" {
			continue
		}
		switch e.EventType {
		case "EVENT_TYPE_MTY":
			redemptions = append(redemptions, date)
		case "EVENT_TYPE_CALL":
			offers = append(offers, date)
		}
	}
	sort.Strings(redemptions)
	sort.Strings(offers)
	if len(redemptions) > 1 {
		amortization = redemptions
	}
	return amortization, offers
}

func eventDay(e bondEvent) string {
	raw := e.PayDate
	if raw == "" {
		raw = e.EventDate
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Format("2006-01-02")
	}
	if len(raw) >= 10 {
		return raw[:10]
	}
	return ""
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

// InstrumentByUIDCached is like InstrumentByUID but uses an in-memory cache
// with double-checked locking to avoid cache stampede.
func (c *Client) InstrumentByUIDCached(ctx context.Context, uid string) (*instrumentShort, error) {
	c.instrCacheMu.RLock()
	if v, ok := c.instrShortCache[uid]; ok {
		c.instrCacheMu.RUnlock()
		return v, nil
	}
	c.instrCacheMu.RUnlock()
	c.instrCacheMu.Lock()
	// Double-check: another goroutine may have filled the cache while we waited.
	if v, ok := c.instrShortCache[uid]; ok {
		c.instrCacheMu.Unlock()
		return v, nil
	}
	c.instrCacheMu.Unlock()
	v, err := c.InstrumentByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	c.instrCacheMu.Lock()
	c.instrShortCache[uid] = v
	c.instrCacheMu.Unlock()
	return v, nil
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

// BondByUIDCached is like BondByUID but uses an in-memory cache
// with double-checked locking to avoid cache stampede.
func (c *Client) BondByUIDCached(ctx context.Context, uid string) (*bond, error) {
	c.bondCacheMu.RLock()
	if v, ok := c.bondShortCache[uid]; ok {
		c.bondCacheMu.RUnlock()
		return v, nil
	}
	c.bondCacheMu.RUnlock()
	c.bondCacheMu.Lock()
	// Double-check: another goroutine may have filled the cache while we waited.
	if v, ok := c.bondShortCache[uid]; ok {
		c.bondCacheMu.Unlock()
		return v, nil
	}
	c.bondCacheMu.Unlock()
	v, err := c.BondByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	c.bondCacheMu.Lock()
	c.bondShortCache[uid] = v
	c.bondCacheMu.Unlock()
	return v, nil
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

// BondEvents returns the bond's lifecycle events (coupons, calls, redemptions)
// within [from, to].
func (c *Client) BondEvents(ctx context.Context, instrumentID string, from, to time.Time) ([]bondEvent, error) {
	req := bondEventsRequest{InstrumentID: instrumentID, From: rfc3339(from), To: rfc3339(to)}
	var resp bondEventsResponse
	if err := c.call(ctx, "InstrumentsService", "GetBondEvents", req, &resp); err != nil {
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

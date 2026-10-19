package tinvest

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dmitry/tinvest-snapshot/internal/catalog"
)

var catalogMethods = map[string]string{"share": "Shares", "bond": "Bonds", "etf": "Etfs", "currency": "Currencies", "future": "Futures"}

var allCatalogTypes = []string{"share", "bond", "etf", "currency", "future"}

// Catalog downloads all five instrument directories, calling receive for each
// type independently.  Callers apply atomic per-type merging with independent
// timeouts.
func (c *Client) Catalog(ctx context.Context, now time.Time, receive func(typ string, items []catalog.Instrument, err error)) {
	var wg sync.WaitGroup
	for _, typ := range allCatalogTypes {
		wg.Add(1)
		go func(typ string) {
			defer wg.Done()
			items, err := c.catalogKind(ctx, typ)
			receive(typ, items, err)
		}(typ)
	}
	wg.Wait()
}

// CatalogKind loads a single instrument type (catalog + last prices).
func (c *Client) CatalogKind(ctx context.Context, kind string) ([]catalog.Instrument, error) {
	return c.catalogKind(ctx, kind)
}

func (c *Client) catalogKind(ctx context.Context, typ string) ([]catalog.Instrument, error) {
	method, ok := catalogMethods[typ]
	if !ok {
		return nil, fmt.Errorf("unknown instrument type %q", typ)
	}
	var resp instrumentsResponse
	if err := c.call(ctx, "InstrumentsService", method, map[string]string{"instrumentStatus": "INSTRUMENT_STATUS_BASE"}, &resp); err != nil {
		return nil, err
	}
	items := make([]catalog.Instrument, 0, len(resp.Instruments))
	for _, v := range resp.Instruments {
		x := catalog.Instrument{UID: v.UID, FIGI: v.Figi, Type: typ, Ticker: v.Ticker, Name: v.Name, ISIN: v.ISIN, Currency: v.Currency, Exchange: v.Exchange, Sector: v.Sector, RiskLevel: v.RiskLevel, CouponFrequency: v.CouponQuantityPerYear, FloatingCoupon: v.FloatingCouponFlag, ForQualInvestor: v.ForQualInvestorFlag, Amortized: v.AmortizationFlag, MaturityDate: v.MaturityDate, ContractualMaturityDate: v.MaturityDate, NextCouponDate: v.NextCouponDate}
		if v.Nominal.Currency != "" {
			x.Nominal = v.Nominal.String() + " " + v.Nominal.Currency
		}
		items = append(items, x)
	}
	if err := c.applyLastPrices(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *Client) applyLastPrices(ctx context.Context, items []catalog.Instrument) error {
	const batchSize = 300
	for start := 0; start < len(items); start += batchSize {
		end := min(start+batchSize, len(items))
		uids := make([]string, 0, end-start)
		for _, item := range items[start:end] {
			if item.UID != "" {
				uids = append(uids, item.UID)
			}
		}
		if len(uids) == 0 {
			continue
		}
		prices, err := c.LastPrices(ctx, uids)
		if err != nil {
			return err
		}
		byUID := make(map[string]lastPrice, len(prices))
		for _, price := range prices {
			byUID[price.InstrumentUID] = price
		}
		for i := start; i < end; i++ {
			if price, ok := byUID[items[i].UID]; ok {
				items[i].LastPrice = formatLastPrice(items[i], price)
			}
		}
	}
	return nil
}

func formatLastPrice(item catalog.Instrument, price lastPrice) string {
	value := price.Price.String()
	switch item.Type {
	case "bond":
		return value + "%"
	case "future":
		return value + " points"
	default:
		if item.Currency != "" {
			return value + " " + strings.ToUpper(item.Currency)
		}
		return value
	}
}

// EnrichCatalog fills coupon and dividend fields for a locally narrowed set.
// When non-nil, onProgress is called sequentially from the caller's goroutine
// with (completed, total) after each instrument finishes, success or failure.
func (c *Client) EnrichCatalog(ctx context.Context, items []catalog.Instrument, now time.Time, onProgress ProgressFunc) ([]catalog.Instrument, error) {
	out := append([]catalog.Instrument(nil), items...)
	// The enrichment endpoints share the InstrumentsService quota, which the
	// client limiter now paces for every call to that service. A small bounded
	// pool keeps the number of in-flight requests predictable while a full
	// catalog completes within the GUI's overall timeout.
	sem := make(chan struct{}, 3)
	done := make(chan struct{}, len(out))
	var failed atomic.Int32
	for i := range out {
		go func(i int) {
			sem <- struct{}{}
			defer func() { <-sem; done <- struct{}{} }()
			if out[i].Enriched {
				return
			}
			if out[i].Type == "bond" {
				// A bond may amortize over decades: ask for the whole life span,
				// not the default nearest-period window.
				if events, e := c.BondEvents(ctx, out[i].UID, out[i].FIGI, now.AddDate(-10, 0, 0), now.AddDate(30, 0, 0)); e == nil {
					if n, ok := nextBondCoupon(events, now); ok {
						out[i].NextCouponDate = eventDay(n)
						if out[i].CouponFrequency > 0 {
							nom := parseAmount(out[i].Nominal)
							if nom > 0 {
								r := n.PayOneBond.Float() * float64(out[i].CouponFrequency) / nom * 100
								out[i].CouponRatePct = &r
							}
						}
					}
					out[i].AmortizationDates, out[i].OfferDates = redemptionSchedule(events)
					contractual := out[i].ContractualMaturityDate
					if contractual == "" {
						contractual = out[i].MaturityDate
					}
					out[i].MaturityDate = effectiveMaturity(contractual, out[i].OfferDates, now)
				} else {
					failed.Add(1)
					c.log("Не удалось получить события облигации %s: %v", out[i].UID, e)
					return
				}
			} else if out[i].Type == "share" {
				d, e := c.Dividends(ctx, out[i].UID, out[i].FIGI, now.AddDate(-1, 0, 0), now)
				if e != nil {
					failed.Add(1)
					c.log("Не удалось получить дивиденды %s: %v", out[i].UID, e)
					return
				}
				out[i].HasDividends = len(d) > 0
			}
			out[i].Enriched = true
		}(i)
	}
	for i := range len(out) {
		<-done
		if onProgress != nil {
			onProgress(i+1, len(out))
		}
	}
	if count := int(failed.Load()); count > 0 {
		return out, &EnrichmentError{Failed: count, Cause: ctx.Err()}
	}
	return out, nil
}

// EnrichmentError reports per-instrument failures while preserving usable
// catalog rows. Callers can render unsuccessful fields as unavailable.
type EnrichmentError struct {
	Failed int
	Cause  error
}

func (e *EnrichmentError) Error() string {
	return fmt.Sprintf("catalog enrichment failed for %d instruments", e.Failed)
}

func (e *EnrichmentError) Unwrap() error { return e.Cause }

func effectiveMaturity(contractual string, offers []string, now time.Time) string {
	var nearest time.Time
	for _, raw := range offers {
		date, err := time.Parse("2006-01-02", raw)
		if err != nil || !date.After(now.UTC()) {
			continue
		}
		if nearest.IsZero() || date.Before(nearest) {
			nearest = date
		}
	}
	if nearest.IsZero() {
		return contractual
	}
	return nearest.Format("2006-01-02")
}

func nextBondCoupon(events []bondEvent, now time.Time) (bondEvent, bool) {
	var nearest bondEvent
	var nearestDate time.Time
	for _, event := range events {
		if event.EventType != "EVENT_TYPE_CPN" {
			continue
		}
		date, err := time.Parse("2006-01-02", eventDay(event))
		if err != nil || !date.After(now.UTC()) {
			continue
		}
		if nearestDate.IsZero() || date.Before(nearestDate) {
			nearest, nearestDate = event, date
		}
	}
	return nearest, !nearestDate.IsZero()
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

// instrumentByID returns reference data for one identifier, memoized per
// identifier, misses included: an instrument the catalog does not index must
// not be re-fetched for every operation that references it. Concurrent callers
// for the same identifier share a single request.
func (c *Client) instrumentByID(ctx context.Context, idType, id string) (*instrumentShort, error) {
	return c.instrCache.get(ctx, idType+":"+id, func(ctx context.Context) (*instrumentShort, error) {
		req := instrumentRequest{IDType: idType, ID: id}
		var resp getInstrumentResponse
		if err := c.call(ctx, "InstrumentsService", "GetInstrumentBy", req, &resp); err != nil {
			return nil, err
		}
		return &resp.Instrument, nil
	})
}

// canonicalKey names an instrument once. FIGI is the canonical identifier when
// the call carries one; the UID is used when it does not. markUnresolved and
// markResolved reconcile the two keys for the same paper.
func canonicalKey(uid, figi string) string {
	if figi != "" {
		return "figi:" + figi
	}
	return "uid:" + uid
}

// trackResolution records the outcome of one reference-data resolution. The
// counter only holds instruments whose reference data stayed missing, so a later
// successful lookup removes the instrument again.
func (c *Client) trackResolution(uid, figi string, value *instrumentShort, err error) (*instrumentShort, error) {
	if err != nil {
		c.markUnresolved(uid, figi)
		return nil, err
	}
	// A lookup by FIGI returns the catalog uid, so a record made by a UID-only
	// call is cleared even when the later call carries only the FIGI.
	if value != nil && value.UID != "" {
		uid = value.UID
	}
	c.markResolved(uid, figi)
	return value, nil
}

// InstrumentRef returns reference data (ticker/name/isin/currency) for an
// instrument. The operations and portfolio services can report a UID that the
// instruments catalog does not index — instruments excluded from the base
// catalog — while the same FIGI still resolves, so a permanent miss on the UID
// falls back to the FIGI. An empty UID is never sent as an identifier.
func (c *Client) InstrumentRef(ctx context.Context, uid, figi string) (*instrumentShort, error) {
	if uid == "" && figi == "" {
		return nil, fmt.Errorf("нет идентификатора инструмента для GetInstrumentBy")
	}
	if uid == "" {
		v, err := c.instrumentByID(ctx, "INSTRUMENT_ID_TYPE_FIGI", figi)
		return c.trackResolution(uid, figi, v, err)
	}
	v, err := c.instrumentByID(ctx, "INSTRUMENT_ID_TYPE_UID", uid)
	if err == nil {
		c.markResolved(uid, figi)
		return v, nil
	}
	if !isPermanent(err) || figi == "" {
		c.markUnresolved(uid, figi)
		return nil, err
	}
	f, ferr := c.instrumentByID(ctx, "INSTRUMENT_ID_TYPE_FIGI", figi)
	return c.trackResolution(uid, figi, f, ferr)
}

func (c *Client) bondByID(ctx context.Context, idType, id string) (*bond, error) {
	return c.bondCache.get(ctx, idType+":"+id, func(ctx context.Context) (*bond, error) {
		req := instrumentRequest{IDType: idType, ID: id}
		var resp bondResponse
		if err := c.call(ctx, "InstrumentsService", "BondBy", req, &resp); err != nil {
			return nil, err
		}
		return &resp.Instrument, nil
	})
}

// BondRef is BondBy with the same UID-then-FIGI resolution as InstrumentRef. It
// does not touch the unresolved counter: that counter measures reference data
// (ticker/name/isin/currency), and a bond-specific failure leaves those fields
// filled, while the CLI summary promises them empty.
func (c *Client) BondRef(ctx context.Context, uid, figi string) (*bond, error) {
	if uid == "" && figi == "" {
		return nil, fmt.Errorf("нет идентификатора инструмента для BondBy")
	}
	if uid == "" {
		return c.bondByID(ctx, "INSTRUMENT_ID_TYPE_FIGI", figi)
	}
	v, err := c.bondByID(ctx, "INSTRUMENT_ID_TYPE_UID", uid)
	if err == nil || !isPermanent(err) || figi == "" {
		return v, err
	}
	return c.bondByID(ctx, "INSTRUMENT_ID_TYPE_FIGI", figi)
}

// instrumentScoped runs an InstrumentsService method that accepts either a UID
// or a FIGI, falling back to the FIGI when the UID is not in the catalog.
func (c *Client) instrumentScoped(ctx context.Context, method, uid, figi string, request func(id string) any, out any) error {
	if uid == "" {
		uid, figi = figi, ""
	}
	if uid == "" {
		return fmt.Errorf("нет идентификатора инструмента для %s", method)
	}
	err := c.call(ctx, "InstrumentsService", method, request(uid), out)
	if err == nil || !isPermanent(err) || figi == "" {
		return err
	}
	return c.call(ctx, "InstrumentsService", method, request(figi), out)
}

// Coupons returns coupon events for a bond within [from, to]. The endpoint only
// accepts a FIGI.
func (c *Client) Coupons(ctx context.Context, figi string, from, to time.Time) ([]couponEvent, error) {
	req := couponsRequest{FIGI: figi, From: rfc3339(from), To: rfc3339(to)}
	var resp couponsResponse
	if err := c.call(ctx, "InstrumentsService", "GetBondCoupons", req, &resp); err != nil {
		return nil, err
	}
	return resp.Events, nil
}

// BondEvents returns the bond's lifecycle events (coupons, calls, redemptions)
// within [from, to].
func (c *Client) BondEvents(ctx context.Context, uid, figi string, from, to time.Time) ([]bondEvent, error) {
	var resp bondEventsResponse
	if err := c.instrumentScoped(ctx, "GetBondEvents", uid, figi, func(id string) any {
		return bondEventsRequest{InstrumentID: id, From: rfc3339(from), To: rfc3339(to)}
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Events, nil
}

// Dividends returns dividend events for a share within [from, to].
func (c *Client) Dividends(ctx context.Context, uid, figi string, from, to time.Time) ([]dividend, error) {
	var resp dividendsResponse
	if err := c.instrumentScoped(ctx, "GetDividends", uid, figi, func(id string) any {
		return dividendsRequest{InstrumentID: id, From: rfc3339(from), To: rfc3339(to)}
	}, &resp); err != nil {
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

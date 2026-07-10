package tinvest

import (
	"context"
	"time"
)

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

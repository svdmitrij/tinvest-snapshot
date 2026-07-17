package tinvest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dmitry/tinvest-snapshot/internal/model"
)

// mockAPI serves canned proto-JSON responses keyed by the method segment of
// the request path, letting us exercise Collect end-to-end without a network.
func mockAPI(t *testing.T) *httptest.Server {
	t.Helper()
	responses := map[string]string{
		"GetSandboxAccounts": `{"accounts":[{"id":"acc1","type":"ACCOUNT_TYPE_TINKOFF","name":"Брокерский","status":"ACCOUNT_STATUS_OPEN"}]}`,
		"GetSandboxPortfolio": `{
			"totalAmountPortfolio":{"currency":"rub","units":"10500","nano":0},
			"positions":[
				{"figi":"BBG00","instrumentType":"bond","instrumentUid":"bond-uid","quantity":{"units":"10","nano":0},
				 "averagePositionPrice":{"currency":"rub","units":"900","nano":0},
				 "currentNkd":{"currency":"rub","units":"5","nano":0},
				 "currentPrice":{"currency":"rub","units":"950","nano":0}},
				{"figi":"SHR00","instrumentType":"share","instrumentUid":"share-uid","quantity":{"units":"5","nano":0},
				 "averagePositionPrice":{"currency":"rub","units":"100","nano":0},
				 "currentPrice":{"currency":"rub","units":"120","nano":0}},
				{"figi":"RUB00","instrumentType":"currency","instrumentUid":"rub-uid","quantity":{"units":"1000","nano":0},
				 "averagePositionPrice":{"currency":"rub","units":"1","nano":0},
				 "currentPrice":{"currency":"rub","units":"1","nano":0}},
				{"figi":"RUB00","instrumentType":"currency","instrumentUid":"rub-uid","quantity":{"units":"500","nano":250000000},
				 "averagePositionPrice":{"currency":"rub","units":"1","nano":0},
				 "currentPrice":{"currency":"rub","units":"1","nano":0}}
			]}`,
		"BondBy": `{"instrument":{"figi":"BBG00","name":"ОФЗ","couponQuantityPerYear":4,"nominal":{"currency":"rub","units":"1000","nano":0},"riskLevel":"RISK_LEVEL_LOW","currency":"rub","maturityDate":"2027-07-09T00:00:00Z","floatingCouponFlag":false,"perpetualFlag":false,"amortizationFlag":false}}`,
		"GetBondCoupons": `{"events":[
			{"couponDate":"2026-09-01T00:00:00Z","payOneBond":{"currency":"rub","units":"20","nano":0},"couponType":"COUPON_TYPE_FIXED"},
			{"couponDate":"2026-12-01T00:00:00Z","payOneBond":{"currency":"rub","units":"20","nano":0},"couponType":"COUPON_TYPE_FIXED"},
			{"couponDate":"2027-03-01T00:00:00Z","payOneBond":{"currency":"rub","units":"20","nano":0},"couponType":"COUPON_TYPE_FIXED"},
			{"couponDate":"2027-06-01T00:00:00Z","payOneBond":{"currency":"rub","units":"20","nano":0},"couponType":"COUPON_TYPE_FIXED"}
		]}`,
		"GetDividends": `{"dividends":[{"dividendNet":{"currency":"rub","units":"7","nano":0},"paymentDate":"2026-05-01T00:00:00Z","declaredDate":"2026-04-01T00:00:00Z","regularity":"annual"}]}`,
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		method := parts[len(parts)-1]
		if method == "GetInstrumentBy" {
			id := "unknown"
			body := make([]byte, r.ContentLength)
			r.Body.Read(body)
			switch {
			case strings.Contains(string(body), "bond-uid"):
				id = `{"instrument":{"figi":"BBG00","ticker":"SU26240","isin":"RU000A101","name":"ОФЗ 26240","currency":"rub","instrumentType":"bond","uid":"bond-uid"}}`
			case strings.Contains(string(body), "share-uid"):
				id = `{"instrument":{"figi":"SHR00","ticker":"SBER","isin":"RU0009029540","name":"Сбербанк","currency":"rub","instrumentType":"share","uid":"share-uid"}}`
			default:
				id = `{"instrument":{}}`
			}
			w.Write([]byte(id))
			return
		}
		resp, ok := responses[method]
		if !ok {
			http.Error(w, "no mock for "+method, http.StatusNotFound)
			return
		}
		w.Write([]byte(resp))
	}))
}

func TestCollectSandbox(t *testing.T) {
	srv := mockAPI(t)
	defer srv.Close()

	c := New(srv.URL, "test-token", "test", 1, time.Millisecond, nil)
	c.Sandbox = true

	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	snap, err := c.Collect(context.Background(), "sandbox", "", now, nil)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(snap.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(snap.Accounts))
	}
	acc := snap.Accounts[0]
	if len(acc.Positions) != 2 {
		t.Fatalf("positions = %d, want 2 (bond+share, cash excluded)", len(acc.Positions))
	}
	// Two rub currency positions (1000 + 500.25) must aggregate into one entry.
	if len(acc.Cash) != 1 || acc.Cash[0].Currency != "rub" || acc.Cash[0].Amount != "1500.25" {
		t.Errorf("cash = %+v, want single rub 1500.25", acc.Cash)
	}
	if acc.Total.Amount != "10500" || acc.Total.Currency != "rub" {
		t.Errorf("account total = %+v", acc.Total)
	}

	var bond, share *model.Position
	for i := range acc.Positions {
		switch acc.Positions[i].InstrumentType {
		case "bond":
			bond = &acc.Positions[i]
		case "share":
			share = &acc.Positions[i]
		}
	}
	if bond == nil || share == nil {
		t.Fatal("expected one bond and one share position")
	}

	if bond.Ticker != "SU26240" || bond.CurrentValue != "9500" || bond.PnLAbs != "500" {
		t.Errorf("bond position = %+v", bond)
	}
	if bond.Bond == nil || bond.Bond.CouponFrequency != "4" {
		t.Errorf("bond coupon frequency = %+v", bond.Bond)
	}
	if bond.Bond.NextCouponDate != "2026-09-01" || bond.Bond.NextCouponAmount != "20" {
		t.Errorf("next coupon = %s / %s", bond.Bond.NextCouponDate, bond.Bond.NextCouponAmount)
	}
	// coupon rate = 20 * 4 / 1000 * 100 = 8.00
	if bond.Bond.CouponRatePct != "8" && bond.Bond.CouponRatePct != "8.00" {
		t.Errorf("coupon rate = %q, want ~8", bond.Bond.CouponRatePct)
	}

	if share.Share == nil || share.Share.LastDividendAmount != "7" {
		t.Errorf("share dividend = %+v", share.Share)
	}

	// 6.1 current yield = annual coupon (20*4=80) / clean price 950 = 8.42%.
	if bond.Bond.CurrentYield != "8.42" {
		t.Errorf("current yield = %q, want 8.42", bond.Bond.CurrentYield)
	}
	// 6.2 YTM must be a number for this fixed, non-amortized bond, and since it
	// trades at a discount (dirty 955 < redemption 1000) it must exceed the
	// current yield.
	if bond.Bond.YieldToMaturity == model.NA {
		t.Fatalf("ytm = NA, want a number for fixed non-amortized bond")
	}
	ytm, err := strconv.ParseFloat(bond.Bond.YieldToMaturity, 64)
	if err != nil {
		t.Fatalf("ytm not a number: %q", bond.Bond.YieldToMaturity)
	}
	if ytm <= 8.42 || ytm > 30 {
		t.Errorf("ytm = %v, want in (8.42, 30] for a discounted bond", ytm)
	}
}

func TestActiveInvestmentAccountsExcludeClosedAndUnsupported(t *testing.T) {
	accounts := activeInvestmentAccounts([]apiAccount{
		{ID: "broker", Type: "ACCOUNT_TYPE_TINKOFF", Status: "ACCOUNT_STATUS_OPEN"},
		{ID: "iis", Type: "ACCOUNT_TYPE_TINKOFF_IIS", Status: "ACCOUNT_STATUS_OPEN"},
		{ID: "closed", Type: "ACCOUNT_TYPE_TINKOFF", Status: "ACCOUNT_STATUS_CLOSED"},
		{ID: "other", Type: "ACCOUNT_TYPE_INVEST_BOX", Status: "ACCOUNT_STATUS_OPEN"},
	})
	if len(accounts) != 2 || accounts[0].ID != "broker" || accounts[1].ID != "iis" {
		t.Fatalf("active investment accounts = %#v", accounts)
	}
}

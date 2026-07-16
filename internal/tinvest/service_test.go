package tinvest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dmitry/tinvest-snapshot/internal/catalog"
	"github.com/dmitry/tinvest-snapshot/internal/money"
)

func TestCatalogLoadsDirectoriesConcurrently(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(80 * time.Millisecond)
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		w.Write([]byte(`{"instruments":[{"uid":"` + method + `","ticker":"` + method + `"}]}`))
	}))
	defer server.Close()

	client := New(server.URL, "token", "test", 0, time.Millisecond, nil)
	started := time.Now()
	items, err := client.Catalog(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("instruments = %d, want 5", len(items))
	}
	if elapsed := time.Since(started); elapsed >= 250*time.Millisecond {
		t.Fatalf("Catalog took %s; directories must load concurrently", elapsed)
	}
}

func TestCatalogRetainsDirectoryNextCouponDate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if method == "Bonds" {
			w.Write([]byte(`{"instruments":[{"uid":"bond","ticker":"BOND","nextCouponDate":"2026-09-01T00:00:00Z"}]}`))
			return
		}
		w.Write([]byte(`{"instruments":[]}`))
	}))
	defer server.Close()
	items, err := New(server.URL, "token", "test", 0, time.Millisecond, nil).Catalog(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.UID == "bond" && item.NextCouponDate == "2026-09-01T00:00:00Z" {
			return
		}
	}
	t.Fatalf("directory next coupon date was lost: %#v", items)
}

func TestEnrichCatalogGetsCouponsByFIGI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if method == "GetBondCoupons" {
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request["figi"] != "BBG00REAL" || request["instrumentId"] != nil {
				http.Error(w, "expected figi", http.StatusBadRequest)
				return
			}
			w.Write([]byte(`{"events":[{"couponDate":"2026-09-01T00:00:00Z","payOneBond":{"currency":"rub","units":"20","nano":0}}]}`))
			return
		}
		w.Write([]byte(`{"events":[]}`))
	}))
	defer server.Close()
	client := New(server.URL, "token", "test", 0, time.Millisecond, nil)
	items, err := client.EnrichCatalog(context.Background(), []catalog.Instrument{{Type: "bond", UID: "uid", FIGI: "BBG00REAL", CouponFrequency: 4, Nominal: "1000 rub"}}, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got := items[0].NextCouponDate; got != "2026-09-01T00:00:00Z" {
		t.Fatalf("next coupon = %q", got)
	}
}

func TestEnrichCatalogUsesUIDForBondEventsAndDividends(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if method == "GetBondCoupons" {
			w.Write([]byte(`{"events":[]}`))
			return
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"GetBondEvents": "bond-uid", "GetDividends": "share-uid"}[method]
		if request["instrumentId"] != want || request["figi"] != nil {
			http.Error(w, "expected instrument UID", http.StatusBadRequest)
			return
		}
		switch method {
		case "GetBondEvents":
			w.Write([]byte(`{"events":[{"eventType":"EVENT_TYPE_CALL","payDate":"2026-09-11T00:00:00Z"}]}`))
		case "GetDividends":
			w.Write([]byte(`{"dividends":[{"paymentDate":"2026-09-01T00:00:00Z"}]}`))
		}
	}))
	defer server.Close()
	items, err := New(server.URL, "token", "test", 0, time.Millisecond, nil).EnrichCatalog(context.Background(), []catalog.Instrument{{Type: "bond", UID: "bond-uid", FIGI: "BBG-BOND", MaturityDate: "2030-12-13"}, {Type: "share", UID: "share-uid", FIGI: "BBG-SHARE"}}, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if items[0].MaturityDate != "2026-09-11" || !items[1].HasDividends {
		t.Fatalf("catalog contract result = %#v", items)
	}
}

func TestPortfolioDividendsUseUIDInstrumentID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["instrumentId"] != "share-uid" || request["figi"] != nil {
			http.Error(w, "expected instrument UID", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"dividends":[{"paymentDate":"2026-09-01T00:00:00Z","dividendNet":{"currency":"rub","units":"1","nano":0}}]}`))
	}))
	defer server.Close()
	info := New(server.URL, "token", "test", 0, time.Millisecond, nil).enrichShare(context.Background(), portfolioPosition{InstrumentUID: "share-uid", Figi: "BBG-SHARE"}, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if info.NextPaymentDate != "2026-09-01" {
		t.Fatalf("next payment = %q", info.NextPaymentDate)
	}
}

func TestEnrichCatalogRejectsPartialCouponFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()
	_, err := New(server.URL, "token", "test", 0, time.Millisecond, nil).EnrichCatalog(context.Background(), []catalog.Instrument{{Type: "bond", UID: "uid", FIGI: "RU000A102LF6"}}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "GetBondCoupons uid=uid figi=RU000A102LF6") {
		t.Fatalf("partial coupon failure was accepted: %v", err)
	}
}

func TestCouponsHonorsRetryAfterThenSucceeds(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"events":[{"couponDate":"2030-01-01T00:00:00Z"}]}`))
	}))
	defer server.Close()
	start := time.Now()
	_, err := New(server.URL, "token", "test", 1, time.Millisecond, nil).Coupons(context.Background(), "FIGI", time.Now(), time.Now())
	if err != nil || attempts != 2 || time.Since(start) < time.Second {
		t.Fatalf("retry-after: attempts=%d err=%v elapsed=%s", attempts, err, time.Since(start))
	}
}

func TestEnrichCatalogPacesConcurrentWorkersBelowGlobalQuota(t *testing.T) {
	var mu sync.Mutex
	var requests []time.Time
	var rejected int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		now := time.Now()
		requests = append(requests, now)
		count := 0
		for _, at := range requests {
			if now.Sub(at) < time.Second {
				count++
			}
		}
		mu.Unlock()
		if count > 7 {
			mu.Lock()
			rejected++
			mu.Unlock()
			w.Header().Set("Retry-After", "1")
			http.Error(w, "quota", http.StatusTooManyRequests)
			return
		}
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if method == "GetBondCoupons" {
			w.Write([]byte(`{"events":[{"couponDate":"2030-01-01T00:00:00Z"}]}`))
			return
		}
		w.Write([]byte(`{"events":[]}`))
	}))
	defer server.Close()
	items := make([]catalog.Instrument, 30)
	for i := range items {
		items[i] = catalog.Instrument{Type: "bond", UID: fmt.Sprintf("uid-%d", i), FIGI: fmt.Sprintf("figi-%d", i)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	got, err := New(server.URL, "token", "test", 3, time.Millisecond, nil).EnrichCatalog(ctx, items, time.Now())
	if err != nil || len(got) != len(items) {
		t.Fatalf("quota enrichment: items=%d err=%v", len(got), err)
	}
	mu.Lock()
	defer mu.Unlock()
	if rejected == 0 || len(requests) < 60 {
		t.Fatalf("quota requests=%d rejected=%d", len(requests), rejected)
	}
}

func TestEnrichCatalogKeepsWorkerThroughputWithoutCooldown(t *testing.T) {
	var mu sync.Mutex
	inFlight, peak := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()
		defer func() {
			mu.Lock()
			inFlight--
			mu.Unlock()
		}()
		time.Sleep(50 * time.Millisecond)
		if strings.HasSuffix(r.URL.Path, "/GetBondCoupons") {
			w.Write([]byte(`{"events":[{"couponDate":"2030-01-01T00:00:00Z"}]}`))
			return
		}
		w.Write([]byte(`{"events":[]}`))
	}))
	defer server.Close()
	items := make([]catalog.Instrument, 150)
	for i := range items {
		items[i] = catalog.Instrument{Type: "bond", UID: fmt.Sprintf("uid-%d", i), FIGI: fmt.Sprintf("figi-%d", i)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	started := time.Now()
	got, err := New(server.URL, "token", "test", 3, time.Millisecond, nil).EnrichCatalog(ctx, items, time.Now())
	if err != nil || len(got) != len(items) {
		t.Fatalf("throughput enrichment: items=%d err=%v", len(got), err)
	}
	if elapsed := time.Since(started); elapsed >= 30*time.Second {
		t.Fatalf("throughput enrichment took %s", elapsed)
	}
	mu.Lock()
	defer mu.Unlock()
	if peak < 2 {
		t.Fatalf("peak parallel requests = %d, want at least 2", peak)
	}
}

func TestFormatLastPriceUsesNativeInstrumentFormat(t *testing.T) {
	price := lastPrice{Price: money.Quotation{Units: 123, Nano: 450000000}}
	cases := []struct{ kind, currency, want string }{
		{"share", "rub", "123.45 RUB"}, {"bond", "rub", "123.45%"}, {"future", "rub", "123.45 points"}, {"currency", "usd", "123.45 USD"},
	}
	for _, tc := range cases {
		if got := formatLastPrice(catalog.Instrument{Type: tc.kind, Currency: tc.currency}, price); got != tc.want {
			t.Errorf("%s price = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

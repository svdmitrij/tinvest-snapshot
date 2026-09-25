package tinvest

import (
	"context"
	"encoding/json"
	"errors"
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

	client, err := New(server.URL, "token", "test", 0, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	var all []catalog.Instrument
	var mu sync.Mutex
	var firstErr error
	client.Catalog(context.Background(), time.Now(), func(typ string, items []catalog.Instrument, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil && firstErr == nil {
			firstErr = err
		}
		all = append(all, items...)
	})
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	if len(all) != 5 {
		t.Fatalf("instruments = %d, want 5", len(all))
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
	client, err := New(server.URL, "token", "test", 0, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var all []catalog.Instrument
	var firstErr error
	client.Catalog(context.Background(), time.Now(), func(typ string, items []catalog.Instrument, err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
		all = append(all, items...)
	})
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	for _, item := range all {
		if item.UID == "bond" && item.NextCouponDate == "2026-09-01T00:00:00Z" {
			return
		}
	}
	t.Fatalf("directory next coupon date was lost: %#v", all)
}

func TestCatalogRetainsForQualInvestorFlagAndMissingValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if method == "Bonds" {
			_, _ = w.Write([]byte(`{"instruments":[{"uid":"yes","forQualInvestorFlag":true},{"uid":"unknown"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"instruments":[]}`))
	}))
	defer server.Close()
	client, err := New(server.URL, "token", "test", 0, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.CatalogKind(context.Background(), "bond")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ForQualInvestor == nil || !*items[0].ForQualInvestor || items[1].ForQualInvestor != nil {
		t.Fatalf("qual flags = %#v", items)
	}
}

func TestEnrichCatalogGetsCouponFromBondEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if method == "GetBondEvents" {
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request["instrumentId"] != "uid" || request["figi"] != nil {
				http.Error(w, "expected instrument UID", http.StatusBadRequest)
				return
			}
			w.Write([]byte(`{"events":[{"eventType":"EVENT_TYPE_CPN","payDate":"2026-09-01T00:00:00Z","payOneBond":{"currency":"rub","units":"20","nano":0}}]}`))
			return
		}
		w.Write([]byte(`{"events":[]}`))
	}))
	defer server.Close()
	client, err := New(server.URL, "token", "test", 0, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.EnrichCatalog(context.Background(), []catalog.Instrument{{Type: "bond", UID: "uid", FIGI: "BBG00REAL", CouponFrequency: 4, Nominal: "1000 rub"}}, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := items[0].NextCouponDate; got != "2026-09-01" {
		t.Fatalf("next coupon = %q", got)
	}
}

func TestEnrichCatalogUsesUIDForBondEventsAndDividends(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
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
			w.Write([]byte(`{"events":[{"eventType":"EVENT_TYPE_CPN","payDate":"2026-09-01T00:00:00Z"},{"eventType":"EVENT_TYPE_CALL","payDate":"2026-09-11T00:00:00Z"}]}`))
		case "GetDividends":
			w.Write([]byte(`{"dividends":[{"paymentDate":"2026-09-01T00:00:00Z"}]}`))
		}
	}))
	defer server.Close()
	client, err := New(server.URL, "token", "test", 0, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.EnrichCatalog(context.Background(), []catalog.Instrument{{Type: "bond", UID: "bond-uid", FIGI: "BBG-BOND", MaturityDate: "2030-12-13"}, {Type: "share", UID: "share-uid", FIGI: "BBG-SHARE"}}, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), nil)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].MaturityDate != "2026-09-11" || items[0].NextCouponDate != "2026-09-01" || !items[1].HasDividends {
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
	client, err := New(server.URL, "token", "test", 0, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	info := client.enrichShare(context.Background(), portfolioPosition{InstrumentUID: "share-uid", Figi: "BBG-SHARE"}, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if info.NextPaymentDate != "2026-09-01" {
		t.Fatalf("next payment = %q", info.NextPaymentDate)
	}
}

func TestEnrichCatalogPreservesPartialCouponFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()
	client, err := New(server.URL, "token", "test", 0, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.EnrichCatalog(context.Background(), []catalog.Instrument{{Type: "bond", UID: "uid", FIGI: "RU000A102LF6"}}, time.Now(), nil)
	var enrichment *EnrichmentError
	if !errors.As(err, &enrichment) || enrichment.Failed != 1 {
		t.Fatalf("partial coupon failure = %v, want one reported failure", err)
	}
	if len(got) != 1 || got[0].Enriched {
		t.Fatalf("partial catalog = %#v, want one un-enriched row", got)
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
	client, err := New(server.URL, "token", "test", 1, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Coupons(context.Background(), "FIGI", time.Now(), time.Now())
	if err != nil || attempts != 2 || time.Since(start) < time.Second {
		t.Fatalf("retry-after: attempts=%d err=%v elapsed=%s", attempts, err, time.Since(start))
	}
}

func TestEnrichCatalogPacesBelowInstrumentsServiceQuota(t *testing.T) {
	var mu sync.Mutex
	var requests []time.Time
	var rejected int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		now := time.Now()
		requests = append(requests, now)
		count := 0
		for _, at := range requests {
			if now.Sub(at) < 3*time.Second {
				count++
			}
		}
		mu.Unlock()
		if count > 10 {
			mu.Lock()
			rejected++
			mu.Unlock()
			w.Header().Set("Retry-After", "1")
			http.Error(w, "quota", http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"events":[{"eventType":"EVENT_TYPE_CPN","payDate":"2030-01-01T00:00:00Z"}]}`))
	}))
	defer server.Close()
	items := make([]catalog.Instrument, 30)
	for i := range items {
		items[i] = catalog.Instrument{Type: "bond", UID: fmt.Sprintf("uid-%d", i), FIGI: fmt.Sprintf("figi-%d", i)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	client, err := New(server.URL, "token", "test", 3, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.EnrichCatalog(ctx, items, time.Now(), nil)
	if err != nil || len(got) != len(items) {
		t.Fatalf("quota enrichment: items=%d err=%v", len(got), err)
	}
	mu.Lock()
	defer mu.Unlock()
	if rejected != 0 || len(requests) != 30 {
		t.Fatalf("quota requests=%d rejected=%d", len(requests), rejected)
	}
}

func TestEnrichCatalogCompletes400BondsWithinQuotaTimeout(t *testing.T) {
	// The test compresses the documented 200-request/minute InstrumentsService
	// limit by 20x: 150 ms models a three-second, ten-request rolling window.
	const quotaWindow = 150 * time.Millisecond
	const scaledTimeout = 9 * time.Second
	var mu sync.Mutex
	var requests []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		now := time.Now()
		requests = append(requests, now)
		count := 0
		for _, at := range requests {
			if now.Sub(at) < quotaWindow {
				count++
			}
		}
		mu.Unlock()
		if count > 10 {
			http.Error(w, "quota", http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"events":[{"eventType":"EVENT_TYPE_CPN","payDate":"2030-01-01T00:00:00Z"}]}`))
	}))
	defer server.Close()
	items := make([]catalog.Instrument, 400)
	for i := range items {
		items[i] = catalog.Instrument{Type: "bond", UID: fmt.Sprintf("uid-%d", i), FIGI: fmt.Sprintf("figi-%d", i)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), scaledTimeout)
	defer cancel()
	client, err := New(server.URL, "token", "test", 3, quotaWindow, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	client.enrichmentInterval = defaultEnrichmentInterval / 20
	got, err := client.EnrichCatalog(ctx, items, time.Now(), nil)
	if err != nil || len(got) != len(items) {
		t.Fatalf("full quota enrichment: items=%d err=%v", len(got), err)
	}
	for _, item := range got {
		if !item.Enriched {
			t.Fatal("full quota enrichment returned a partial catalog")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 400 {
		t.Fatalf("requests = %d, want one GetBondEvents request per bond", len(requests))
	}
}

// TestEnrichCatalogMeasuredLiveBondVolume models the catalog measured on the
// live API on 2026-07-17: 1558 INSTRUMENT_STATUS_BASE bonds, one GetBondEvents
// each, under the documented 200-requests/60s InstrumentsService window. Time
// is compressed 40x (window 75 ms holds 10 requests, pacing 320 ms -> 8 ms).
// At the current pacing the full volume needs ~499s: a 300-second budget must
// preserve the partial catalog, while a 600-second budget must complete
// all 1558 bonds without a single 429.
func TestEnrichCatalogMeasuredLiveBondVolume(t *testing.T) {
	const (
		liveBonds   = 1558
		quotaWindow = 75 * time.Millisecond
		scaledIn300 = 300 * time.Second / 40
		scaledIn600 = 600 * time.Second / 40
	)
	newQuotaServer := func() (*httptest.Server, func() (requests, rejected int)) {
		var mu sync.Mutex
		var arrivals []time.Time
		var total, over429 int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			now := time.Now()
			arrivals = append(arrivals, now)
			total++
			count := 0
			for _, at := range arrivals {
				if now.Sub(at) < quotaWindow {
					count++
				}
			}
			over := count > 10
			if over {
				over429++
			}
			mu.Unlock()
			if over {
				http.Error(w, "quota", http.StatusTooManyRequests)
				return
			}
			w.Write([]byte(`{"events":[{"eventType":"EVENT_TYPE_CPN","payDate":"2030-01-01T00:00:00Z"}]}`))
		}))
		return server, func() (int, int) {
			mu.Lock()
			defer mu.Unlock()
			return total, over429
		}
	}
	items := make([]catalog.Instrument, liveBonds)
	for i := range items {
		items[i] = catalog.Instrument{Type: "bond", UID: fmt.Sprintf("uid-%d", i), FIGI: fmt.Sprintf("figi-%d", i)}
	}

	t.Run("default 300s budget cannot cover the live volume", func(t *testing.T) {
		server, _ := newQuotaServer()
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), scaledIn300)
		defer cancel()
		client, err := New(server.URL, "token", "test", 3, quotaWindow, nil, "", false)
		if err != nil {
			t.Fatal(err)
		}
		client.enrichmentInterval = defaultEnrichmentInterval / 40
		got, err := client.EnrichCatalog(ctx, items, time.Now(), nil)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected deadline exceeded, got %v", err)
		}
		if len(got) != liveBonds {
			t.Fatalf("timed-out enrichment returned %d rows, want %d partial rows", len(got), liveBonds)
		}
	})

	t.Run("600s budget completes the live volume without 429", func(t *testing.T) {
		server, counts := newQuotaServer()
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), scaledIn600)
		defer cancel()
		client, err := New(server.URL, "token", "test", 3, quotaWindow, nil, "", false)
		if err != nil {
			t.Fatal(err)
		}
		client.enrichmentInterval = defaultEnrichmentInterval / 40
		start := time.Now()
		got, err := client.EnrichCatalog(ctx, items, time.Now(), nil)
		elapsed := time.Since(start)
		if err != nil || len(got) != liveBonds {
			t.Fatalf("live-volume enrichment: items=%d err=%v", len(got), err)
		}
		for _, item := range got {
			if !item.Enriched {
				t.Fatal("live-volume enrichment returned a partial catalog")
			}
		}
		if requests, rejected := counts(); requests != liveBonds || rejected != 0 {
			t.Fatalf("requests=%d rejected=%d, want exactly %d and 0", requests, rejected, liveBonds)
		}
		t.Logf("scaled elapsed=%v (~%v real)", elapsed.Round(time.Millisecond), (elapsed * 40).Round(time.Second))
	})
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
func TestEnrichCatalogReportsProgressPerInstrument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if method == "GetBondEvents" {
			w.Write([]byte(`{"events":[]}`))
			return
		}
		w.Write([]byte(`{"dividends":[]}`))
	}))
	defer server.Close()
	client, err := New(server.URL, "token", "test", 0, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]catalog.Instrument, 5)
	for i := range items {
		items[i] = catalog.Instrument{Type: "bond", UID: fmt.Sprintf("uid-%d", i), FIGI: fmt.Sprintf("figi-%d", i)}
	}
	var progress []int
	enriched, err := client.EnrichCatalog(context.Background(), items, time.Now(), func(current, total int) {
		if total != len(items) {
			t.Fatalf("progress total = %d, want %d", total, len(items))
		}
		progress = append(progress, current)
	})
	if err != nil || len(enriched) != len(items) {
		t.Fatalf("enrichment: items=%d err=%v", len(enriched), err)
	}
	if len(progress) != len(items) {
		t.Fatalf("progress reports = %v, want one per instrument", progress)
	}
	for i := range progress {
		if progress[i] != i+1 {
			t.Fatalf("progress sequence = %v, want 1..%d in order", progress, len(items))
		}
	}
}

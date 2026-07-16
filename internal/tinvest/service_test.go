package tinvest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

package tinvest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCallDoesNotRetryPermanentError(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, `{"code":5,"message":"Instrument not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	c, err := New(srv.URL, "tok", "test", 3, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	err = c.call(context.Background(), "InstrumentsService", "GetInstrumentBy",
		instrumentRequest{IDType: "INSTRUMENT_ID_TYPE_UID", ID: "uid"}, &getInstrumentResponse{})
	var api *APIError
	if !errors.As(err, &api) || !api.Permanent() {
		t.Fatalf("error = %v, want a permanent APIError", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("attempts = %d, want 1: a permanent error must not be retried", got)
	}
}

func TestCallStillRetriesQuotaError(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			http.Error(w, "quota", http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"instrument":{"ticker":"GLRX"}}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL, "tok", "test", 2, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	c.limiterInterval = time.Millisecond
	var resp getInstrumentResponse
	if err := c.call(context.Background(), "InstrumentsService", "GetInstrumentBy",
		instrumentRequest{IDType: "INSTRUMENT_ID_TYPE_UID", ID: "uid"}, &resp); err != nil {
		t.Fatalf("quota error should be retried, got %v", err)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Errorf("attempts = %d, want 2", hits)
	}
}

func TestLimiterWaitsForTheReportedQuotaReset(t *testing.T) {
	var hits int32
	first := make(chan time.Time, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			first <- time.Now()
			w.Header().Set("x-ratelimit-limit", "200, 200;w=60")
			w.Header().Set("x-ratelimit-remaining", "0")
			w.Header().Set("x-ratelimit-reset", "1")
			http.Error(w, "quota", http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"accounts":[]}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL, "tok", "test", 3, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	c.limiterInterval = time.Millisecond
	if _, err := c.Accounts(context.Background()); err != nil {
		t.Fatalf("accounts: %v", err)
	}
	at := <-first
	if elapsed := time.Since(at); elapsed < time.Second {
		t.Errorf("second attempt started after %v, want the reported 1s window reset", elapsed)
	}
}

// TestLimiterPacesEveryInstrumentsServiceCall is the regression for the quota
// storm: the service budget is shared by lookups, not only by the enrichment
// endpoints, so a run that mixes GetInstrumentBy with GetBondEvents must stay
// inside the window.
func TestLimiterPacesEveryInstrumentsServiceCall(t *testing.T) {
	const window = 3 * time.Second
	const limit = 10
	var mu sync.Mutex
	var arrivals []time.Time
	var rejected int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		now := time.Now()
		arrivals = append(arrivals, now)
		count := 0
		for _, at := range arrivals {
			if now.Sub(at) < window {
				count++
			}
		}
		mu.Unlock()
		if count > limit {
			mu.Lock()
			rejected++
			mu.Unlock()
			http.Error(w, "quota", http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"instrument":{"ticker":"T","uid":"U"}}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL, "tok", "test", 3, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := range 12 {
		var resp getInstrumentResponse
		if err := c.call(ctx, "InstrumentsService", "GetInstrumentBy",
			instrumentRequest{IDType: "INSTRUMENT_ID_TYPE_UID", ID: "uid"}, &resp); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(arrivals) != 12 || rejected != 0 {
		t.Errorf("requests=%d rejected=%d, want 12 paced requests and no rejection", len(arrivals), rejected)
	}
}

func TestParseQuotaValueHandlesCounterLists(t *testing.T) {
	cases := []struct {
		value  string
		number int
		window time.Duration
		ok     bool
	}{
		{"200, 200;w=60", 200, time.Minute, true},
		{"198", 198, 0, true},
		{"37, 37;w=60", 37, time.Minute, true},
		{"0", 0, 0, true},
		{"", 0, 0, false},
		{"abc", 0, 0, false},
		{"120, 60", 60, 0, true},
	}
	for _, tc := range cases {
		number, window, ok := parseQuotaValue(tc.value)
		if number != tc.number || window != tc.window || ok != tc.ok {
			t.Errorf("parseQuotaValue(%q) = %d, %v, %v; want %d, %v, %v", tc.value, number, window, ok, tc.number, tc.window, tc.ok)
		}
	}
}

// TestLimiterDoesNotStretchTheWindowPerReservation: once the window budget is
// exhausted, every reservation must land on the same window deadline instead of
// a fresh full window counted from its own moment.
func TestLimiterDoesNotStretchTheWindowPerReservation(t *testing.T) {
	l := &limiter{interval: 100 * time.Millisecond, limit: 10, remaining: 10, window: time.Second}
	if err := l.reserve(context.Background()); err != nil {
		t.Fatal(err)
	}
	l.observe(http.Header{"X-Ratelimit-Remaining": []string{"0"}, "X-Ratelimit-Reset": []string{"1"}})

	start := time.Now()
	if err := l.reserve(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := time.Since(start)
	if first < 900*time.Millisecond {
		t.Fatalf("first reservation after exhaustion waited %v, want the window deadline", first)
	}
	if err := l.reserve(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := time.Since(start)
	if second > 1500*time.Millisecond {
		t.Fatalf("second reservation waited %v: the window deadline drifted past the reset", second)
	}
}

// TestParseQuotaHeadersBindsCounterToItsOwnWindow: a header with counters for
// different windows must not mix a count from one window with the window of
// another.
func TestParseQuotaHeadersBindsCounterToItsOwnWindow(t *testing.T) {
	headers := http.Header{}
	headers.Set("x-ratelimit-limit", "100;w=1, 5000;w=60")
	limit, window, _, _, ok := parseQuotaHeaders(headers)
	if !ok || limit != 100 || window != time.Second {
		t.Errorf("limit=%d window=%v ok=%v, want 100 with its own 1s window", limit, window, ok)
	}
}

// TestQuotaResetIsClamped: an absolute epoch in x-ratelimit-reset must not push
// the schedule decades out.
func TestQuotaResetIsClamped(t *testing.T) {
	headers := http.Header{}
	headers.Set("x-ratelimit-reset", "1767000000")
	_, _, _, reset, ok := parseQuotaHeaders(headers)
	if !ok || reset != maxQuotaResetSeconds*time.Second {
		t.Errorf("reset=%v ok=%v, want the %ds clamp", reset, ok, maxQuotaResetSeconds)
	}
}

func TestTransientFourXXIsNotPermanent(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusRequestTimeout, http.StatusTooEarly} {
		if (&APIError{Status: status}).Permanent() {
			t.Errorf("HTTP %d should stay retryable", status)
		}
	}
	if !(&APIError{Status: http.StatusNotFound}).Permanent() {
		t.Error("HTTP 404 should be permanent")
	}
}

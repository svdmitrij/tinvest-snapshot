package tinvest

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const contractPrefix = "tinkoff.public.invest.api.contract.v1."

// defaultLimiterInterval paces one request per service group. The documented
// InstrumentsService REST quota is 200 requests per 60-second window, so 320 ms
// keeps a small margin; the limiter additionally follows the quota headers the
// gateway returns on every response.
const defaultLimiterInterval = 320 * time.Millisecond

// documentedQuota is the per-service unary limit (requests per minute) from the
// API rate-limit policy. It seeds the window budget; the x-ratelimit-* headers
// override it as soon as a response arrives.
var documentedQuota = map[string]int{
	"InstrumentsService": 200,
	"OperationsService":  200,
	"MarketDataService":  600,
	"UsersService":       100,
	"SandboxService":     200,
}

// Logf logs a diagnostic line. The token is never passed to it.
type Logf func(format string, args ...any)

// defaultQuotaWindow is the window the gateway reports when a header carries a
// counter without an explicit ";w=" part.
const defaultQuotaWindow = time.Minute

// maxQuotaResetSeconds clamps x-ratelimit-reset: some deployments report an
// absolute epoch or a value far larger than the window, which would push the
// schedule years into the future.
const maxQuotaResetSeconds = 600

// limiter paces requests to one service group. A slot is reserved per request
// and consumes the window budget; once the budget is spent, the next slot lands
// on the window deadline the gateway reported instead of a fresh full window
// counted from the moment of the reservation.
type limiter struct {
	mu        sync.Mutex
	interval  time.Duration
	next      time.Time
	limit     int           // requests allowed per window
	remaining int           // requests still allowed in the current window
	window    time.Duration // window length the gateway reports
	resetAt   time.Time     // when the current window ends
}

func (l *limiter) reserve(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	if l.window > 0 && !l.resetAt.IsZero() && !now.Before(l.resetAt) {
		// The window rolled over: the budget is whole again.
		l.remaining = l.limit
		l.resetAt = now.Add(l.window)
	}
	start := now
	if l.next.After(start) {
		start = l.next
	}
	if l.remaining <= 0 && !l.resetAt.IsZero() && l.resetAt.After(start) {
		start = l.resetAt
		l.remaining = l.limit
		l.resetAt = start.Add(l.window)
	}
	if l.remaining > 0 {
		l.remaining--
	}
	l.next = start.Add(l.interval)
	l.mu.Unlock()

	if wait := time.Until(start); wait > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil
}

// observe folds the gateway's quota headers into the schedule. A header the
// response does not carry must not be read as a zero counter: an absent
// x-ratelimit-remaining would otherwise declare the budget spent and stall the
// limiter for a whole window.
func (l *limiter) observe(headers http.Header) {
	limit, window, remaining, reset, limitFound, remainingFound, resetFound := parseQuotaHeaders(headers)
	if !limitFound && !remainingFound && !resetFound {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if limitFound && limit > 0 {
		l.limit = limit
	}
	if window > 0 {
		l.window = window
	} else if l.window == 0 {
		l.window = defaultQuotaWindow
	}
	if remainingFound {
		l.remaining = remaining
	}
	if resetFound {
		l.resetAt = time.Now().Add(reset)
	} else if l.window > 0 {
		l.resetAt = time.Now().Add(l.window)
	}
}

// parseQuotaHeaders reads the x-ratelimit-* triple the gateway returns on every
// response. Each counter is reported separately so the caller can tell an absent
// header from a zero value.
func parseQuotaHeaders(headers http.Header) (limit int, window time.Duration, remaining int, reset time.Duration, limitFound, remainingFound, resetFound bool) {
	if value, w, found := parseQuotaValue(headers.Get("x-ratelimit-limit")); found {
		limit, window, limitFound = value, w, true
	}
	if value, _, found := parseQuotaValue(headers.Get("x-ratelimit-remaining")); found {
		remaining, remainingFound = value, true
	}
	if raw := headers.Get("x-ratelimit-reset"); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			if seconds > maxQuotaResetSeconds {
				seconds = maxQuotaResetSeconds
			}
			reset, resetFound = time.Duration(seconds)*time.Second, true
		}
	}
	return
}

// parseQuotaValue reads a rate-limit header that may hold several counters for
// different windows ("100;w=1, 5000;w=60"). It returns the tightest counter
// together with the window that counter belongs to.
func parseQuotaValue(value string) (int, time.Duration, bool) {
	if value == "" {
		return 0, 0, false
	}
	best, bestWindow := -1, time.Duration(0)
	for _, part := range strings.Split(value, ",") {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}
		countPart, windowPart := token, ""
		if i := strings.Index(token, ";"); i >= 0 {
			countPart, windowPart = strings.TrimSpace(token[:i]), strings.TrimSpace(token[i+1:])
		}
		count, err := strconv.Atoi(countPart)
		if err != nil {
			continue
		}
		window := time.Duration(0)
		if strings.HasPrefix(windowPart, "w=") {
			if seconds, err := strconv.Atoi(windowPart[2:]); err == nil && seconds > 0 {
				window = time.Duration(seconds) * time.Second
			}
		}
		if best < 0 || count < best || (count == best && bestWindow == 0 && window > 0) {
			best, bestWindow = count, window
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return best, bestWindow, true
}

// Client is a minimal T-Invest REST gateway client using only the standard
// library. The token lives only in the Authorization header and is never
// logged or serialised.
type Client struct {
	base    string
	token   string
	appName string
	http    *http.Client
	retries int
	delay   time.Duration
	log     Logf
	// Sandbox routes account/portfolio calls through SandboxService.
	Sandbox bool

	// limiterInterval is the pacing applied to every service group; limiters
	// are created lazily per service with the documented initial budget.
	limiterInterval time.Duration
	limiterMu       sync.Mutex
	limiters        map[string]*limiter

	instrCache *lookupCache[instrumentShort]
	bondCache  *lookupCache[bond]

	// unresolved counts instruments whose reference data could not be obtained;
	// each instrument is counted once, when its lookup fails for good. aliases
	// remembers which counter key a paper was recorded under, so a call that
	// carries only one identifier still reaches the record made through the other.
	unresolvedMu sync.Mutex
	unresolved   map[string]bool
	aliases      map[string]string
}

func (c *Client) limiterFor(service string) *limiter {
	c.limiterMu.Lock()
	defer c.limiterMu.Unlock()
	if l, ok := c.limiters[service]; ok {
		return l
	}
	budget := documentedQuota[service]
	if budget <= 0 {
		budget = 200
	}
	l := &limiter{interval: c.limiterInterval, limit: budget, remaining: budget, window: defaultQuotaWindow}
	c.limiters[service] = l
	return l
}

// Unresolved reports how many instruments the API could not resolve.
func (c *Client) Unresolved() int {
	c.unresolvedMu.Lock()
	defer c.unresolvedMu.Unlock()
	return len(c.unresolved)
}

// markUnresolved records an instrument under the identifier the call carried. If
// the same paper was already recorded under the other identifier, the existing
// key is reused, so a paper known by both a UID and a FIGI is counted once.
func (c *Client) markUnresolved(uid, figi string) {
	c.unresolvedMu.Lock()
	defer c.unresolvedMu.Unlock()
	if c.unresolved == nil {
		c.unresolved = map[string]bool{}
	}
	key := canonicalKey(uid, figi)
	if uid != "" {
		if a, ok := c.aliases[uid]; ok {
			key = a
		}
	}
	if figi != "" {
		if a, ok := c.aliases[figi]; ok {
			key = a
		}
	}
	c.unresolved[key] = true
	if uid != "" && figi != "" {
		if c.aliases == nil {
			c.aliases = map[string]string{}
		}
		c.aliases[uid] = key
		c.aliases[figi] = key
	}
}

// markResolved removes an instrument under both identifiers it is known by: a
// resolution reached through one identifier clears the record made through the
// other, so the counter reports instruments that stayed unresolved.
func (c *Client) markResolved(uid, figi string) {
	c.unresolvedMu.Lock()
	defer c.unresolvedMu.Unlock()
	if uid != "" {
		delete(c.unresolved, "uid:"+uid)
	}
	if figi != "" {
		delete(c.unresolved, "figi:"+figi)
	}
}

// cacheEntry is one memoized lookup: a value, or the error that produced it.
type cacheEntry[T any] struct {
	value *T
	err   error
}

// lookupCache memoizes reference-data responses, misses included: an instrument
// the catalog does not index must not be re-fetched for every operation that
// references it. Transient failures are not cached, so a later caller can still
// retry. Concurrent callers for the same key share a single request.
type lookupCache[T any] struct {
	mu       sync.Mutex
	entries  map[string]cacheEntry[T]
	inFlight map[string]chan cacheEntry[T]
}

func newLookupCache[T any]() *lookupCache[T] {
	return &lookupCache[T]{entries: map[string]cacheEntry[T]{}, inFlight: map[string]chan cacheEntry[T]{}}
}

func (l *lookupCache[T]) get(ctx context.Context, key string, fetch func(context.Context) (*T, error)) (*T, error) {
	l.mu.Lock()
	if e, ok := l.entries[key]; ok {
		l.mu.Unlock()
		return e.value, e.err
	}
	if ch, ok := l.inFlight[key]; ok {
		l.mu.Unlock()
		// The leader's result is handed over directly: a transient failure is
		// not memoized, so re-reading the map would show an empty entry that
		// the caller would read as a successful lookup.
		select {
		case e := <-ch:
			return e.value, e.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ch := make(chan cacheEntry[T], 1)
	l.inFlight[key] = ch
	l.mu.Unlock()

	v, err := fetch(ctx)
	e := cacheEntry[T]{value: v, err: err}
	l.mu.Lock()
	if err == nil || isPermanent(err) {
		l.entries[key] = e
	}
	delete(l.inFlight, key)
	l.mu.Unlock()
	ch <- e
	return v, err
}

// New builds a client. delay is the base linear backoff between attempts.
// caPEMPath is an optional path to a PEM file with trusted CA certificates.
// If non-empty, it is loaded and set as the TLS root CA pool.
// insecureSkipVerify controls whether the server certificate is verified.
func New(base, token, appName string, retries int, delay time.Duration, log Logf, caPEMPath string, insecureSkipVerify bool) (*Client, error) {
	if log == nil {
		log = func(string, ...any) {}
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{}}
	if caPEMPath != "" {
		caCert, err := os.ReadFile(caPEMPath)
		if err != nil {
			return nil, fmt.Errorf("не удалось прочитать CA-файл %q: %w", caPEMPath, err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("не удалось разобрать CA-файл %q: нет валидных PEM-сертификатов", caPEMPath)
		}
		transport.TLSClientConfig.RootCAs = caCertPool
	} else if insecureSkipVerify {
		transport.TLSClientConfig.InsecureSkipVerify = true
	}
	return &Client{
		base:            base,
		token:           token,
		appName:         appName,
		http:            &http.Client{Timeout: 30 * time.Second, Transport: transport},
		retries:         retries,
		delay:           delay,
		log:             log,
		limiterInterval: defaultLimiterInterval,
		limiters:        map[string]*limiter{},
		instrCache:      newLookupCache[instrumentShort](),
		bondCache:       newLookupCache[bond](),
	}, nil
}

// APIError describes a non-2xx response. It never contains the token.
type APIError struct {
	Status     int
	Service    string
	Method     string
	Body       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s/%s: HTTP %d: %s", e.Service, e.Method, e.Status, e.Body)
}

// Permanent reports an error that no retry can fix: the request is wrong or the
// resource does not exist. 429 stays retryable, and so do the transient 4xx
// codes 408 (Request Timeout) and 425 (Too Early).
func (e *APIError) Permanent() bool {
	if e.Status < 400 || e.Status >= 500 {
		return false
	}
	switch e.Status {
	case http.StatusTooManyRequests, http.StatusRequestTimeout, http.StatusTooEarly:
		return false
	}
	return true
}

func isPermanent(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Permanent()
}

// call invokes service/method with req and decodes the response into out.
// Permanent errors are returned immediately: retrying a "not found" or a bad
// request only burns the service quota. Transient failures are retried with
// exponential backoff, honouring Retry-After. Every attempt, including the
// successful ones, is paced by the service limiter.
func (c *Client) call(ctx context.Context, service, method string, req, out any) error {
	limiter := c.limiterFor(service)
	attempts := c.retries + 1
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := limiter.reserve(ctx); err != nil {
			return err
		}
		err := c.do(ctx, service, method, req, out)
		if err == nil {
			return nil
		}
		lastErr = err
		var api *APIError
		if errors.As(err, &api) && api.Permanent() {
			return err
		}
		if attempt == attempts {
			break
		}
		c.log("Ошибка при вызове %s/%s (попытка %d из %d): %v", service, method, attempt, attempts, err)
		wait := c.delay * time.Duration(1<<(attempt-1))
		if api != nil && api.RetryAfter > wait {
			wait = api.RetryAfter
		}
		wait += time.Duration(time.Now().UnixNano() % int64(max(c.delay/4, time.Millisecond)))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return fmt.Errorf("исчерпаны попытки (%d) для %s/%s: %w", attempts, service, method, lastErr)
}

func (c *Client) do(ctx context.Context, service, method string, req, out any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	url := c.base + "/" + contractPrefix + service + "/" + method
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("x-app-name", c.appName)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("network: %w", err)
	}
	defer resp.Body.Close()
	// The gateway reports the live quota on every response, success included.
	c.limiterFor(service).observe(resp.Header)

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retryAfter := time.Duration(0)
		if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && seconds > 0 {
			retryAfter = time.Duration(seconds) * time.Second
		}
		return &APIError{Status: resp.StatusCode, Service: service, Method: method, Body: string(respBody), RetryAfter: retryAfter}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode %s/%s response: %w", service, method, err)
	}
	return nil
}

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
	"sync/atomic"
	"time"
)

const contractPrefix = "tinkoff.public.invest.api.contract.v1."

// defaultLimiterInterval paces one request per service group. The documented
// InstrumentsService REST quota is 200 requests per 60-second window, so 320 ms
// keeps a small margin; the limiter additionally follows the quota headers the
// gateway returns on every response.
const defaultLimiterInterval = 320 * time.Millisecond

// documentedQuota is the per-service unary limit (requests per minute) from the
// API rate-limit policy. It is only the initial budget: the x-ratelimit-*
// headers override it as soon as a response arrives.
var documentedQuota = map[string]int{
	"InstrumentsService": 200,
	"OperationsService":  200,
	"MarketDataService":  200,
	"UsersService":       100,
	"SandboxService":     100,
}

// Logf logs a diagnostic line. The token is never passed to it.
type Logf func(format string, args ...any)

// limiter paces requests to one service group. A slot is reserved per request;
// once the API reports the window quota as exhausted, the next slot moves to
// the reported reset time.
type limiter struct {
	mu        sync.Mutex
	interval  time.Duration
	next      time.Time
	remaining int           // requests left in the window; unknown until observed
	reset     time.Duration // time until the window counter resets
}

func (l *limiter) reserve(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	start := l.next
	if start.Before(now) {
		start = now
	}
	if l.remaining <= 0 && l.reset > 0 {
		if until := now.Add(l.reset); until.After(start) {
			start = until
		}
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

// observe folds the gateway's quota headers into the schedule.
func (l *limiter) observe(headers http.Header) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limit, window, ok := parseQuotaValue(headers.Get("x-ratelimit-limit")); ok {
		l.remaining = limit
		if window > 0 {
			l.reset = window
		}
	}
	if remaining, window, ok := parseQuotaValue(headers.Get("x-ratelimit-remaining")); ok {
		l.remaining = remaining
		if window > 0 {
			l.reset = window
		}
	}
	if seconds, window, ok := parseQuotaValue(headers.Get("x-ratelimit-reset")); ok {
		if seconds > 0 {
			l.reset = time.Duration(seconds) * time.Second
		} else if window > 0 {
			l.reset = window
		}
	}
	if l.remaining <= 0 && l.reset > 0 {
		if until := time.Now().Add(l.reset); until.After(l.next) {
			l.next = until
		}
	}
}

// parseQuotaValue reads the first integer of a rate-limit header, which the
// gateway may emit as a comma-separated list of counters ("200, 200;w=60"),
// together with the window each counter belongs to. The tightest counter wins.
func parseQuotaValue(value string) (int, time.Duration, bool) {
	if value == "" {
		return 0, 0, false
	}
	value = strings.TrimPrefix(value, "w=")
	number, window := -1, time.Duration(0)
	for _, part := range strings.Split(value, ",") {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}
		if i := strings.Index(token, ";"); i >= 0 {
			if w, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(token[i+1:]), "w=")); err == nil && w > 0 {
				window = time.Duration(w) * time.Second
			}
			token = strings.TrimSpace(token[:i])
		}
		v, err := strconv.Atoi(token)
		if err != nil {
			continue
		}
		if number < 0 || v < number {
			number = v
		}
	}
	if number < 0 {
		return 0, 0, false
	}
	return number, window, true
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

	// unresolved counts instruments the API could not resolve at all; each
	// instrument is counted once, when its first lookup fails permanently.
	unresolved atomic.Int32
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
	l := &limiter{interval: c.limiterInterval, remaining: budget, reset: time.Minute}
	c.limiters[service] = l
	return l
}

// Unresolved reports how many instruments the API never resolved.
func (c *Client) Unresolved() int { return int(c.unresolved.Load()) }

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
	inFlight map[string]chan struct{}
}

func newLookupCache[T any]() *lookupCache[T] {
	return &lookupCache[T]{entries: map[string]cacheEntry[T]{}, inFlight: map[string]chan struct{}{}}
}

func (l *lookupCache[T]) get(ctx context.Context, key string, fetch func(context.Context) (*T, error)) (value *T, err error, fetched bool) {
	l.mu.Lock()
	if e, ok := l.entries[key]; ok {
		l.mu.Unlock()
		return e.value, e.err, false
	}
	if ch, ok := l.inFlight[key]; ok {
		l.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err(), false
		}
		l.mu.Lock()
		e := l.entries[key]
		l.mu.Unlock()
		return e.value, e.err, false
	}
	ch := make(chan struct{})
	l.inFlight[key] = ch
	l.mu.Unlock()

	v, err := fetch(ctx)
	l.mu.Lock()
	if err == nil || isPermanent(err) {
		l.entries[key] = cacheEntry[T]{value: v, err: err}
	}
	delete(l.inFlight, key)
	close(ch)
	l.mu.Unlock()
	return v, err, true
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
// resource does not exist. Only 429 among the 4xx family is worth retrying.
func (e *APIError) Permanent() bool {
	return e.Status >= 400 && e.Status < 500 && e.Status != http.StatusTooManyRequests
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
		api, ok := err.(*APIError)
		if ok && api.Permanent() {
			return err
		}
		if attempt == attempts {
			break
		}
		c.log("Ошибка при вызове %s/%s (попытка %d из %d): %v", service, method, attempt, attempts, err)
		wait := c.delay * time.Duration(1<<(attempt-1))
		if ok && api.RetryAfter > wait {
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

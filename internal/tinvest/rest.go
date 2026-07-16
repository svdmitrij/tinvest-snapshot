package tinvest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const contractPrefix = "tinkoff.public.invest.api.contract.v1."

// Logf logs a diagnostic line. The token is never passed to it.
type Logf func(format string, args ...any)

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
	Sandbox         bool
	instrCacheMu    sync.RWMutex
	instrShortCache map[string]*instrumentShort
	inCoalesceMu    sync.Mutex
	inCoalesce      map[string]chan struct{}
	bondCacheMu     sync.RWMutex
	bondShortCache  map[string]*bond
	bCoalesceMu     sync.Mutex
	bCoalesce       map[string]chan struct{}
	cooldownMu      sync.Mutex
	cooldownUntil   time.Time
	nextEnrichment  time.Time
}

func (c *Client) enrichmentCall(ctx context.Context, service, method string, req, out any) error {
	attempts := c.retries + 1
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := c.waitEnrichmentCooldown(ctx); err != nil {
			return err
		}
		err := c.do(ctx, service, method, req, out)
		if err == nil {
			return nil
		}
		lastErr = err
		c.log("Ошибка при вызове %s/%s (попытка %d из %d): %v", service, method, attempt, attempts, err)
		if api, ok := err.(*APIError); ok && api.RetryAfter > 0 {
			c.extendEnrichmentCooldown(api.RetryAfter)
		}
		if attempt == attempts {
			break
		}
		wait := c.delay * time.Duration(1<<(attempt-1))
		if api, ok := err.(*APIError); ok && api.RetryAfter > 0 {
			wait = 0
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

func (c *Client) waitEnrichmentCooldown(ctx context.Context) error {
	c.cooldownMu.Lock()
	now := time.Now()
	start := time.Time{}
	if c.cooldownUntil.After(now) {
		start = c.cooldownUntil
		if c.nextEnrichment.After(start) {
			start = c.nextEnrichment
		}
		c.nextEnrichment = start.Add(150 * time.Millisecond)
	}
	c.cooldownMu.Unlock()
	if start.IsZero() {
		return nil
	}
	wait := time.Until(start)
	if wait > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil
}

func (c *Client) extendEnrichmentCooldown(delay time.Duration) {
	c.cooldownMu.Lock()
	defer c.cooldownMu.Unlock()
	until := time.Now().Add(delay)
	if until.After(c.cooldownUntil) {
		c.cooldownUntil, c.nextEnrichment = until, until
	}
}

// New builds a client. delay is the base linear backoff between attempts.
func New(base, token, appName string, retries int, delay time.Duration, log Logf) *Client {
	if log == nil {
		log = func(string, ...any) {}
	}
	return &Client{
		base:            base,
		token:           token,
		appName:         appName,
		http:            &http.Client{Timeout: 30 * time.Second},
		retries:         retries,
		delay:           delay,
		log:             log,
		instrShortCache: map[string]*instrumentShort{},
		inCoalesce:      map[string]chan struct{}{},
		bondShortCache:  map[string]*bond{},
		bCoalesce:       map[string]chan struct{}{},
	}
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

// call invokes service/method with req and decodes the response into out.
// It retries transient failures up to the configured number of attempts.
func (c *Client) call(ctx context.Context, service, method string, req, out any) error {
	attempts := c.retries + 1
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		err := c.do(ctx, service, method, req, out)
		if err == nil {
			return nil
		}
		lastErr = err
		c.log("Ошибка при вызове %s/%s (попытка %d из %d): %v", service, method, attempt, attempts, err)
		if attempt < attempts {
			wait := c.delay * time.Duration(1<<(attempt-1))
			if api, ok := err.(*APIError); ok && api.RetryAfter > wait {
				wait = api.RetryAfter
			}
			wait += time.Duration(time.Now().UnixNano() % int64(max(c.delay/4, time.Millisecond)))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
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

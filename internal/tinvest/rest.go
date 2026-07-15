package tinvest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	Sandbox          bool
	instrCacheMu     sync.RWMutex
	instrShortCache  map[string]*instrumentShort
	bondCacheMu      sync.RWMutex
	bondShortCache   map[string]*bond
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
		bondShortCache:  map[string]*bond{},
	}
}

// APIError describes a non-2xx response. It never contains the token.
type APIError struct {
	Status  int
	Service string
	Method  string
	Body    string
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
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.delay * time.Duration(attempt)):
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
		return &APIError{Status: resp.StatusCode, Service: service, Method: method, Body: string(respBody)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode %s/%s response: %w", service, method, err)
	}
	return nil
}

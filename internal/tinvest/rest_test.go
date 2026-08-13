package tinvest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCallRetriesThenFails(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, err := New(srv.URL, "tok", "test", 2, time.Millisecond, nil, "", false) // 2 retries => 3 attempts
	if err != nil {
		t.Fatal(err)
	}
	err = c.call(context.Background(), "UsersService", "GetAccounts", struct{}{}, &getAccountsResponse{})
	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestCallSendsBearerTokenInHeaderOnly(t *testing.T) {
	const token = "super-secret-token"
	var sawHeader, sawInBody bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeader = r.Header.Get("Authorization") == "Bearer "+token
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		sawInBody = strings.Contains(string(buf), token)
		w.Write([]byte(`{"accounts":[]}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL, token, "test", 0, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Accounts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !sawHeader {
		t.Error("token was not sent in the Authorization header")
	}
	if sawInBody {
		t.Error("token leaked into the request body")
	}
}

func TestCallSucceedsAfterTransientError(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"accounts":[]}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL, "tok", "test", 3, time.Millisecond, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Accounts(context.Background()); err != nil {
		t.Fatalf("expected success on retry, got %v", err)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Errorf("attempts = %d, want 2", atomic.LoadInt32(&hits))
	}
}

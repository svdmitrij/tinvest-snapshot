package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaults(t *testing.T) {
	p := writeTemp(t, `{"token_env":"TINVEST_TOKEN"}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Mode != ModeSandbox {
		t.Errorf("default mode = %q, want sandbox", c.Mode)
	}
	if c.RetryDelayMs != 1000 {
		t.Errorf("default retry delay = %d, want 1000", c.RetryDelayMs)
	}
	if c.BaseURL() != endpointSandbox {
		t.Errorf("BaseURL = %q, want %q", c.BaseURL(), endpointSandbox)
	}
}

func TestValidateRejectsBadMode(t *testing.T) {
	p := writeTemp(t, `{"mode":"prod-ish","token":"x"}`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestEmptyTokenSourceDefaultsToEnv(t *testing.T) {
	// With neither token nor token_env, the loader falls back to the
	// TINVEST_TOKEN environment variable rather than failing.
	p := writeTemp(t, `{"mode":"prod","token":"","token_env":""}`)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.TokenEnv != "TINVEST_TOKEN" {
		t.Errorf("TokenEnv = %q, want TINVEST_TOKEN", c.TokenEnv)
	}
}

func TestResolveTokenErrorsWhenEnvEmpty(t *testing.T) {
	t.Setenv("TINVEST_TOKEN", "")
	p := writeTemp(t, `{"mode":"prod","token_env":"TINVEST_TOKEN"}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResolveToken(); err == nil {
		t.Fatal("expected error when env var is empty and no inline token")
	}
}

func TestResolveTokenFromEnv(t *testing.T) {
	t.Setenv("TINVEST_TOKEN", "secret-value")
	p := writeTemp(t, `{"mode":"sandbox","token_env":"TINVEST_TOKEN"}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := c.ResolveToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok != "secret-value" {
		t.Errorf("ResolveToken = %q", tok)
	}
}

func TestBaseURLProd(t *testing.T) {
	p := writeTemp(t, `{"mode":"prod","token":"x"}`)
	c, _ := Load(p)
	if c.BaseURL() != endpointProd {
		t.Errorf("BaseURL = %q, want %q", c.BaseURL(), endpointProd)
	}
}

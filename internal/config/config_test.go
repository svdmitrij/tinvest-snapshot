package config

import (
	"os"
	"path/filepath"
	"strconv"
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

func TestCatalogTTLNormalizesOnlyNonPositiveValues(t *testing.T) {
	for _, tc := range []struct{ raw, want int }{{0, 24}, {-1, 24}, {1, 1}, {1000000, 1000000}} {
		p := writeTemp(t, `{"token":"x","catalog_ttl_hours":`+strconv.Itoa(tc.raw)+`}`)
		c, err := Load(p)
		if err != nil || c.CatalogTTLHours != tc.want {
			t.Fatalf("ttl %d: got %+v, err %v", tc.raw, c, err)
		}
	}
}

func TestInstrumentLoadTimeoutNormalizesOnlyNonPositiveValues(t *testing.T) {
	p := writeTemp(t, `{"token":"x"}`)
	c, err := Load(p)
	if err != nil || c.InstrumentLoadTimeoutSeconds != 300 {
		t.Fatalf("absent timeout: got %+v, err %v", c, err)
	}
	for _, tc := range []struct{ raw, want int }{{0, 300}, {-1, 300}, {1, 1}, {600, 600}} {
		p := writeTemp(t, `{"token":"x","instrument_load_timeout_seconds":`+strconv.Itoa(tc.raw)+`}`)
		c, err := Load(p)
		if err != nil || c.InstrumentLoadTimeoutSeconds != tc.want {
			t.Fatalf("timeout %d: got %+v, err %v", tc.raw, c, err)
		}
	}
}

func TestInstrumentLoadTimeoutRejectsIncompatibleJSONType(t *testing.T) {
	p := writeTemp(t, `{"token":"x","instrument_load_timeout_seconds":"fast"}`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected config error for a non-integer JSON value")
	}
}

func TestSaveRestoresIndependentScales(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	c := &Config{Mode: ModeSandbox, Token: "x", FontScalePortfolio: 80, FontScaleOperations: 120, FontScaleInstruments: 150}
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || got.FontScalePortfolio != 80 || got.FontScaleOperations != 120 || got.FontScaleInstruments != 150 {
		t.Fatalf("scales = %#v, %v", got, err)
	}
}

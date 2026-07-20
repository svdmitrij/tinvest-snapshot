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

func TestInstrumentLoadTimeoutRange(t *testing.T) {
	p := writeTemp(t, `{"token":"x"}`)
	c, err := Load(p)
	if err != nil || c.InstrumentLoadTimeoutSeconds != 600 {
		t.Fatalf("absent timeout: got %+v, err %v", c, err)
	}
	for _, tc := range []struct{ raw, want int }{{0, 600}, {-1, 600}, {1, 1}, {10, 10}, {30, 30}, {600, 600}, {3600, 3600}, {3601, 600}} {
		p := writeTemp(t, `{"token":"x","instrument_load_timeout_seconds":`+strconv.Itoa(tc.raw)+`}`)
		c, err := Load(p)
		if err != nil || c.InstrumentLoadTimeoutSeconds != tc.want {
			t.Fatalf("timeout %d: got %d want %d, err %v", tc.raw, c.InstrumentLoadTimeoutSeconds, tc.want, err)
		}
	}
}

func TestPortfolioLoadTimeoutRange(t *testing.T) {
	p := writeTemp(t, `{"token":"x"}`)
	c, err := Load(p)
	if err != nil || c.PortfolioLoadTimeoutSeconds != 180 {
		t.Fatalf("absent portfolio timeout: got %+v, err %v", c, err)
	}
	for _, tc := range []struct{ raw, want int }{{0, 180}, {4, 180}, {5, 5}, {180, 180}, {3601, 180}} {
		p := writeTemp(t, `{"token":"x","portfolio_load_timeout_seconds":`+strconv.Itoa(tc.raw)+`}`)
		c, err := Load(p)
		if err != nil || c.PortfolioLoadTimeoutSeconds != tc.want {
			t.Fatalf("portfolio timeout %d: got %d want %d, err %v", tc.raw, c.PortfolioLoadTimeoutSeconds, tc.want, err)
		}
	}
}

func TestDividendLoadTimeoutRange(t *testing.T) {
	p := writeTemp(t, `{"token":"x"}`)
	c, err := Load(p)
	if err != nil || c.DividendLoadTimeoutSeconds != 900 {
		t.Fatalf("absent timeout: got %+v, err %v", c, err)
	}
	for _, tc := range []struct{ raw, want int }{{0, 900}, {-1, 900}, {29, 900}, {30, 30}, {900, 900}, {3600, 3600}, {3601, 900}} {
		p := writeTemp(t, `{"token":"x","dividend_load_timeout_seconds":`+strconv.Itoa(tc.raw)+`}`)
		c, err := Load(p)
		if err != nil || c.DividendLoadTimeoutSeconds != tc.want {
			t.Fatalf("dividend timeout %d: got %d want %d, err %v", tc.raw, c.DividendLoadTimeoutSeconds, tc.want, err)
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

// Package config loads and validates the utility configuration.
// Configuration is JSON to keep the binary free of external dependencies.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ModeProd    = "prod"
	ModeSandbox = "sandbox"

	endpointProd    = "https://invest-public-api.tinkoff.ru/rest"
	endpointSandbox = "https://sandbox-invest-public-api.tinkoff.ru/rest"
)

// Config is the on-disk configuration (see config.example.json).
type Config struct {
	// Mode selects the API environment: "prod" or "sandbox".
	Mode string `json:"mode"`
	// Token is the read-only API token. Prefer TokenEnv over storing it here.
	Token string `json:"token"`
	// TokenEnv names an environment variable to read the token from.
	TokenEnv string `json:"token_env"`
	// Retries is the number of extra attempts on transient API errors.
	Retries int `json:"retries"`
	// RetryDelayMs is the base delay between attempts (linear backoff).
	RetryDelayMs int `json:"retry_delay_ms"`
	// ReportsDir is where JSON/CSV snapshots are written.
	ReportsDir string `json:"reports_dir"`
	// TargetCurrency is the default currency for converted totals ("" = none).
	TargetCurrency string `json:"target_currency"`
	// Endpoint optionally overrides the API base URL for the selected mode.
	Endpoint string `json:"endpoint"`
	// AppName is sent in the x-app-name header for API analytics.
	AppName string `json:"app_name"`
	// GUI-only preferences are ignored by the CLI and keep the file backwards compatible.
	AutoRefreshMinutes int    `json:"auto_refresh_minutes,omitempty"`
	CatalogTTLHours    int    `json:"catalog_ttl_hours,omitempty"`
	// InstrumentLoadTimeoutSeconds bounds the full network bond load
	// (catalog, mandatory enrichment and result assembly), in whole seconds.
	// Default 600, range 30–3600.
	InstrumentLoadTimeoutSeconds int `json:"instrument_load_timeout_seconds,omitempty"`
	// DividendLoadTimeoutSeconds bounds the demand-driven dividend enrichment
	// for shares, in whole seconds. Default 900, range 30–3600.
	DividendLoadTimeoutSeconds int    `json:"dividend_load_timeout_seconds,omitempty"`
	Language                   string `json:"language,omitempty"`
	// TimezoneOffset is nil when the field is absent from the config (defaults
	// to +4 in that case).  An explicit UTC+0 is stored as a pointer to 0,
	// distinct from absent.
	TimezoneOffset *int `json:"timezone_offset,omitempty"`
	// FontScalePortfolio sets the font scale for the Portfolio tab in percent (60-200, default 100).
	FontScalePortfolio int `json:"font_scale_portfolio,omitempty"`
	// FontScaleOperations sets the font scale for the Operations tab in percent (60-200, default 100).
	FontScaleOperations int `json:"font_scale_operations,omitempty"`
	// FontScaleInstruments sets the font scale for the Instruments tab in percent (60-200, default 100).
	FontScaleInstruments int `json:"font_scale_instruments,omitempty"`
}

// Load reads and validates the configuration from path, applying defaults.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать конфиг %q: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("не удалось разобрать конфиг %q: %w", path, err)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
	if c.Mode == "" {
		c.Mode = ModeSandbox
	}
	if c.Retries < 0 {
		c.Retries = 0
	}
	if c.RetryDelayMs <= 0 {
		c.RetryDelayMs = 1000
	}
	if c.AppName == "" {
		c.AppName = "tinvest-snapshot"
	}
	if c.CatalogTTLHours <= 0 {
		c.CatalogTTLHours = 24
	}
	// Clamp both timeouts to [30, 3600]; 0, negative, and out-of-range → default.
	if c.InstrumentLoadTimeoutSeconds < 30 || c.InstrumentLoadTimeoutSeconds > 3600 {
		c.InstrumentLoadTimeoutSeconds = 600
	}
	if c.DividendLoadTimeoutSeconds < 30 || c.DividendLoadTimeoutSeconds > 3600 {
		c.DividendLoadTimeoutSeconds = 900
	}
	if c.Language == "" {
		c.Language = "ru"
	}
	if c.TimezoneOffset == nil {
		def := 4
		c.TimezoneOffset = &def
	}
	if *c.TimezoneOffset < -23 || *c.TimezoneOffset > 23 {
		def := 4
		c.TimezoneOffset = &def
	}
	clampScale := func(v *int) {
		if *v == 0 {
			*v = 100
		}
		if *v < 60 {
			*v = 60
		}
		if *v > 200 {
			*v = 200
		}
	}
	clampScale(&c.FontScalePortfolio)
	clampScale(&c.FontScaleOperations)
	clampScale(&c.FontScaleInstruments)
	if c.TokenEnv == "" && c.Token == "" {
		c.TokenEnv = "TINVEST_TOKEN"
	}
	if c.ReportsDir == "" {
		c.ReportsDir = defaultReportsDir()
	}
	c.TargetCurrency = strings.ToLower(strings.TrimSpace(c.TargetCurrency))
}

// Save writes configuration atomically. It deliberately does not resolve or
// otherwise copy a token from the environment.
func (c *Config) Save(path string) error {
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("не удалось сериализовать конфиг: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(append(raw, '\n')); err == nil {
		err = tmp.Close()
	} else {
		_ = tmp.Close()
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (c *Config) validate() error {
	if c.Mode != ModeProd && c.Mode != ModeSandbox {
		return fmt.Errorf("недопустимый режим %q (ожидается %q или %q)", c.Mode, ModeProd, ModeSandbox)
	}
	if c.Token == "" && c.TokenEnv == "" {
		return fmt.Errorf("не задан источник токена: укажите token или token_env")
	}
	return nil
}

// ResolveToken returns the API token from the configured source.
// The token is never logged or written anywhere by this package.
func (c *Config) ResolveToken() (string, error) {
	if c.TokenEnv != "" {
		if v := strings.TrimSpace(os.Getenv(c.TokenEnv)); v != "" {
			return v, nil
		}
		if c.Token == "" {
			return "", fmt.Errorf("переменная окружения %s пуста и inline-токен не задан", c.TokenEnv)
		}
	}
	if c.Token != "" {
		return c.Token, nil
	}
	return "", fmt.Errorf("не удалось получить токен")
}

// BaseURL returns the API base URL for the configured mode.
func (c *Config) BaseURL() string {
	if c.Endpoint != "" {
		return strings.TrimRight(c.Endpoint, "/")
	}
	if c.Mode == ModeSandbox {
		return endpointSandbox
	}
	return endpointProd
}

// Sandbox reports whether the sandbox environment is selected.
func (c *Config) Sandbox() bool { return c.Mode == ModeSandbox }

func defaultReportsDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "reports"
	}
	return filepath.Join(filepath.Dir(exe), "reports")
}

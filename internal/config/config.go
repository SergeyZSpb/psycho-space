// Package config loads all runtime configuration from PSYCHOSPACE_* environment
// variables. Secrets have no defaults — the process fails fast if they are unset
// or malformed, so a misconfigured deploy never starts serving.
package config

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config is the fully-resolved application configuration.
type Config struct {
	Env      string `env:"PSYCHOSPACE_ENV" envDefault:"dev"`
	HTTPAddr string `env:"PSYCHOSPACE_HTTP_ADDR" envDefault:"127.0.0.1:8080"`
	BaseURL  string `env:"PSYCHOSPACE_BASE_URL" envDefault:"http://localhost:8080"`
	LogDir   string `env:"PSYCHOSPACE_LOG_DIR" envDefault:""` // empty -> stdout only

	// OTLPEndpoint enables trace export when set (e.g. localhost:4318). Traces
	// are always generated; leaving this empty means "generate but don't export".
	OTLPEndpoint string `env:"PSYCHOSPACE_OTLP_ENDPOINT" envDefault:""`

	DatabaseURL string `env:"PSYCHOSPACE_DATABASE_URL,required"`

	SessionTTL time.Duration `env:"PSYCHOSPACE_SESSION_TTL" envDefault:"720h"` // 30 days

	VK VK

	LLM LLM

	// Base64-encoded 32-byte keys (required, no defaults).
	EncKeyB64     string `env:"PSYCHOSPACE_ENC_KEY,required"`
	HMACKeyB64    string `env:"PSYCHOSPACE_HMAC_KEY,required"`
	SessionKeyB64 string `env:"PSYCHOSPACE_SESSION_KEY,required"`

	// Decoded key material (populated by Load; never sourced from env directly).
	EncKey     []byte `env:"-"`
	HMACKey    []byte `env:"-"`
	SessionKey []byte `env:"-"`
}

// VK holds VK ID (id.vk.ru) integration settings. These are intentionally NOT
// required so the walking-skeleton build can deploy before the VK secrets exist;
// the auth handler refuses to run until VK.Configured() is true.
type VK struct {
	AppID        string `env:"PSYCHOSPACE_VK_APP_ID" envDefault:""`
	ServiceToken string `env:"PSYCHOSPACE_VK_SERVICE_TOKEN" envDefault:""`
	RedirectURI  string `env:"PSYCHOSPACE_VK_REDIRECT_URI" envDefault:""`
	// BaseURL is the VK ID API base; overridable in tests. Empty -> production.
	BaseURL string `env:"PSYCHOSPACE_VK_BASE_URL" envDefault:"https://id.vk.ru"`

	// VerifyIDToken turns on OIDC id_token signature verification (via JWKS).
	// Off by default so go-live isn't blocked on confirming VK's exact JWKS URL;
	// enable once JWKSURL is confirmed against the live app.
	VerifyIDToken bool   `env:"PSYCHOSPACE_VK_VERIFY_IDTOKEN" envDefault:"false"`
	JWKSURL       string `env:"PSYCHOSPACE_VK_JWKS_URL" envDefault:""`
	Issuer        string `env:"PSYCHOSPACE_VK_ISSUER" envDefault:""`
}

// Configured reports whether the VK integration has the secrets it needs.
func (v VK) Configured() bool {
	return v.AppID != "" && v.ServiceToken != "" && v.RedirectURI != ""
}

// LLM holds the OpenAI-compatible chat API used to drive AI characters (Yandex
// Cloud, DeepSeek). Optional: when unset the game falls back to the mock
// evaluator, so the loop is fully playable before credentials exist.
type LLM struct {
	BaseURL string `env:"PSYCHOSPACE_LLM_BASE_URL" envDefault:""`
	APIKey  string `env:"PSYCHOSPACE_LLM_API_KEY" envDefault:""`
	Model   string `env:"PSYCHOSPACE_LLM_MODEL" envDefault:"deepseek-4-pro"`
}

// Enabled reports whether a real LLM endpoint is configured. When false, the
// game uses the mock evaluator.
func (l LLM) Enabled() bool { return l.BaseURL != "" && l.APIKey != "" }

// IsProd reports whether we are running in the production environment.
func (c Config) IsProd() bool { return c.Env == "prod" }

// CookieSecure reports whether session cookies must carry the Secure flag.
// Always true except in the dev environment (so localhost http works).
func (c Config) CookieSecure() bool { return c.Env != "dev" }

// Load parses the environment, decodes and validates key material, and returns
// the resolved config. It returns an error (never panics) so tests can use it.
func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, fmt.Errorf("config: parse env: %w", err)
	}
	if cfg.EncKey, err = decodeKey("PSYCHOSPACE_ENC_KEY", cfg.EncKeyB64, 32); err != nil {
		return Config{}, err
	}
	if cfg.HMACKey, err = decodeKey("PSYCHOSPACE_HMAC_KEY", cfg.HMACKeyB64, 32); err != nil {
		return Config{}, err
	}
	if cfg.SessionKey, err = decodeKey("PSYCHOSPACE_SESSION_KEY", cfg.SessionKeyB64, 32); err != nil {
		return Config{}, err
	}
	if cfg.VK.VerifyIDToken && cfg.VK.JWKSURL == "" {
		return Config{}, fmt.Errorf("config: PSYCHOSPACE_VK_VERIFY_IDTOKEN is on but PSYCHOSPACE_VK_JWKS_URL is empty")
	}
	return cfg, nil
}

// MustLoad is Load but panics on error — used by main at startup.
func MustLoad() Config {
	cfg, err := Load()
	if err != nil {
		panic(err)
	}
	return cfg
}

// decodeKey base64-decodes a key and enforces an exact byte length. We never
// hash or stretch a passphrase into a key — callers must supply real 32-byte
// random material (e.g. `openssl rand -base64 32`).
func decodeKey(name, b64 string, wantLen int) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("config: %s is not valid base64: %w", name, err)
	}
	if len(raw) != wantLen {
		return nil, fmt.Errorf("config: %s must decode to exactly %d bytes, got %d", name, wantLen, len(raw))
	}
	return raw, nil
}

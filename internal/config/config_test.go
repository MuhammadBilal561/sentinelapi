package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func lookupFrom(env map[string]string) LookupFunc {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

func TestLoadDefaults(t *testing.T) {
	got, err := Load(lookupFrom(nil))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := Config{
		HTTPAddr:          "127.0.0.1:8080",
		LogLevel:          slog.LevelInfo,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ShutdownTimeout:   10 * time.Second,
	}
	if got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadEmptyValuesKeepDefaults(t *testing.T) {
	got, err := Load(lookupFrom(map[string]string{
		EnvHTTPAddr: "", EnvLogLevel: "   ", EnvReadTimeout: "",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != Defaults() {
		t.Fatalf("Load() = %+v, want defaults %+v", got, Defaults())
	}
}

func TestLoadOverrides(t *testing.T) {
	got, err := Load(lookupFrom(map[string]string{
		EnvHTTPAddr:          "0.0.0.0:9090",
		EnvLogLevel:          "DEBUG",
		EnvReadHeaderTimeout: "1s",
		EnvReadTimeout:       "2s",
		EnvWriteTimeout:      "3s",
		EnvIdleTimeout:       "4m",
		EnvShutdownTimeout:   "500ms",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := Config{
		HTTPAddr:          "0.0.0.0:9090",
		LogLevel:          slog.LevelDebug,
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       4 * time.Minute,
		ShutdownTimeout:   500 * time.Millisecond,
	}
	if got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadAcceptsValidAddresses(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1:8080", ":8080", "localhost:3000", "[::1]:8080",
		"my-host.example.com:443", "127.0.0.1:0",
	} {
		t.Run(addr, func(t *testing.T) {
			if _, err := Load(lookupFrom(map[string]string{EnvHTTPAddr: addr})); err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
		})
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantVar string
	}{
		{"address without port", map[string]string{EnvHTTPAddr: "127.0.0.1"}, EnvHTTPAddr},
		{"address with text port", map[string]string{EnvHTTPAddr: "127.0.0.1:http"}, EnvHTTPAddr},
		{"address port too large", map[string]string{EnvHTTPAddr: "127.0.0.1:70000"}, EnvHTTPAddr},
		{"address negative port", map[string]string{EnvHTTPAddr: "127.0.0.1:-1"}, EnvHTTPAddr},
		{"address bad host", map[string]string{EnvHTTPAddr: "bad host:8080"}, EnvHTTPAddr},
		{"address with url", map[string]string{EnvHTTPAddr: "http://127.0.0.1:8080"}, EnvHTTPAddr},
		{"unknown log level", map[string]string{EnvLogLevel: "verbose"}, EnvLogLevel},
		{"numeric log level", map[string]string{EnvLogLevel: "INFO+2"}, EnvLogLevel},
		{"malformed read header timeout", map[string]string{EnvReadHeaderTimeout: "soon"}, EnvReadHeaderTimeout},
		{"zero read timeout", map[string]string{EnvReadTimeout: "0s"}, EnvReadTimeout},
		{"negative write timeout", map[string]string{EnvWriteTimeout: "-5s"}, EnvWriteTimeout},
		{"duration without unit", map[string]string{EnvIdleTimeout: "30"}, EnvIdleTimeout},
		{"zero shutdown timeout", map[string]string{EnvShutdownTimeout: "0"}, EnvShutdownTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(lookupFrom(tt.env))
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantVar) {
				t.Fatalf("error %q does not name %s", err, tt.wantVar)
			}
		})
	}
}

func TestLoadReportsAllProblemsAtOnce(t *testing.T) {
	_, err := Load(lookupFrom(map[string]string{
		EnvHTTPAddr:    "nonsense",
		EnvLogLevel:    "loud",
		EnvReadTimeout: "-1s",
	}))
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	for _, name := range []string{EnvHTTPAddr, EnvLogLevel, EnvReadTimeout} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not mention %s", err, name)
		}
	}
}

func TestLoadReturnsZeroConfigOnError(t *testing.T) {
	cfg, err := Load(lookupFrom(map[string]string{EnvLogLevel: "loud"}))
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if cfg != (Config{}) {
		t.Fatalf("Load() config = %+v, want zero value on error", cfg)
	}
}

func TestLoadIgnoresUnrelatedVariables(t *testing.T) {
	const secret = "super-secret-token-value"
	_, err := Load(lookupFrom(map[string]string{
		"API_KEY":   secret,
		EnvLogLevel: "loud",
	}))
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked an unrelated variable: %q", err)
	}
}

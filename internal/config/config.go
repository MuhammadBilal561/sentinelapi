// Package config loads and validates SentinelAPI runtime configuration from
// environment variables.
//
// Configuration is read once at startup. Invalid configuration is reported
// together (all problems at once) so the process can refuse to start.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Environment variable names.
const (
	EnvHTTPAddr          = "HTTP_ADDR"
	EnvLogLevel          = "LOG_LEVEL"
	EnvReadHeaderTimeout = "HTTP_READ_HEADER_TIMEOUT"
	EnvReadTimeout       = "HTTP_READ_TIMEOUT"
	EnvWriteTimeout      = "HTTP_WRITE_TIMEOUT"
	EnvIdleTimeout       = "HTTP_IDLE_TIMEOUT"
	EnvShutdownTimeout   = "SHUTDOWN_TIMEOUT"
)

// Config is the validated runtime configuration.
type Config struct {
	HTTPAddr          string
	LogLevel          slog.Level
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

// LookupFunc looks up an environment variable, like os.LookupEnv.
type LookupFunc func(key string) (string, bool)

// Defaults returns the documented default configuration.
func Defaults() Config {
	return Config{
		HTTPAddr:          "127.0.0.1:8080",
		LogLevel:          slog.LevelInfo,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ShutdownTimeout:   10 * time.Second,
	}
}

// Load builds a Config from the environment. A variable that is unset, empty
// or only whitespace keeps its default. Every invalid value is reported; the
// returned error joins all of them. Load never reads or reports variables it
// does not own.
func Load(lookup LookupFunc) (Config, error) {
	cfg := Defaults()
	var errs []error

	if v, ok := value(lookup, EnvHTTPAddr); ok {
		if err := validateAddr(v); err != nil {
			errs = append(errs, fmt.Errorf("invalid %s %q: %w", EnvHTTPAddr, v, err))
		} else {
			cfg.HTTPAddr = v
		}
	}

	if v, ok := value(lookup, EnvLogLevel); ok {
		level, err := parseLogLevel(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("invalid %s %q: %w", EnvLogLevel, v, err))
		} else {
			cfg.LogLevel = level
		}
	}

	durations := []struct {
		name string
		dst  *time.Duration
	}{
		{EnvReadHeaderTimeout, &cfg.ReadHeaderTimeout},
		{EnvReadTimeout, &cfg.ReadTimeout},
		{EnvWriteTimeout, &cfg.WriteTimeout},
		{EnvIdleTimeout, &cfg.IdleTimeout},
		{EnvShutdownTimeout, &cfg.ShutdownTimeout},
	}
	for _, d := range durations {
		v, ok := value(lookup, d.name)
		if !ok {
			continue
		}
		parsed, err := parsePositiveDuration(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("invalid %s %q: %w", d.name, v, err))
			continue
		}
		*d.dst = parsed
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// value returns the trimmed value of key and whether it is set and non-empty.
func value(lookup LookupFunc, key string) (string, bool) {
	v, ok := lookup(key)
	v = strings.TrimSpace(v)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

func parseLogLevel(v string) (slog.Level, error) {
	switch strings.ToLower(v) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, errors.New("must be one of: debug, info, warn, error")
}

func parsePositiveDuration(v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, errors.New("not a valid duration (examples: 500ms, 5s, 2m)")
	}
	if d <= 0 {
		return 0, errors.New("must be greater than zero")
	}
	return d, nil
}

// validateAddr checks a host:port listen address. The host may be empty
// (all interfaces), an IP address or a hostname. Port 0 asks the operating
// system for a free port.
func validateAddr(v string) error {
	host, port, err := net.SplitHostPort(v)
	if err != nil {
		return errors.New("must be in host:port form, for example 127.0.0.1:8080")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return errors.New("port must be a number between 0 and 65535")
	}
	if host != "" && !validHost(host) {
		return errors.New("host is not a valid IP address or hostname")
	}
	return nil
}

func validHost(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	return validHostname(host)
}

func validHostname(host string) bool {
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			isAlnum := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
			if !isAlnum && r != '-' {
				return false
			}
		}
	}
	return true
}

package config

import (
	"strings"
	"testing"
)

func TestToolRegistryPathDefault(t *testing.T) {
	cfg, err := Load(lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ToolRegistryPath != DefaultToolRegistryPath || DefaultToolRegistryPath != "examples/config/tools.json" {
		t.Fatalf("ToolRegistryPath = %q, default constant = %q", cfg.ToolRegistryPath, DefaultToolRegistryPath)
	}
	if EnvToolRegistryPath != "TOOL_REGISTRY_PATH" {
		t.Fatalf("EnvToolRegistryPath = %q", EnvToolRegistryPath)
	}
}

func TestToolRegistryPathAcceptsOrdinaryPaths(t *testing.T) {
	for _, p := range []string{
		"tools.json",
		"examples/config/tools.json",
		"/etc/sentinelapi/tools.json",
		`C:\sentinel\config\tools.json`,
		`.\config\tools.json`,
		"path with spaces/tools.json",
		strings.Repeat("a", 4096),
	} {
		cfg, err := Load(lookupFrom(map[string]string{EnvToolRegistryPath: p}))
		if err != nil {
			t.Errorf("path %.40q rejected: %v", p, err)
			continue
		}
		if cfg.ToolRegistryPath != p {
			t.Errorf("ToolRegistryPath = %.40q, want %.40q", cfg.ToolRegistryPath, p)
		}
	}
}

func TestToolRegistryPathRejectsImplausibleValues(t *testing.T) {
	for name, p := range map[string]string{
		"NUL byte":        "tools\x00.json",
		"over 4096 bytes": strings.Repeat("a", 4097),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(lookupFrom(map[string]string{EnvToolRegistryPath: p}))
			if err == nil || !strings.Contains(err.Error(), EnvToolRegistryPath) {
				t.Fatalf("expected an error naming %s, got %v", EnvToolRegistryPath, err)
			}
			if len(err.Error()) > 600 {
				t.Errorf("the error echoes the whole value (%d bytes)", len(err.Error()))
			}
		})
	}
}

func TestToolRegistryPathErrorIsReportedWithOtherErrors(t *testing.T) {
	_, err := Load(lookupFrom(map[string]string{EnvToolRegistryPath: "x\x00", EnvLogLevel: "loud"}))
	if err == nil || !strings.Contains(err.Error(), EnvToolRegistryPath) || !strings.Contains(err.Error(), EnvLogLevel) {
		t.Fatalf("both problems should be reported: %v", err)
	}
}

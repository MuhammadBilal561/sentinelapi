package main

import (
	"path/filepath"
	"runtime"
)

// repoRoot returns the repository root, located from this source file so that
// tests do not depend on the working directory.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// exampleRegistryPath returns the absolute path of the shipped example registry.
func exampleRegistryPath() string {
	return filepath.Join(repoRoot(), "examples", "config", "tools.json")
}

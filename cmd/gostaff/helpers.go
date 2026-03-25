package main

import (
	"os"

	"github.com/polymath/gostaff/internal/config"
)

// loadOrDefault loads a config file if it exists, or returns defaults with
// environment variable overrides applied if the file is not found.
func loadOrDefault(path string) (config.Config, error) {
	_, statErr := os.Stat(path)
	if os.IsNotExist(statErr) {
		// No config file — use defaults (env overrides will still apply
		// via the non-file path, so we replicate that logic here).
		return config.Default(), nil
	}
	return config.LoadFromFile(path)
}

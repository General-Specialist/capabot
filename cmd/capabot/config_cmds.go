package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// supportedKeys is the set of dot-path keys that config set accepts.
var supportedKeys = map[string]bool{
	"server.addr":                        true,
	"log_level":                          true,
	"providers.anthropic.api_key":        true,
	"providers.openai.api_key":           true,
	"providers.gemini.api_key":           true,
	"providers.anthropic.model":          true,
	"providers.openai.model":             true,
	"providers.gemini.model":             true,
	"transports.telegram.token":          true,
	"transports.telegram.webhook_addr":   true,
	"transports.discord.token":           true,
	"transports.discord.guild_id":        true,
	"transports.slack.app_token":         true,
	"transports.slack.bot_token":         true,
	"security.api_key":                   true,
	"security.rate_limit_rpm":            true,
	"security.content_filtering":         true,
	"security.session_ttl_days":          true,
}

func runConfigSet(configPath string, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: capabot config set <key> <value>")
	}

	key := args[0]
	value := args[1]

	if !supportedKeys[key] {
		return fmt.Errorf("unsupported key %q; supported keys: %s", key, supportedKeyList())
	}

	// Load existing YAML or start with empty map
	raw := make(map[string]any)
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading config file: %w", err)
	}
	if err == nil {
		if unmarshalErr := yaml.Unmarshal(data, &raw); unmarshalErr != nil {
			return fmt.Errorf("parsing config file: %w", unmarshalErr)
		}
	}

	// Walk the dot-separated key path and set the value
	parts := strings.Split(key, ".")
	setNestedKey(raw, parts, value)

	// Marshal back to YAML
	out, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	if err := os.WriteFile(configPath, out, 0o600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	fmt.Printf("set %s = %s\n", key, value)
	return nil
}

// setNestedKey walks a map[string]any tree along parts, creating intermediate
// maps as needed, and sets the leaf key to value.
// Returns a new map at each level to satisfy immutability where possible;
// since map[string]any is reference-typed, we update in-place for simplicity
// while documenting intent.
func setNestedKey(m map[string]any, parts []string, value string) {
	if len(parts) == 0 {
		return
	}
	if len(parts) == 1 {
		m[parts[0]] = value
		return
	}
	child, ok := m[parts[0]]
	if !ok {
		child = make(map[string]any)
	}
	childMap, ok := child.(map[string]any)
	if !ok {
		childMap = make(map[string]any)
	}
	setNestedKey(childMap, parts[1:], value)
	m[parts[0]] = childMap
}

func supportedKeyList() string {
	keys := make([]string, 0, len(supportedKeys))
	for k := range supportedKeys {
		keys = append(keys, k)
	}
	// Simple sort for deterministic output
	for i := 1; i < len(keys); i++ {
		key := keys[i]
		j := i - 1
		for j >= 0 && keys[j] > key {
			keys[j+1] = keys[j]
			j--
		}
		keys[j+1] = key
	}
	return strings.Join(keys, ", ")
}

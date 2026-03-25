package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/polymath/capabot/internal/config"
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

	if err := config.SetKey(configPath, key, value); err != nil {
		return err
	}

	fmt.Printf("set %s = %s\n", key, value)
	return nil
}

func supportedKeyList() string {
	keys := make([]string, 0, len(supportedKeys))
	for k := range supportedKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

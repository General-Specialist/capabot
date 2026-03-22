package main

import (
	"fmt"
)

func runAgentList(configPath string) error {
	_, err := loadOrDefault(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Placeholder — real agent config loading comes with Phase 6 orchestrator integration.
	fmt.Println("no agents configured")
	fmt.Println("Use 'capabot serve' to start with the default agent.")
	return nil
}

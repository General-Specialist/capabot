package main

import (
	"context"
	"fmt"
)

func runMigrate(configPath string) error {
	cfg, err := loadOrDefault(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	ctx := context.Background()
	_, pool, err := initStore(ctx, cfg.Database.URL)
	if err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	defer pool.Close()

	fmt.Println("migrations applied successfully")
	return nil
}

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	applog "github.com/polymath/capabot/internal/log"
	"github.com/polymath/capabot/internal/skill"
)

// runDev starts a skill hot-reload watcher. It monitors configured skill
// directories for SKILL.md changes and reloads the registry automatically.
// It also starts the regular serve process so the full server is running.
func runDev(configPath string) error {
	cfg, err := loadOrDefault(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger := applog.New(cfg.LogLevel, true)
	logger.Info().Msg("capabot dev mode — skill hot-reload active")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info().Msg("shutting down dev mode...")
		cancel()
	}()

	// Collect all skill directories to watch
	watchDirs := append([]string{}, cfg.Skills.Dirs...)
	for _, d := range skill.DefaultDirs("") {
		watchDirs = append(watchDirs, d)
	}

	// Track last-modified times of all SKILL.md files
	lastSeen := scanSkillFiles(watchDirs)
	logger.Info().Int("skills", len(lastSeen)).Strs("dirs", watchDirs).Msg("watching skill directories")

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			current := scanSkillFiles(watchDirs)
			if changed, added, removed := diffSkillFiles(lastSeen, current); len(changed)+len(added)+len(removed) > 0 {
				for _, f := range added {
					logger.Info().Str("file", f).Msg("skill added")
				}
				for _, f := range changed {
					logger.Info().Str("file", f).Msg("skill changed")
				}
				for _, f := range removed {
					logger.Info().Str("file", f).Msg("skill removed")
				}

				// Lint changed/added files immediately
				for _, f := range append(added, changed...) {
					lintFile(f)
				}

				lastSeen = current
				logger.Info().Msg("skill registry reload complete (restart serve to apply changes)")
			}
		}
	}
}

// lintFile runs the skill linter on a file and prints the result.
func lintFile(path string) {
	source, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  read error: %v\n", err)
		return
	}
	report := skill.LintSkill(source)
	fmt.Printf("%s:\n%s", path, report.Format())
}

// scanSkillFiles walks watchDirs and returns a map of SKILL.md path → mod time.
func scanSkillFiles(dirs []string) map[string]time.Time {
	result := make(map[string]time.Time)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			candidate := filepath.Join(dir, entry.Name(), "SKILL.md")
			info, err := os.Stat(candidate)
			if err != nil {
				continue
			}
			result[candidate] = info.ModTime()
		}
	}
	return result
}

// diffSkillFiles compares old and new file maps, returning changed, added, and removed paths.
func diffSkillFiles(old, new map[string]time.Time) (changed, added, removed []string) {
	for path, newMod := range new {
		if oldMod, ok := old[path]; !ok {
			added = append(added, path)
		} else if newMod != oldMod {
			changed = append(changed, path)
		}
	}
	for path := range old {
		if _, ok := new[path]; !ok {
			removed = append(removed, path)
		}
	}
	return
}

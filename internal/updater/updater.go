package updater

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const checkInterval = time.Minute

type state struct {
	LastCheck time.Time `json:"last_check"`
}

// CheckAndUpdate fetches from origin and pulls if new commits are available.
// Rate-limited to once per minute. air detects changes and rebuilds automatically.
// Skips silently if not in a git repo or CAPABOT_NO_AUTOUPDATE is set.
func CheckAndUpdate() {
	if os.Getenv("CAPABOT_NO_AUTOUPDATE") != "" {
		return
	}

	if time.Since(lastCheck()) < checkInterval {
		return
	}
	saveLastCheck()

	if err := run("git", "fetch", "origin"); err != nil {
		return
	}

	out, err := exec.Command("git", "rev-list", "HEAD..origin/HEAD", "--count").Output()
	if err != nil || strings.TrimSpace(string(out)) == "0" {
		return
	}

	fmt.Fprintln(os.Stderr, "capabot: new commits available, pulling...")
	if err := run("git", "pull", "--ff-only"); err != nil {
		fmt.Fprintf(os.Stderr, "capabot: git pull failed: %v\n", err)
		return
	}
	fmt.Fprintln(os.Stderr, "capabot: updated — air will rebuild automatically")
}

func statePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".capabot", "update.json")
}

func lastCheck() time.Time {
	data, err := os.ReadFile(statePath())
	if err != nil {
		return time.Time{}
	}
	var s state
	if err := json.Unmarshal(data, &s); err != nil {
		return time.Time{}
	}
	return s.LastCheck
}

func saveLastCheck() {
	p := statePath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	data, _ := json.Marshal(state{LastCheck: time.Now()})
	os.WriteFile(p, data, 0o600)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

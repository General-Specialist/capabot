package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	githubRepo    = "General-Specialist/capabot"
	checkInterval = 1 * time.Minute
)

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// AutoUpdater holds state for a background auto-update. Create one with
// Prepare before app starts, then call Apply after app finishes.
type AutoUpdater struct {
	done    chan struct{}
	binary  []byte
	version string
	err     error
}

// Prepare starts a background goroutine that checks for a newer version and
// downloads the binary. Call Apply() after the command finishes to install it.
// Returns nil if auto-update should be skipped.
func Prepare(currentVersion string) *AutoUpdater {
	if currentVersion == "dev" || currentVersion == "" {
		return nil
	}
	if os.Getenv("CAPABOT_NO_AUTOUPDATE") != "" {
		return nil
	}

	state := loadState()

	if time.Since(state.LastCheck) < checkInterval {
		if state.LatestVersion == "" {
			return nil
		}
		latest := strings.TrimPrefix(state.LatestVersion, "v")
		current := strings.TrimPrefix(currentVersion, "v")
		if latest == current {
			return nil
		}
	}

	au := &AutoUpdater{done: make(chan struct{})}

	go func() {
		defer close(au.done)

		release, err := fetchLatestRelease()
		if err != nil {
			au.err = err
			return
		}

		saveState(updateState{
			LastCheck:     time.Now(),
			LatestVersion: release.TagName,
		})

		latest := strings.TrimPrefix(release.TagName, "v")
		current := strings.TrimPrefix(currentVersion, "v")
		if latest == current {
			return
		}

		asset := findAsset(release.Assets)
		if asset == nil {
			au.err = fmt.Errorf("no asset for %s/%s", runtime.GOOS, runtime.GOARCH)
			return
		}

		binary, err := downloadAndExtract(asset)
		if err != nil {
			au.err = err
			return
		}

		au.binary = binary
		au.version = release.TagName
	}()

	return au
}

// Apply waits for the background download to finish and replaces the current
// binary. Prints a one-line message on success. Safe to call on a nil receiver.
func (au *AutoUpdater) Apply() {
	if au == nil {
		return
	}

	select {
	case <-au.done:
	case <-time.After(15 * time.Second):
		return
	}

	if au.err != nil || au.binary == nil {
		return
	}

	execPath, err := os.Executable()
	if err != nil {
		return
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return
	}

	if err := replaceBinary(execPath, au.binary); err != nil {
		return
	}

	fmt.Fprintf(os.Stderr, "  Auto-updated to %s\n", au.version)
}

// ── GitHub API ──────────────────────────────────────────────────────────────

func fetchLatestRelease() (*githubRelease, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	return &release, nil
}

func findAsset(assets []githubAsset) *githubAsset {
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	target := fmt.Sprintf("capabot_%s_%s%s", runtime.GOOS, runtime.GOARCH, ext)

	for i := range assets {
		if assets[i].Name == target {
			return &assets[i]
		}
	}
	return nil
}

// ── Download & extract ──────────────────────────────────────────────────────

func downloadAndExtract(asset *githubAsset) ([]byte, error) {
	resp, err := http.Get(asset.BrowserDownloadURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if strings.HasSuffix(asset.Name, ".tar.gz") {
		return extractTarGz(data)
	}
	return extractZip(data)
}

func extractTarGz(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(hdr.Name) == "capabot" {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("capabot binary not found in archive")
}

func extractZip(data []byte) ([]byte, error) {
	tmpFile, err := os.CreateTemp("", "capabot-update-*.zip")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return nil, err
	}
	tmpFile.Close()

	r, err := zip.OpenReader(tmpFile.Name())
	if err != nil {
		return nil, err
	}
	defer r.Close()

	for _, f := range r.File {
		base := filepath.Base(f.Name)
		if base == "capabot" || base == "capabot.exe" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("capabot binary not found in archive")
}

// ── Binary replacement ──────────────────────────────────────────────────────

func replaceBinary(execPath string, newBinary []byte) error {
	dir := filepath.Dir(execPath)
	tmp, err := os.CreateTemp(dir, ".capabot-update-*")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(newBinary); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	tmp.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, execPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// ── Persistent state ────────────────────────────────────────────────────────

type updateState struct {
	LastCheck     time.Time `json:"last_check"`
	LatestVersion string    `json:"latest_version"`
}

func statePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".capabot", "update.json")
}

func loadState() updateState {
	p := statePath()
	if p == "" {
		return updateState{}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return updateState{}
	}
	var s updateState
	if err := json.Unmarshal(data, &s); err != nil {
		return updateState{}
	}
	return s
}

func saveState(s updateState) {
	p := statePath()
	if p == "" {
		return
	}
	os.MkdirAll(filepath.Dir(p), 0755)
	data, _ := json.Marshal(s)
	os.WriteFile(p, data, 0600)
}

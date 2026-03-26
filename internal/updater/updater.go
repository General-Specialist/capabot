package updater

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
)

const (
	repo   = "General-Specialist/gostaff"
	binary = "gostaff"
)

type ghRelease struct {
	TagName string `json:"tag_name"`
}

// Update checks GitHub Releases for a newer version and replaces the current binary.
// Returns the new version string, or empty if already up to date.
func Update(currentVersion string) (string, error) {
	latest, err := latestTag()
	if err != nil {
		return "", fmt.Errorf("checking for updates: %w", err)
	}

	if latest == currentVersion || latest == "v"+currentVersion {
		return "", nil
	}

	assetURL := assetURL(latest)

	tmpPath, err := downloadAndExtract(assetURL)
	if err != nil {
		return "", fmt.Errorf("downloading update: %w", err)
	}
	defer os.Remove(tmpPath)

	selfPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("finding current binary: %w", err)
	}

	if err := replaceBinary(selfPath, tmpPath); err != nil {
		return "", fmt.Errorf("replacing binary: %w", err)
	}

	return latest, nil
}

// LatestTag returns the latest release tag from GitHub.
func LatestTag() (string, error) {
	return latestTag()
}

func latestTag() (string, error) {
	resp, err := http.Get("https://api.github.com/repos/" + repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github API returned %d", resp.StatusCode)
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	return rel.TagName, nil
}

func assetURL(tag string) string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	return fmt.Sprintf(
		"https://github.com/%s/releases/download/%s/%s_%s_%s.tar.gz",
		repo, tag, binary, goos, goarch,
	)
}

func downloadAndExtract(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned %d", resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if strings.HasSuffix(hdr.Name, binary) && hdr.Typeflag == tar.TypeReg {
			tmp, err := os.CreateTemp("", "gostaff-update-*")
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(tmp, tr); err != nil {
				tmp.Close()
				os.Remove(tmp.Name())
				return "", err
			}
			tmp.Close()
			if err := os.Chmod(tmp.Name(), 0o755); err != nil {
				os.Remove(tmp.Name())
				return "", err
			}
			return tmp.Name(), nil
		}
	}
	return "", fmt.Errorf("binary %q not found in archive", binary)
}

func replaceBinary(dst, src string) error {
	// Rename is atomic on the same filesystem. If cross-device, fall back to copy.
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

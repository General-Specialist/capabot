package skill

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// IsGitHubShorthand returns true for "owner/repo" or "owner/repo@ref" patterns.
func IsGitHubShorthand(s string) bool {
	if strings.Contains(s, "://") {
		return false
	}
	base := s
	if idx := strings.Index(s, "@"); idx > 0 {
		base = s[:idx]
	}
	parts := strings.Split(base, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

// ParseGitHubURL extracts "owner/repo" from a GitHub URL like
// "https://github.com/owner/repo" or "https://github.com/owner/repo/tree/main".
// Returns the shorthand and true if it matched, or ("", false) otherwise.
func ParseGitHubURL(s string) (string, bool) {
	for _, prefix := range []string{"https://github.com/", "http://github.com/"} {
		if rest, ok := strings.CutPrefix(s, prefix); ok {
			rest = strings.TrimSuffix(rest, "/")
			parts := strings.Split(rest, "/")
			if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
				return parts[0] + "/" + parts[1], true
			}
		}
	}
	return "", false
}

// DownloadGitHub downloads a GitHub repo archive, extracts it, and returns the
// path to the extracted source directory. The caller should defer os.RemoveAll
// on the returned path.
func DownloadGitHub(ctx context.Context, target string) (string, error) {
	repo := target
	ref := "HEAD"
	if idx := strings.Index(target, "@"); idx > 0 {
		repo = target[:idx]
		ref = target[idx+1:]
	}

	tarURL := fmt.Sprintf("https://github.com/%s/archive/%s.tar.gz", repo, ref)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tarURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading from GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub download failed: HTTP %d (check that %s exists)", resp.StatusCode, repo)
	}

	// Extract tar.gz directly from response body
	extractDir, err := extractTarGzReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("extracting archive: %w", err)
	}

	// GitHub tarballs extract to <repo>-<ref>/ — unwrap single wrapper dir
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		os.RemoveAll(extractDir)
		return "", fmt.Errorf("reading extract dir: %w", err)
	}
	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(extractDir, entries[0].Name()), nil
	}
	return extractDir, nil
}

// extractTarGzReader extracts a tar.gz stream to a temp directory.
func extractTarGzReader(r io.Reader) (string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()

	dir, err := os.MkdirTemp("", "gostaff-gh-extract-*")
	if err != nil {
		return "", err
	}

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			os.RemoveAll(dir)
			return "", err
		}

		target := filepath.Join(dir, filepath.Clean("/"+hdr.Name))
		if !strings.HasPrefix(target, filepath.Clean(dir)+string(os.PathSeparator)) && target != filepath.Clean(dir) {
			os.RemoveAll(dir)
			return "", fmt.Errorf("tar entry %q would escape destination", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				os.RemoveAll(dir)
				return "", err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				os.RemoveAll(dir)
				return "", err
			}
			out, err := os.Create(target)
			if err != nil {
				os.RemoveAll(dir)
				return "", err
			}
			if _, err := io.Copy(out, tr); err != nil { //nolint:gosec
				out.Close()
				os.RemoveAll(dir)
				return "", err
			}
			out.Close()
		}
	}
	return dir, nil
}

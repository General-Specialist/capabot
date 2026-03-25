package skill

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const npmRegistry = "https://registry.npmjs.org"

// DownloadNPM downloads a package from the npm registry by name, extracts it,
// and returns the path to the extracted source directory. Supports exact versions
// via "name@version" syntax. The caller should defer os.RemoveAll on the parent.
func DownloadNPM(ctx context.Context, spec string) (string, error) {
	name, version := parseNPMSpec(spec)

	// Fetch package metadata
	metaURL := fmt.Sprintf("%s/%s/%s", npmRegistry, name, version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching npm metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("package %q not found on npm", name)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("npm registry returned HTTP %d for %q", resp.StatusCode, name)
	}

	var meta struct {
		Dist struct {
			Tarball string `json:"tarball"`
		} `json:"dist"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", fmt.Errorf("parsing npm metadata: %w", err)
	}
	if meta.Dist.Tarball == "" {
		return "", fmt.Errorf("no tarball URL in npm metadata for %q", name)
	}

	// Download tarball
	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, meta.Dist.Tarball, nil)
	if err != nil {
		return "", fmt.Errorf("creating tarball request: %w", err)
	}

	dlResp, err := http.DefaultClient.Do(dlReq)
	if err != nil {
		return "", fmt.Errorf("downloading npm tarball: %w", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("npm tarball download failed: HTTP %d", dlResp.StatusCode)
	}

	// npm tarballs extract to a "package/" directory
	extractDir, err := extractNPMTarball(dlResp.Body)
	if err != nil {
		return "", fmt.Errorf("extracting npm tarball: %w", err)
	}

	// npm tarballs always have a single "package" wrapper dir
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

// parseNPMSpec splits "name@version" into name and version.
// Returns "latest" as version if none specified.
func parseNPMSpec(spec string) (string, string) {
	// Handle scoped packages: @scope/name@version
	if strings.HasPrefix(spec, "@") {
		// Find the second @ (version separator)
		rest := spec[1:]
		if idx := strings.Index(rest, "@"); idx >= 0 {
			return spec[:idx+1], rest[idx+1:]
		}
		return spec, "latest"
	}
	if idx := strings.LastIndex(spec, "@"); idx > 0 {
		return spec[:idx], spec[idx+1:]
	}
	return spec, "latest"
}

// extractNPMTarball extracts a .tar.gz stream from npm to a temp directory.
func extractNPMTarball(r io.Reader) (string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()

	dir, err := os.MkdirTemp("", "gostaff-npm-*")
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

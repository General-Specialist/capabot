package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/polymath/capabot/internal/skill"
)

func runSkillLint(paths []string) {
	if len(paths) == 0 {
		paths = []string{"."}
	}

	exitCode := 0
	for _, p := range paths {
		files, err := resolveSkillFiles(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error resolving %s: %v\n", p, err)
			exitCode = 1
			continue
		}

		for _, f := range files {
			source, err := os.ReadFile(f)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error reading %s: %v\n", f, err)
				exitCode = 1
				continue
			}

			report := skill.LintSkill(source)
			fmt.Printf("%s:\n%s", f, report.Format())

			if !report.Valid {
				exitCode = 1
			}
		}
	}

	os.Exit(exitCode)
}

func runSkillImport(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: capabot skill import <skill-dir> [dest-dir]")
		os.Exit(1)
	}

	srcDir := args[0]

	destDir := defaultSkillsDir()
	if len(args) >= 2 {
		destDir = args[1]
	}

	result, err := skill.ImportSkill(srcDir, destDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "import failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("imported %s (tier %d)\n", result.SkillName, result.Tier)

	for _, w := range result.Warnings {
		fmt.Printf("  WARN: %s\n", w)
	}
	for _, m := range result.MappedTools {
		fmt.Printf("  TOOL: %s -> %s\n", m.From, m.To)
	}
	for _, h := range result.InstallHints {
		fmt.Printf("  INSTALL: %s\n", h)
	}

	if !result.Success {
		os.Exit(1)
	}
}

func runSkillCreate(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: capabot skill create <name>")
	}

	name := args[0]
	dirPath := filepath.Join(".", name)

	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		return fmt.Errorf("creating directory %q: %w", dirPath, err)
	}

	skillMDPath := filepath.Join(dirPath, "SKILL.md")
	content := buildSkillTemplate(name)

	if err := os.WriteFile(skillMDPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing SKILL.md: %w", err)
	}

	fmt.Printf("created %s/SKILL.md\n", name)
	return nil
}

// buildSkillTemplate returns the SKILL.md template content for a new skill.
func buildSkillTemplate(name string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("name: " + name + "\n")
	sb.WriteString("description: A brief description of what this skill does.\n")
	sb.WriteString("version: \"1.0.0\"\n")
	sb.WriteString("---\n")
	sb.WriteString("\n")
	sb.WriteString("# " + name + "\n")
	sb.WriteString("\n")
	sb.WriteString("<!-- Instructions for the LLM go here. Describe what the skill does and how to use the available tools. -->\n")
	sb.WriteString("\n")
	sb.WriteString("## Behavior\n")
	sb.WriteString("\n")
	sb.WriteString("- Describe the expected behavior\n")
	sb.WriteString("- List any constraints or guidelines\n")
	return sb.String()
}

// runSkillSearch searches the ClawHub skill directory for skills matching query.
func runSkillSearch(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: capabot skill search <query>")
	}
	query := strings.Join(args, " ")

	token := os.Getenv("CAPABOT_GITHUB_TOKEN")
	client := skill.NewClawHubClient(skill.ClawHubConfig{GitHubToken: token})

	fmt.Printf("searching ClawHub for %q...\n", query)
	results, err := client.SearchSkills(context.Background(), query)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}
	if len(results) == 0 {
		fmt.Println("no skills found")
		return nil
	}
	fmt.Printf("%-30s %-12s %s\n", "NAME", "VERSION", "DESCRIPTION")
	fmt.Println(strings.Repeat("-", 80))
	for _, s := range results {
		ver := s.Version
		if ver == "" {
			ver = "-"
		}
		desc := s.Description
		if len(desc) > 45 {
			desc = desc[:42] + "..."
		}
		fmt.Printf("%-30s %-12s %s\n", s.Name, ver, desc)
	}
	return nil
}

// runSkillInstall downloads and installs a skill from a URL or ClawHub name.
// If arg looks like a URL (contains "://") it downloads an archive.
// Otherwise it treats arg as a ClawHub skill name and fetches it directly.
func runSkillInstall(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: capabot skill install <name-or-url> [dest-dir]")
	}
	target := args[0]
	destDir := defaultSkillsDir()
	if len(args) >= 2 {
		destDir = args[1]
	}

	// If target looks like a ClawHub skill name (no "://"), use the registry client
	if !strings.Contains(target, "://") {
		return runSkillInstallFromClawHub(target, destDir)
	}

	rawURL := target
	fmt.Printf("downloading %s...\n", rawURL)

	resp, err := http.Get(rawURL) //nolint:noctx
	if err != nil {
		return fmt.Errorf("downloading skill: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	// Write to temp file
	tmp, err := os.CreateTemp("", "capabot-skill-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("writing download: %w", err)
	}
	tmp.Close()

	// Determine format from URL or Content-Type
	lowerURL := strings.ToLower(rawURL)
	ct := resp.Header.Get("Content-Type")
	var extractDir string
	switch {
	case strings.HasSuffix(lowerURL, ".zip") || strings.Contains(ct, "zip"):
		extractDir, err = extractZip(tmpPath)
	default:
		// Default: try tar.gz
		extractDir, err = extractTarGz(tmpPath)
	}
	if err != nil {
		return fmt.Errorf("extracting archive: %w", err)
	}
	defer os.RemoveAll(extractDir)

	// Run import on extracted directory
	result, importErr := skill.ImportSkill(extractDir, destDir)
	if importErr != nil {
		return fmt.Errorf("import failed: %w", importErr)
	}

	fmt.Printf("installed %s (tier %d)\n", result.SkillName, result.Tier)
	for _, w := range result.Warnings {
		fmt.Printf("  WARN: %s\n", w)
	}
	for _, h := range result.InstallHints {
		fmt.Printf("  INSTALL: %s\n", h)
	}

	if !result.Success {
		return fmt.Errorf("install completed with errors")
	}
	return nil
}

// runSkillInstallFromClawHub downloads a skill by name from the ClawHub registry
// and imports it into destDir.
func runSkillInstallFromClawHub(name, destDir string) error {
	token := os.Getenv("CAPABOT_GITHUB_TOKEN")
	client := skill.NewClawHubClient(skill.ClawHubConfig{GitHubToken: token})

	fmt.Printf("downloading %q from ClawHub...\n", name)
	skillPath, err := client.DownloadSkill(context.Background(), name, os.TempDir())
	if err != nil {
		return fmt.Errorf("ClawHub download failed: %w", err)
	}
	defer os.RemoveAll(skillPath)

	result, err := skill.ImportSkill(skillPath, destDir)
	if err != nil {
		return fmt.Errorf("import failed: %w", err)
	}

	fmt.Printf("installed %s (tier %d)\n", result.SkillName, result.Tier)
	for _, w := range result.Warnings {
		fmt.Printf("  WARN: %s\n", w)
	}
	for _, h := range result.InstallHints {
		fmt.Printf("  INSTALL: %s\n", h)
	}
	if !result.Success {
		return fmt.Errorf("install completed with errors")
	}
	return nil
}

// extractZip extracts a zip archive to a temp directory and returns its path.
func extractZip(src string) (string, error) {
	r, err := zip.OpenReader(src)
	if err != nil {
		return "", err
	}
	defer r.Close()

	dir, err := os.MkdirTemp("", "capabot-skill-extract-*")
	if err != nil {
		return "", err
	}

	for _, f := range r.File {
		if err := extractZipFile(f, dir); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}

func extractZipFile(f *zip.File, dest string) error {
	// Security: prevent path traversal
	target := filepath.Join(dest, filepath.Clean("/"+f.Name))
	if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) && target != filepath.Clean(dest) {
		return fmt.Errorf("zip entry %q would escape destination", f.Name)
	}

	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc) //nolint:gosec
	return err
}

// extractTarGz extracts a .tar.gz archive to a temp directory and returns its path.
func extractTarGz(src string) (string, error) {
	f, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()

	dir, err := os.MkdirTemp("", "capabot-skill-extract-*")
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

// runSkillInit scaffolds a new skill directory. Passing --wasm creates a
// Tier 3 WASM skill template instead of a plain Markdown skill.
func runSkillInit(args []string) error {
	wasm := false
	filtered := args[:0]
	for _, a := range args {
		if a == "--wasm" {
			wasm = true
		} else {
			filtered = append(filtered, a)
		}
	}
	if len(filtered) == 0 {
		return fmt.Errorf("usage: capabot skill init [--wasm] <name>")
	}

	name := filtered[0]
	dirPath := filepath.Join(".", name)

	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		return fmt.Errorf("creating directory %q: %w", dirPath, err)
	}

	// Always write SKILL.md
	skillMD := buildWASMSkillTemplate(name, wasm)
	if err := os.WriteFile(filepath.Join(dirPath, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		return fmt.Errorf("writing SKILL.md: %w", err)
	}
	fmt.Printf("created %s/SKILL.md\n", name)

	if wasm {
		// Write the Go WASM source stub
		mainGo := buildWASMSourceTemplate(name)
		if err := os.WriteFile(filepath.Join(dirPath, "main.go"), []byte(mainGo), 0o644); err != nil {
			return fmt.Errorf("writing main.go: %w", err)
		}
		fmt.Printf("created %s/main.go\n", name)

		// Write a Makefile for building the .wasm binary
		mk := buildWASMMakefile(name)
		if err := os.WriteFile(filepath.Join(dirPath, "Makefile"), []byte(mk), 0o644); err != nil {
			return fmt.Errorf("writing Makefile: %w", err)
		}
		fmt.Printf("created %s/Makefile\n", name)

		fmt.Printf("\nTo build: cd %s && make\n", name)
		fmt.Printf("Then load it with: capabot skill import ./%s\n", name)
	}

	return nil
}

// buildWASMSkillTemplate returns a SKILL.md for a WASM skill with the parameters schema.
func buildWASMSkillTemplate(name string, wasm bool) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("name: " + name + "\n")
	sb.WriteString("description: A brief description of what this skill does.\n")
	sb.WriteString("version: \"1.0.0\"\n")
	if wasm {
		sb.WriteString("parameters:\n")
		sb.WriteString("  type: object\n")
		sb.WriteString("  properties:\n")
		sb.WriteString("    input:\n")
		sb.WriteString("      type: string\n")
		sb.WriteString("      description: The input to process\n")
		sb.WriteString("  required: [\"input\"]\n")
	}
	sb.WriteString("---\n\n")
	sb.WriteString("# " + name + "\n\n")
	if wasm {
		sb.WriteString("This is a Tier 3 WASM skill. Build `skill.wasm` with `make` before loading.\n")
	} else {
		sb.WriteString("<!-- Instructions for the LLM go here. -->\n")
	}
	return sb.String()
}

// buildWASMSourceTemplate returns a Go WASM skill source stub.
// The skill receives JSON input from the host and writes JSON output back.
func buildWASMSourceTemplate(name string) string {
	return `//go:build js && wasm

package main

import (
	"encoding/json"
	"unsafe"
)

// inputBuf holds the JSON input written by the host via capabot_write_input.
var inputBuf []byte

// capabot_write_input allocates inputBuf and returns a pointer so the host
// can write the JSON parameters directly into WASM linear memory.
//
//go:export capabot_write_input
func writeInput(length uint32) uint32 {
	inputBuf = make([]byte, length)
	return uint32(uintptr(unsafe.Pointer(&inputBuf[0])))
}

// run is the skill entry point. Called by the host after writing input.
//
//go:export run
func run() {
	var params struct {
		Input string ` + "`" + `json:"input"` + "`" + `
	}
	if err := json.Unmarshal(inputBuf, &params); err != nil {
		setOutput(map[string]any{"content": "parse error: " + err.Error(), "is_error": true})
		return
	}

	// TODO: implement skill logic here
	result := "processed: " + params.Input

	setOutput(map[string]any{"content": result})
}

// setOutput serialises result to JSON and calls the host import capabot.set_output.
func setOutput(v any) {
	b, _ := json.Marshal(v)
	hostSetOutput(&b[0], uint32(len(b)))
}

// hostSetOutput is the host import: capabot.set_output(ptr, len).
//
//go:wasmimport capabot set_output
func hostSetOutput(ptr *byte, length uint32)

func main() {}
`
}

// buildWASMMakefile returns a Makefile for compiling a Go WASM skill.
func buildWASMMakefile(name string) string {
	return `SKILL := skill.wasm

.PHONY: all clean

## all: compile the WASM skill binary
all: $(SKILL)

$(SKILL): main.go
	GOOS=wasip1 GOARCH=wasm go build -o $(SKILL) .

## clean: remove build artifacts
clean:
	rm -f $(SKILL)
`
}

func defaultSkillsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".capabot", "skills")
	}
	return filepath.Join(home, ".capabot", "skills")
}

// resolveSkillFiles finds SKILL.md files from a path. If the path is a
// directory, it looks for SKILL.md inside it. If the path is a file, it
// uses it directly.
func resolveSkillFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return []string{path}, nil
	}

	skillPath := filepath.Join(path, "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		return []string{skillPath}, nil
	}

	// Scan subdirectories for SKILL.md files
	var files []string
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			candidate := filepath.Join(path, entry.Name(), "SKILL.md")
			if _, err := os.Stat(candidate); err == nil {
				files = append(files, candidate)
			}
		}
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no SKILL.md files found in %s", path)
	}
	return files, nil
}

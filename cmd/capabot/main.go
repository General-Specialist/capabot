package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/polymath/capabot/internal/skill"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "skill":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: capabot skill <lint|import> [path]")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "lint":
			runSkillLint(os.Args[3:])
		case "import":
			runSkillImport(os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "unknown skill command: %s\n", os.Args[2])
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: capabot <command>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  skill lint [path...]       Lint SKILL.md files for compatibility")
	fmt.Fprintln(os.Stderr, "  skill import <path> [dest] Import an OpenClaw skill directory")
}

func runSkillLint(paths []string) {
	if len(paths) == 0 {
		// Default: lint SKILL.md in current directory
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

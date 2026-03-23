package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/polymath/capabot/internal/updater"
)

// Set via -ldflags at build time.
var version = "dev"

const defaultConfigPath = "~/.capabot/config.yaml"

func main() {
	au := updater.Prepare(version)
	defer au.Apply()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		configPath := parseConfigFlag(os.Args[2:], defaultConfigPath)
		if err := runServe(expandHome(configPath)); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "chat":
		configPath := parseConfigFlag(os.Args[2:], defaultConfigPath)
		if err := runChat(expandHome(configPath)); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "dev":
		configPath := parseConfigFlag(os.Args[2:], defaultConfigPath)
		if err := runDev(expandHome(configPath)); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "skill":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: capabot skill <lint|import|create|init|install|search> [args...]")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "lint":
			runSkillLint(os.Args[3:])
		case "import":
			runSkillImport(os.Args[3:])
		case "create":
			if err := runSkillCreate(os.Args[3:]); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		case "install":
			if err := runSkillInstall(os.Args[3:]); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		case "init":
			if err := runSkillInit(os.Args[3:]); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		case "search":
			if err := runSkillSearch(os.Args[3:]); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown skill command: %s\n", os.Args[2])
			os.Exit(1)
		}

	case "agent":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: capabot agent <list>")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "list":
			configPath := parseConfigFlag(os.Args[3:], defaultConfigPath)
			if err := runAgentList(expandHome(configPath)); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown agent command: %s\n", os.Args[2])
			os.Exit(1)
		}

	case "config":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: capabot config <set> <key> <value>")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "set":
			configPath := parseConfigFlag(os.Args[3:], defaultConfigPath)
			// Remaining args after stripping --config flag
			args := filterConfigFlag(os.Args[3:])
			if err := runConfigSet(expandHome(configPath), args); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown config command: %s\n", os.Args[2])
			os.Exit(1)
		}

	case "migrate":
		configPath := parseConfigFlag(os.Args[2:], defaultConfigPath)
		if err := runMigrate(expandHome(configPath)); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "--help", "-h", "help":
		printUsage()

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: capabot <command> [options]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  serve [--config <path>]           Start the HTTP API server")
	fmt.Fprintln(os.Stderr, "  dev   [--config <path>]           Hot-reload mode for skill development")
	fmt.Fprintln(os.Stderr, "  chat  [--config <path>]           Interactive CLI chat session")
	fmt.Fprintln(os.Stderr, "  skill lint [path...]              Lint SKILL.md files for compatibility")
	fmt.Fprintln(os.Stderr, "  skill import <src> [dest]         Import an OpenClaw skill")
	fmt.Fprintln(os.Stderr, "  skill create <name>               Scaffold a new skill directory")
	fmt.Fprintln(os.Stderr, "  skill init [--wasm] <name>        Scaffold a skill (--wasm adds Go+Makefile)")
	fmt.Fprintln(os.Stderr, "  skill install <name-or-url> [dest] Download and install a skill from ClawHub or URL")
	fmt.Fprintln(os.Stderr, "  skill search <query>              Search the ClawHub skill registry")
	fmt.Fprintln(os.Stderr, "  agent list [--config <path>]      List configured agents")
	fmt.Fprintln(os.Stderr, "  config set <key> <value>          Set a config value")
	fmt.Fprintln(os.Stderr, "  migrate [--config <path>]         Run database migrations")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "default config path: ~/.capabot/config.yaml")
}

// parseConfigFlag scans args for --config <path> or --config=<path> and
// returns the path found, or defaultPath if none.
func parseConfigFlag(args []string, defaultPath string) string {
	for i, arg := range args {
		if arg == "--config" && i+1 < len(args) {
			return args[i+1]
		}
		if len(arg) > 9 && arg[:9] == "--config=" {
			return arg[9:]
		}
	}
	return defaultPath
}

// filterConfigFlag returns args with --config <val> or --config=<val> removed.
func filterConfigFlag(args []string) []string {
	result := make([]string, 0, len(args))
	skip := false
	for _, arg := range args {
		if skip {
			skip = false
			continue
		}
		if arg == "--config" {
			skip = true
			continue
		}
		if len(arg) > 9 && arg[:9] == "--config=" {
			continue
		}
		result = append(result, arg)
	}
	return result
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if len(path) == 0 || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

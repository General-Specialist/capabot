package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/polymath/capabot/internal/updater"
)

const defaultConfigPath = "config.yaml"

func main() {
	go updater.CheckAndUpdate()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		configPath := fs.String("config", defaultConfigPath, "path to config file")
		fs.Parse(os.Args[2:]) //nolint:errcheck
		if err := runServe(expandHome(*configPath)); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "chat":
		fs := flag.NewFlagSet("chat", flag.ExitOnError)
		configPath := fs.String("config", defaultConfigPath, "path to config file")
		fs.Parse(os.Args[2:]) //nolint:errcheck
		if err := runChat(expandHome(*configPath)); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "dev":
		fs := flag.NewFlagSet("dev", flag.ExitOnError)
		configPath := fs.String("config", defaultConfigPath, "path to config file")
		fs.Parse(os.Args[2:]) //nolint:errcheck
		if err := runDev(expandHome(*configPath)); err != nil {
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
			fs := flag.NewFlagSet("agent-list", flag.ExitOnError)
			configPath := fs.String("config", defaultConfigPath, "path to config file")
			fs.Parse(os.Args[3:]) //nolint:errcheck
			if err := runAgentList(expandHome(*configPath)); err != nil {
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
			fs := flag.NewFlagSet("config-set", flag.ExitOnError)
			configPath := fs.String("config", defaultConfigPath, "path to config file")
			fs.Parse(os.Args[3:]) //nolint:errcheck
			if err := runConfigSet(expandHome(*configPath), fs.Args()); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown config command: %s\n", os.Args[2])
			os.Exit(1)
		}

	case "migrate":
		fs := flag.NewFlagSet("migrate", flag.ExitOnError)
		configPath := fs.String("config", defaultConfigPath, "path to config file")
		fs.Parse(os.Args[2:]) //nolint:errcheck
		if err := runMigrate(expandHome(*configPath)); err != nil {
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
	fmt.Fprintln(os.Stderr, "  skill init [--plugin] <name>      Scaffold a skill (--plugin adds index.ts)")
	fmt.Fprintln(os.Stderr, "  skill install <name-or-url> [dest] Download and install a skill from ClawHub or URL")
	fmt.Fprintln(os.Stderr, "  skill search <query>              Search the ClawHub skill registry")
	fmt.Fprintln(os.Stderr, "  agent list [--config <path>]      List configured agents")
	fmt.Fprintln(os.Stderr, "  config set <key> <value>          Set a config value")
	fmt.Fprintln(os.Stderr, "  migrate [--config <path>]         Run database migrations")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "default config path: ~/.capabot/config.yaml")
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

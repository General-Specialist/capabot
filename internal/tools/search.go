package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/polymath/capabot/internal/agent"
)

// GlobTool implements the glob tool — find files matching a pattern.
type GlobTool struct{}

func (t *GlobTool) Name() string { return "glob" }
func (t *GlobTool) Description() string {
	return "Find files matching a glob pattern (e.g. **/*.go). Returns matching paths sorted by modification time, newest first."
}

func (t *GlobTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Glob pattern, e.g. \"**/*.go\" or \"src/**/*.ts\""},
			"path":    {"type": "string", "description": "Base directory to search (default: current working directory)"},
			"limit":   {"type": "integer", "description": "Max results to return (default 200)"}
		},
		"required": ["pattern"]
	}`)
}

func (t *GlobTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.Pattern == "" {
		return agent.ToolResult{Content: "pattern is required", IsError: true}, nil
	}
	if p.Limit <= 0 {
		p.Limit = 200
	}

	base := p.Path
	if base == "" {
		var err error
		base, err = os.Getwd()
		if err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("getwd: %v", err), IsError: true}, nil
		}
	} else {
		abs, err := resolvePath(base)
		if err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("path error: %v", err), IsError: true}, nil
		}
		base = abs
	}

	type fileEntry struct {
		path    string
		modTime int64
	}
	var matches []fileEntry

	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return nil
		}
		matched, err := matchGlob(p.Pattern, rel)
		if err != nil || !matched {
			return nil
		}
		info, _ := d.Info()
		var mt int64
		if info != nil {
			mt = info.ModTime().UnixNano()
		}
		matches = append(matches, fileEntry{path: path, modTime: mt})
		return nil
	})
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("walk error: %v", err), IsError: true}, nil
	}

	// Sort by mtime descending (newest first)
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].modTime > matches[j].modTime
	})
	if len(matches) > p.Limit {
		matches = matches[:p.Limit]
	}

	if len(matches) == 0 {
		return agent.ToolResult{Content: "no files found"}, nil
	}

	paths := make([]string, len(matches))
	for i, m := range matches {
		paths[i] = m.path
	}
	return agent.ToolResult{Content: strings.Join(paths, "\n")}, nil
}

// matchGlob handles ** in patterns by splitting on / and matching segment by segment.
func matchGlob(pattern, path string) (bool, error) {
	// Normalize separators
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)

	// Fast path: no ** — use stdlib
	if !strings.Contains(pattern, "**") {
		return filepath.Match(pattern, path)
	}

	return matchDoublestar(pattern, path), nil
}

func matchDoublestar(pattern, path string) bool {
	pp := strings.Split(pattern, "/")
	fp := strings.Split(path, "/")
	return dsMatch(pp, fp)
}

func dsMatch(pp, fp []string) bool {
	for len(pp) > 0 {
		if pp[0] == "**" {
			pp = pp[1:]
			if len(pp) == 0 {
				return true // ** at end matches everything
			}
			// Try matching ** against 0 or more path segments
			for i := 0; i <= len(fp); i++ {
				if dsMatch(pp, fp[i:]) {
					return true
				}
			}
			return false
		}
		if len(fp) == 0 {
			return false
		}
		ok, err := filepath.Match(pp[0], fp[0])
		if err != nil || !ok {
			return false
		}
		pp = pp[1:]
		fp = fp[1:]
	}
	return len(fp) == 0
}

// GrepTool implements the grep tool — search file contents with regex.
type GrepTool struct{}

func (t *GrepTool) Name() string { return "grep" }
func (t *GrepTool) Description() string {
	return "Search file contents using a regular expression. Supports glob file filtering, output modes (content/files/count), and context lines."
}

func (t *GrepTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern":          {"type": "string", "description": "Regular expression to search for"},
			"path":             {"type": "string", "description": "File or directory to search (default: current directory)"},
			"glob":             {"type": "string", "description": "Glob filter for file names, e.g. \"*.go\""},
			"output_mode":      {"type": "string", "enum": ["content", "files", "count"], "description": "content=matching lines, files=file paths only, count=match counts (default: files)"},
			"case_insensitive": {"type": "boolean", "description": "Case-insensitive matching (default false)"},
			"context_lines":    {"type": "integer", "description": "Lines of context before and after each match (content mode only)"},
			"limit":            {"type": "integer", "description": "Max output lines (default 200)"}
		},
		"required": ["pattern"]
	}`)
}

func (t *GrepTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Pattern         string `json:"pattern"`
		Path            string `json:"path"`
		Glob            string `json:"glob"`
		OutputMode      string `json:"output_mode"`
		CaseInsensitive bool   `json:"case_insensitive"`
		ContextLines    int    `json:"context_lines"`
		Limit           int    `json:"limit"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.Pattern == "" {
		return agent.ToolResult{Content: "pattern is required", IsError: true}, nil
	}
	if p.OutputMode == "" {
		p.OutputMode = "files"
	}
	if p.Limit <= 0 {
		p.Limit = 200
	}

	regexStr := p.Pattern
	if p.CaseInsensitive {
		regexStr = "(?i)" + regexStr
	}
	re, err := regexp.Compile(regexStr)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("invalid regex: %v", err), IsError: true}, nil
	}

	base := p.Path
	if base == "" {
		base, err = os.Getwd()
		if err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("getwd: %v", err), IsError: true}, nil
		}
	} else {
		base, err = resolvePath(base)
		if err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("path error: %v", err), IsError: true}, nil
		}
	}

	var outputLines []string

	info, statErr := os.Stat(base)
	if statErr == nil && !info.IsDir() {
		// Single file
		lines, err := grepFile(base, re, p.OutputMode, p.ContextLines)
		if err == nil && len(lines) > 0 {
			outputLines = append(outputLines, lines...)
		}
	} else {
		// Directory walk
		err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if p.Glob != "" {
				matched, err := filepath.Match(p.Glob, filepath.Base(path))
				if err != nil || !matched {
					return nil
				}
			}
			lines, err := grepFile(path, re, p.OutputMode, p.ContextLines)
			if err != nil || len(lines) == 0 {
				return nil
			}
			outputLines = append(outputLines, lines...)
			return nil
		})
		if err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("walk error: %v", err), IsError: true}, nil
		}
	}

	if len(outputLines) == 0 {
		return agent.ToolResult{Content: "no matches found"}, nil
	}
	if len(outputLines) > p.Limit {
		outputLines = outputLines[:p.Limit]
	}
	return agent.ToolResult{Content: strings.Join(outputLines, "\n")}, nil
}

func grepFile(path string, re *regexp.Regexp, outputMode string, contextLines int) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Skip binary files (simple heuristic: null bytes in first 8KB)
	check := data
	if len(check) > 8192 {
		check = check[:8192]
	}
	for _, b := range check {
		if b == 0 {
			return nil, nil
		}
	}

	lines := strings.Split(string(data), "\n")
	var matchLines []int
	for i, line := range lines {
		if re.MatchString(line) {
			matchLines = append(matchLines, i)
		}
	}
	if len(matchLines) == 0 {
		return nil, nil
	}

	switch outputMode {
	case "files":
		return []string{path}, nil
	case "count":
		return []string{fmt.Sprintf("%s: %d", path, len(matchLines))}, nil
	default: // "content"
		seen := make(map[int]bool)
		var out []string
		for _, mi := range matchLines {
			lo := mi - contextLines
			if lo < 0 {
				lo = 0
			}
			hi := mi + contextLines
			if hi >= len(lines) {
				hi = len(lines) - 1
			}
			for i := lo; i <= hi; i++ {
				if seen[i] {
					continue
				}
				seen[i] = true
				prefix := "  "
				if i == mi {
					prefix = "> "
				}
				out = append(out, fmt.Sprintf("%s:%d%s%s", path, i+1, prefix, lines[i]))
			}
		}
		return out, nil
	}
}

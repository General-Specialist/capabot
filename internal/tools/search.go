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

// SearchTool combines file finding (glob) and content searching (grep) into one tool.
type SearchTool struct{}

func (t *SearchTool) Name() string { return "search" }
func (t *SearchTool) Description() string {
	return "Find files or search content. Use mode=glob to find files by pattern (e.g. **/*.go), mode=grep to search file contents with regex."
}

func (t *SearchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"mode":             {"type": "string", "enum": ["glob", "grep"], "description": "glob=find files by pattern, grep=search file contents"},
			"pattern":          {"type": "string", "description": "Glob pattern (glob mode) or regex (grep mode)"},
			"path":             {"type": "string", "description": "Base directory to search (default: cwd)"},
			"glob":             {"type": "string", "description": "File name filter for grep mode, e.g. \"*.go\""},
			"output_mode":      {"type": "string", "enum": ["content", "files", "count"], "description": "Grep output: content=matching lines, files=paths only, count=match counts (default: files)"},
			"case_insensitive": {"type": "boolean", "description": "Case-insensitive grep (default false)"},
			"context_lines":    {"type": "integer", "description": "Lines of context around grep matches (content mode only)"},
			"limit":            {"type": "integer", "description": "Max results (default 200)"}
		},
		"required": ["pattern"]
	}`)
}

func (t *SearchTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Mode            string `json:"mode"`
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
	if p.Limit <= 0 {
		p.Limit = 200
	}

	// Default to glob if mode is empty and pattern looks like a glob
	if p.Mode == "" {
		if strings.ContainsAny(p.Pattern, "*?[") {
			p.Mode = "glob"
		} else {
			p.Mode = "grep"
		}
	}

	switch p.Mode {
	case "glob":
		return executeGlob(p.Pattern, p.Path, p.Limit)
	case "grep":
		return executeGrep(p.Pattern, p.Path, p.Glob, p.OutputMode, p.CaseInsensitive, p.ContextLines, p.Limit)
	default:
		return agent.ToolResult{Content: fmt.Sprintf("unknown mode %q (use glob or grep)", p.Mode), IsError: true}, nil
	}
}

func executeGlob(pattern, basePath string, limit int) (agent.ToolResult, error) {
	base, err := resolveSearchBase(basePath)
	if err != nil {
		return agent.ToolResult{Content: err.Error(), IsError: true}, nil
	}

	type fileEntry struct {
		path    string
		modTime int64
	}
	var matches []fileEntry

	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return nil
		}
		matched, err := matchGlob(pattern, rel)
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

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].modTime > matches[j].modTime
	})
	if len(matches) > limit {
		matches = matches[:limit]
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

func executeGrep(pattern, basePath, globFilter, outputMode string, caseInsensitive bool, contextLines, limit int) (agent.ToolResult, error) {
	if outputMode == "" {
		outputMode = "files"
	}

	regexStr := pattern
	if caseInsensitive {
		regexStr = "(?i)" + regexStr
	}
	re, err := regexp.Compile(regexStr)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("invalid regex: %v", err), IsError: true}, nil
	}

	base, err := resolveSearchBase(basePath)
	if err != nil {
		return agent.ToolResult{Content: err.Error(), IsError: true}, nil
	}

	var outputLines []string

	info, statErr := os.Stat(base)
	if statErr == nil && !info.IsDir() {
		lines, err := grepFile(base, re, outputMode, contextLines)
		if err == nil && len(lines) > 0 {
			outputLines = append(outputLines, lines...)
		}
	} else {
		err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if globFilter != "" {
				matched, err := filepath.Match(globFilter, filepath.Base(path))
				if err != nil || !matched {
					return nil
				}
			}
			lines, err := grepFile(path, re, outputMode, contextLines)
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
	if len(outputLines) > limit {
		outputLines = outputLines[:limit]
	}
	return agent.ToolResult{Content: strings.Join(outputLines, "\n")}, nil
}

func resolveSearchBase(basePath string) (string, error) {
	if basePath == "" {
		base, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getwd: %v", err)
		}
		return base, nil
	}
	abs, err := resolvePath(basePath)
	if err != nil {
		return "", fmt.Errorf("path error: %v", err)
	}
	return abs, nil
}

// matchGlob handles ** in patterns by splitting on / and matching segment by segment.
func matchGlob(pattern, path string) (bool, error) {
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)
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
				return true
			}
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

func grepFile(path string, re *regexp.Regexp, outputMode string, contextLines int) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
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
	default:
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

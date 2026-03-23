package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/polymath/capabot/internal/agent"
)

// FileReadTool implements the file_read tool.
type FileReadTool struct {
	allowedDirs []string
	maxBytes    int
}

// NewFileReadTool creates a file_read tool. If allowedDirs is empty, all paths are allowed.
func NewFileReadTool(allowedDirs []string) *FileReadTool {
	return &FileReadTool{
		allowedDirs: allowedDirs,
		maxBytes:    1024 * 1024, // 1MB
	}
}

func (t *FileReadTool) Name() string        { return "file_read" }
func (t *FileReadTool) Description() string { return "Read the contents of a file." }

func (t *FileReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File path to read"},
			"start_line": {"type": "integer", "description": "1-based start line (optional)"},
			"end_line": {"type": "integer", "description": "Inclusive end line (optional)"}
		},
		"required": ["path"]
	}`)
}

func (t *FileReadTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.Path == "" {
		return agent.ToolResult{Content: "path is required", IsError: true}, nil
	}

	absPath, err := resolvePath(p.Path)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("path error: %v", err), IsError: true}, nil
	}

	if err := checkAllowedPath(absPath, t.allowedDirs); err != nil {
		return agent.ToolResult{Content: err.Error(), IsError: true}, nil
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
	}

	// Truncate to maxBytes before splitting lines to avoid huge allocations
	if len(data) > t.maxBytes {
		data = data[:t.maxBytes]
	}

	content := string(data)

	if p.StartLine > 0 || p.EndLine > 0 {
		content = extractLines(content, p.StartLine, p.EndLine)
	}

	return agent.ToolResult{Content: content}, nil
}

// extractLines returns lines [startLine, endLine] (1-based, inclusive).
func extractLines(content string, startLine, endLine int) string {
	lines := strings.Split(content, "\n")
	if startLine < 1 {
		startLine = 1
	}
	if endLine < 1 || endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > endLine {
		return ""
	}
	return strings.Join(lines[startLine-1:endLine], "\n")
}

// FileWriteTool implements the file_write tool.
type FileWriteTool struct {
	allowedDirs []string
}

// NewFileWriteTool creates a file_write tool. If allowedDirs is empty, all paths are allowed.
func NewFileWriteTool(allowedDirs []string) *FileWriteTool {
	return &FileWriteTool{allowedDirs: allowedDirs}
}

func (t *FileWriteTool) Name() string        { return "file_write" }
func (t *FileWriteTool) Description() string { return "Write content to a file." }

func (t *FileWriteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File path to write"},
			"content": {"type": "string", "description": "Content to write"},
			"mode": {"type": "string", "enum": ["overwrite", "append"], "description": "Write mode (default overwrite)"}
		},
		"required": ["path", "content"]
	}`)
}

func (t *FileWriteTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Mode    string `json:"mode"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.Path == "" {
		return agent.ToolResult{Content: "path is required", IsError: true}, nil
	}

	absPath, err := resolvePath(p.Path)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("path error: %v", err), IsError: true}, nil
	}

	if err := checkAllowedPath(absPath, t.allowedDirs); err != nil {
		return agent.ToolResult{Content: err.Error(), IsError: true}, nil
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("mkdir error: %v", err), IsError: true}, nil
	}

	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if p.Mode == "append" {
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}

	f, err := os.OpenFile(absPath, flag, 0o644)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("open error: %v", err), IsError: true}, nil
	}
	defer f.Close()

	if _, err := f.WriteString(p.Content); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("write error: %v", err), IsError: true}, nil
	}

	return agent.ToolResult{Content: fmt.Sprintf("wrote %d bytes to %s", len(p.Content), absPath)}, nil
}

// FileEditTool implements the file_edit tool — targeted in-place string replacement.
type FileEditTool struct {
	allowedDirs []string
}

// NewFileEditTool creates a file_edit tool. If allowedDirs is empty, all paths are allowed.
func NewFileEditTool(allowedDirs []string) *FileEditTool {
	return &FileEditTool{allowedDirs: allowedDirs}
}

func (t *FileEditTool) Name() string { return "file_edit" }
func (t *FileEditTool) Description() string {
	return "Replace an exact string in a file. The old_string must match exactly (including whitespace and indentation). Use replace_all to replace every occurrence instead of just the first."
}

func (t *FileEditTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path":        {"type": "string", "description": "File path to edit"},
			"old_string":  {"type": "string", "description": "Exact string to find and replace"},
			"new_string":  {"type": "string", "description": "Replacement string"},
			"replace_all": {"type": "boolean", "description": "Replace all occurrences (default false — only first)"}
		},
		"required": ["path", "old_string", "new_string"]
	}`)
}

func (t *FileEditTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.Path == "" {
		return agent.ToolResult{Content: "path is required", IsError: true}, nil
	}

	absPath, err := resolvePath(p.Path)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("path error: %v", err), IsError: true}, nil
	}
	if err := checkAllowedPath(absPath, t.allowedDirs); err != nil {
		return agent.ToolResult{Content: err.Error(), IsError: true}, nil
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
	}

	content := string(data)
	if !strings.Contains(content, p.OldString) {
		return agent.ToolResult{Content: "old_string not found in file", IsError: true}, nil
	}

	var updated string
	if p.ReplaceAll {
		updated = strings.ReplaceAll(content, p.OldString, p.NewString)
	} else {
		updated = strings.Replace(content, p.OldString, p.NewString, 1)
	}

	if err := os.WriteFile(absPath, []byte(updated), 0o644); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("write error: %v", err), IsError: true}, nil
	}

	return agent.ToolResult{Content: fmt.Sprintf("edited %s", absPath)}, nil
}

// resolvePath cleans and resolves a path to an absolute path.
func resolvePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getting working directory: %w", err)
		}
		path = filepath.Join(cwd, path)
	}
	return filepath.Clean(path), nil
}

// checkAllowedPath returns an error if path is outside all allowed directories.
// If allowedDirs is empty, all paths are allowed.
func checkAllowedPath(absPath string, allowedDirs []string) error {
	if len(allowedDirs) == 0 {
		return nil
	}
	for _, dir := range allowedDirs {
		cleanDir := filepath.Clean(dir)
		if strings.HasPrefix(absPath, cleanDir+string(filepath.Separator)) || absPath == cleanDir {
			return nil
		}
	}
	return fmt.Errorf("path %q is outside allowed directories", absPath)
}

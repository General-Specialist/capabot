package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/polymath/capabot/internal/agent"
	"github.com/polymath/capabot/internal/llm"
)

// FileReadTool implements the file_read tool.
// Automatically handles images (JPEG, PNG, GIF, WEBP) and PDFs.
type FileReadTool struct {
	allowedDirs []string
	maxBytes    int
	maxPDFBytes int
}

func NewFileReadTool(allowedDirs []string) *FileReadTool {
	return &FileReadTool{
		allowedDirs: allowedDirs,
		maxBytes:    1024 * 1024,       // 1MB for text
		maxPDFBytes: 32 * 1024 * 1024,  // 32MB for PDFs
	}
}

func (t *FileReadTool) Name() string        { return "file_read" }
func (t *FileReadTool) Description() string {
	return "Read a file. Automatically handles text, images (JPEG/PNG/GIF/WEBP), and PDFs."
}

func (t *FileReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File path to read"},
			"start_line": {"type": "integer", "description": "1-based start line (text files only)"},
			"end_line": {"type": "integer", "description": "Inclusive end line (text files only)"}
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

	// Check if it's a PDF
	ext := strings.ToLower(filepath.Ext(absPath))
	if ext == ".pdf" {
		return t.readPDF(absPath)
	}

	// Check if it's an image
	if isImageExt(ext) {
		return t.readImage(absPath)
	}

	// Text file
	return t.readText(absPath, p.StartLine, p.EndLine)
}

func (t *FileReadTool) readText(absPath string, startLine, endLine int) (agent.ToolResult, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
	}
	if len(data) > t.maxBytes {
		data = data[:t.maxBytes]
	}

	content := string(data)
	if startLine > 0 || endLine > 0 {
		content = extractLines(content, startLine, endLine)
	}
	return agent.ToolResult{Content: content}, nil
}

func (t *FileReadTool) readImage(absPath string) (agent.ToolResult, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
	}
	mime := detectImageMime(absPath, data)
	if mime == "" {
		return agent.ToolResult{Content: "unsupported image format (supported: JPEG, PNG, GIF, WEBP)", IsError: true}, nil
	}
	name := filepath.Base(absPath)
	return agent.ToolResult{
		Content: fmt.Sprintf("[image: %s, %d bytes]", name, len(data)),
		Parts:   []llm.MediaPart{{MimeType: mime, Data: data, Name: name}},
	}, nil
}

func (t *FileReadTool) readPDF(absPath string) (agent.ToolResult, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("stat error: %v", err), IsError: true}, nil
	}
	if int(info.Size()) > t.maxPDFBytes {
		return agent.ToolResult{Content: fmt.Sprintf("PDF too large (%d MB, max 32 MB)", info.Size()/1024/1024), IsError: true}, nil
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
	}
	if len(data) < 4 || string(data[:4]) != "%PDF" {
		return agent.ToolResult{Content: "file does not appear to be a valid PDF", IsError: true}, nil
	}
	name := filepath.Base(absPath)
	return agent.ToolResult{
		Content: fmt.Sprintf("[PDF: %s, %d bytes]", name, len(data)),
		Parts:   []llm.MediaPart{{MimeType: "application/pdf", Data: data, Name: name}},
	}, nil
}

func isImageExt(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	}
	return false
}

func detectImageMime(path string, data []byte) string {
	if len(data) >= 4 {
		if data[0] == 0xFF && data[1] == 0xD8 {
			return "image/jpeg"
		}
		if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
			return "image/png"
		}
		if data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46 {
			return "image/gif"
		}
		if string(data[:4]) == "RIFF" && len(data) > 8 && string(data[8:12]) == "WEBP" {
			return "image/webp"
		}
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	ct := http.DetectContentType(data)
	if strings.HasPrefix(ct, "image/") {
		return ct
	}
	return ""
}

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

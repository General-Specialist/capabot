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

// ImageReadTool reads an image file and returns it as a media part for vision models.
type ImageReadTool struct{}

func (t *ImageReadTool) Name() string { return "image_read" }
func (t *ImageReadTool) Description() string {
	return "Read an image file (JPEG, PNG, GIF, WEBP) and pass it to the vision model for analysis."
}

func (t *ImageReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path to the image file"}
		},
		"required": ["path"]
	}`)
}

func (t *ImageReadTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Path string `json:"path"`
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

	data, err := os.ReadFile(absPath)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
	}

	mime := detectImageMime(absPath, data)
	if mime == "" {
		return agent.ToolResult{Content: "unsupported image format (supported: JPEG, PNG, GIF, WEBP)", IsError: true}, nil
	}

	return agent.ToolResult{
		Content: fmt.Sprintf("[image: %s, %d bytes]", filepath.Base(absPath), len(data)),
		Parts:   []llm.MediaPart{{MimeType: mime, Data: data, Name: filepath.Base(absPath)}},
	}, nil
}

func detectImageMime(path string, data []byte) string {
	// Fast content sniffing first
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
	// Fall back to extension
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
	// Try http.DetectContentType
	ct := http.DetectContentType(data)
	if strings.HasPrefix(ct, "image/") {
		return ct
	}
	return ""
}

// PDFReadTool reads a PDF and passes it to the model as a document part.
type PDFReadTool struct {
	maxBytes int
}

func NewPDFReadTool() *PDFReadTool {
	return &PDFReadTool{maxBytes: 32 * 1024 * 1024} // 32 MB
}

func (t *PDFReadTool) Name() string { return "pdf_read" }
func (t *PDFReadTool) Description() string {
	return "Read a PDF file and pass it to the model for analysis. The model will receive the full document content."
}

func (t *PDFReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path to the PDF file"}
		},
		"required": ["path"]
	}`)
}

func (t *PDFReadTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Path string `json:"path"`
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

	info, err := os.Stat(absPath)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("stat error: %v", err), IsError: true}, nil
	}
	if int(info.Size()) > t.maxBytes {
		return agent.ToolResult{Content: fmt.Sprintf("PDF too large (%d MB, max 32 MB)", info.Size()/1024/1024), IsError: true}, nil
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
	}

	// Verify it's a PDF
	if len(data) < 4 || string(data[:4]) != "%PDF" {
		return agent.ToolResult{Content: "file does not appear to be a valid PDF", IsError: true}, nil
	}

	name := filepath.Base(absPath)
	return agent.ToolResult{
		Content: fmt.Sprintf("[PDF: %s, %d bytes]", name, len(data)),
		Parts:   []llm.MediaPart{{MimeType: "application/pdf", Data: data, Name: name}},
	}, nil
}

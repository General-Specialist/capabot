package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/polymath/capabot/internal/agent"
)

// notebookCell is a single Jupyter notebook cell.
type notebookCell struct {
	CellType string          `json:"cell_type"`
	Source   json.RawMessage `json:"source"` // string or []string
	Outputs  []struct {
		OutputType string          `json:"output_type"`
		Text       json.RawMessage `json:"text,omitempty"`
	} `json:"outputs,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type notebook struct {
	Cells    []notebookCell  `json:"cells"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
	NBFormat int             `json:"nbformat"`
	NBFormatMinor int        `json:"nbformat_minor"`
}

// cellSource extracts the source string from a cell's Source field (string or []string).
func cellSource(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string first
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	// Try []string
	var lines []string
	if json.Unmarshal(raw, &lines) == nil {
		return strings.Join(lines, "")
	}
	return ""
}

// NotebookTool reads and edits Jupyter .ipynb notebooks.
type NotebookTool struct{}

func (t *NotebookTool) Name() string { return "notebook" }
func (t *NotebookTool) Description() string {
	return "Read or edit a Jupyter notebook (.ipynb). Use action=read to view all cells with their outputs. Use action=edit to replace the source of a specific cell by index."
}

func (t *NotebookTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path":       {"type": "string", "description": "Path to the .ipynb file"},
			"action":     {"type": "string", "enum": ["read", "edit"], "description": "read or edit (default: read)"},
			"cell_index": {"type": "integer", "description": "0-based cell index (required for edit)"},
			"source":     {"type": "string", "description": "New cell source content (required for edit)"}
		},
		"required": ["path"]
	}`)
}

func (t *NotebookTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Path      string `json:"path"`
		Action    string `json:"action"`
		CellIndex int    `json:"cell_index"`
		Source    string `json:"source"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.Path == "" {
		return agent.ToolResult{Content: "path is required", IsError: true}, nil
	}
	if p.Action == "" {
		p.Action = "read"
	}

	absPath, err := resolvePath(p.Path)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("path error: %v", err), IsError: true}, nil
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("read error: %v", err), IsError: true}, nil
	}

	var nb notebook
	if err := json.Unmarshal(data, &nb); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("invalid notebook JSON: %v", err), IsError: true}, nil
	}

	switch p.Action {
	case "read":
		return agent.ToolResult{Content: renderNotebook(nb)}, nil

	case "edit":
		if p.CellIndex < 0 || p.CellIndex >= len(nb.Cells) {
			return agent.ToolResult{Content: fmt.Sprintf("cell_index %d out of range (notebook has %d cells)", p.CellIndex, len(nb.Cells)), IsError: true}, nil
		}
		// Replace source — always store as a single string
		newSource, err := json.Marshal(p.Source)
		if err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("encoding source: %v", err), IsError: true}, nil
		}
		nb.Cells[p.CellIndex].Source = json.RawMessage(newSource)

		out, err := json.MarshalIndent(nb, "", " ")
		if err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("encoding notebook: %v", err), IsError: true}, nil
		}
		if err := os.WriteFile(absPath, out, 0o644); err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("write error: %v", err), IsError: true}, nil
		}
		return agent.ToolResult{Content: fmt.Sprintf("cell %d updated", p.CellIndex)}, nil

	default:
		return agent.ToolResult{Content: fmt.Sprintf("unknown action %q (use read or edit)", p.Action), IsError: true}, nil
	}
}

func renderNotebook(nb notebook) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Jupyter Notebook — %d cells\n\n", len(nb.Cells))
	for i, cell := range nb.Cells {
		src := cellSource(cell.Source)
		fmt.Fprintf(&sb, "--- Cell %d [%s] ---\n%s\n", i, cell.CellType, src)
		for _, out := range cell.Outputs {
			if out.Text != nil {
				outText := cellSource(out.Text)
				if outText != "" {
					fmt.Fprintf(&sb, "Output:\n%s\n", outText)
				}
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

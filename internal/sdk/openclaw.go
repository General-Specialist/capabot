package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/polymath/gostaff/internal/agent"
	"github.com/polymath/gostaff/internal/llm"
	"github.com/polymath/gostaff/internal/skill"
)

// OpenClawPlugin adapts an OpenClaw TS/JS/Python plugin (subprocess) to the
// sdk.Plugin interface. It spawns a PluginProcess, translates its registrations
// into agent.Tool / agent.ToolHook / etc., and delegates invocations over the
// JSON-line protocol.
type OpenClawPlugin struct {
	dir  string
	proc *skill.PluginProcess
}

// NewOpenClawPlugin creates an adapter for the OpenClaw plugin in skillDir.
// The subprocess is not started until Init is called.
func NewOpenClawPlugin(skillDir string) *OpenClawPlugin {
	return &OpenClawPlugin{dir: skillDir}
}

func (p *OpenClawPlugin) Init(r Registrar) error {
	proc, err := skill.NewPluginProcess(context.Background(), p.dir)
	if err != nil {
		return fmt.Errorf("starting openclaw plugin in %s: %w", p.dir, err)
	}
	p.proc = proc

	for _, rt := range proc.Tools() {
		r.RegisterTool(&openclawTool{
			name:   rt.Name,
			desc:   rt.Description,
			schema: rt.Parameters,
			proc:   proc,
		})
	}

	for _, rh := range proc.Hooks() {
		r.RegisterHook(&openclawHook{proc: proc, event: rh.Event})
	}

	for _, rr := range proc.Routes() {
		proc := proc // capture
		method := rr.Method
		path := rr.Path
		r.RegisterRoute(method, path, func(w http.ResponseWriter, req *http.Request) {
			headers := make(map[string]string)
			for k := range req.Header {
				headers[k] = req.Header.Get(k)
			}
			query := make(map[string]string)
			for k := range req.URL.Query() {
				query[k] = req.URL.Query().Get(k)
			}
			var body []byte
			if req.Body != nil {
				body, _ = readAll(req.Body)
			}
			resp, err := proc.InvokeHTTP(req.Context(), skill.HTTPRequest{
				Method:  req.Method,
				Path:    req.URL.Path,
				Headers: headers,
				Query:   query,
				Body:    string(body),
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			w.WriteHeader(resp.Status)
			fmt.Fprint(w, resp.Body)
		})
	}

	for _, rp := range proc.Providers() {
		r.RegisterProvider(rp.Name, &openclawProvider{
			proc:   proc,
			pname:  rp.Name,
			models: rp.Models,
		})
	}

	return nil
}

func (p *OpenClawPlugin) Close() error {
	if p.proc != nil {
		return p.proc.Close()
	}
	return nil
}

// openclawTool wraps a single tool from an OpenClaw subprocess plugin.
type openclawTool struct {
	name   string
	desc   string
	schema json.RawMessage
	proc   *skill.PluginProcess
}

func (t *openclawTool) Name() string               { return t.name }
func (t *openclawTool) Description() string         { return t.desc }
func (t *openclawTool) Parameters() json.RawMessage { return t.schema }

func (t *openclawTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	res, err := t.proc.Invoke(ctx, t.name, params)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("plugin error: %v", err), IsError: true}, nil
	}
	return agent.ToolResult{Content: res.Content, IsError: res.IsError}, nil
}

// openclawHook wraps a hook from an OpenClaw subprocess plugin.
type openclawHook struct {
	proc  *skill.PluginProcess
	event string
}

func (h *openclawHook) BeforeToolUse(ctx context.Context, toolName string, params json.RawMessage) (bool, json.RawMessage, error) {
	if h.event != "pre_tool_use" {
		return true, nil, nil
	}
	result, err := h.proc.InvokeHook(ctx, "pre_tool_use", toolName, params, nil)
	if err != nil {
		return true, nil, err
	}
	if !result.Allow {
		return false, nil, nil
	}
	return true, result.Params, nil
}

func (h *openclawHook) AfterToolUse(ctx context.Context, toolName string, params json.RawMessage, result json.RawMessage) (json.RawMessage, error) {
	if h.event != "post_tool_use" {
		return nil, nil
	}
	hookResult, err := h.proc.InvokeHook(ctx, "post_tool_use", toolName, params, result)
	if err != nil {
		return nil, err
	}
	return hookResult.Result, nil
}

// openclawProvider wraps an LLM provider from an OpenClaw subprocess plugin.
type openclawProvider struct {
	proc   *skill.PluginProcess
	pname  string
	models json.RawMessage
}

func (p *openclawProvider) Name() string { return p.pname }

func (p *openclawProvider) Models() []llm.ModelInfo {
	var ids []string
	_ = json.Unmarshal(p.models, &ids)
	out := make([]llm.ModelInfo, len(ids))
	for i, id := range ids {
		out[i] = llm.ModelInfo{ID: id, Name: id}
	}
	return out
}

func (p *openclawProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	msgsJSON, _ := json.Marshal(req.Messages)
	toolsJSON, _ := json.Marshal(req.Tools)
	resp, err := p.proc.InvokeChat(ctx, skill.ChatRequest{
		Provider: p.pname,
		Model:    req.Model,
		Messages: msgsJSON,
		System:   req.System,
		Tools:    toolsJSON,
		MaxTok:   req.MaxTokens,
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("openclaw provider %s: %s", p.pname, resp.Error)
	}
	return &llm.ChatResponse{
		Content:  resp.Content,
		Model:    resp.Model,
		Provider: p.pname,
	}, nil
}

func (p *openclawProvider) Stream(_ context.Context, _ llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, fmt.Errorf("openclaw provider %q does not support streaming", p.pname)
}

// readAll reads all bytes from an io.Reader. Separate function to avoid
// importing io in the main file just for this.
func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return buf, nil
}

package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

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

	// Persist plugin-level config schema to disk so the API can surface it.
	if cs := proc.ConfigSchema(); len(cs) > 0 && string(cs) != "{}" {
		_ = os.WriteFile(filepath.Join(p.dir, "_config_schema.json"), cs, 0o644)
	}

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

	for _, rc := range proc.Channels() {
		r.RegisterChannel(ChannelConfig{
			ID:             rc.ID,
			Tag:            rc.Tag,
			SystemPrompt:   rc.SystemPrompt,
			SkillNames:     rc.SkillNames,
			Model:          rc.Model,
			MemoryIsolated: rc.MemoryIsolated,
		})
	}

	// Register capabilities as agent tools.
	for _, cap := range proc.Capabilities() {
		tools := capabilityTools(cap, proc)
		for _, t := range tools {
			r.RegisterTool(t)
		}
	}

	// Register memory prompt sections.
	for _, mp := range proc.MemoryPromptSections() {
		reg, ok := r.(*Registration)
		if !ok {
			continue
		}
		reg.MemoryPromptBuilders = append(reg.MemoryPromptBuilders, &openclawMemoryPrompt{
			proc:        proc,
			sectionName: mp.Name,
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

func (p *openclawProvider) Stream(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	msgsJSON, _ := json.Marshal(req.Messages)
	toolsJSON, _ := json.Marshal(req.Tools)
	raw, err := p.proc.InvokeChatStream(ctx, skill.ChatRequest{
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

	ch := make(chan llm.StreamChunk, 64)
	go func() {
		defer close(ch)
		for chunk := range raw {
			if chunk.Err != "" {
				ch <- llm.StreamChunk{Err: fmt.Errorf("openclaw provider %s: %s", p.pname, chunk.Err), Done: true}
				return
			}
			if chunk.Delta != "" {
				ch <- llm.StreamChunk{Delta: chunk.Delta}
			}
			if chunk.Done {
				var usage llm.Usage
				if len(chunk.Usage) > 0 {
					_ = json.Unmarshal(chunk.Usage, &usage)
				}
				ch <- llm.StreamChunk{Done: true, Usage: &usage}
				return
			}
		}
	}()

	return ch, nil
}

// capabilityTools returns agent tools for a registered capability based on its kind.
func capabilityTools(cap skill.RegisteredCapability, proc *skill.PluginProcess) []agent.Tool {
	switch cap.Kind {
	case "speech":
		return []agent.Tool{
			newCapTool(proc, cap, "tts", cap.Name+"_tts", "Convert text to speech via "+cap.Name,
				`{"type":"object","properties":{"text":{"type":"string","description":"Text to convert to speech"}},"required":["text"]}`),
			newCapTool(proc, cap, "stt", cap.Name+"_stt", "Convert speech/audio to text via "+cap.Name,
				`{"type":"object","properties":{"audio":{"type":"string","description":"Base64-encoded audio data"}},"required":["audio"]}`),
		}
	case "image_generation":
		return []agent.Tool{
			newCapTool(proc, cap, "generate", "generate_image_"+cap.Name, "Generate an image via "+cap.Name,
				`{"type":"object","properties":{"prompt":{"type":"string","description":"Image generation prompt"},"params":{"type":"object","description":"Provider-specific parameters"}},"required":["prompt"]}`),
		}
	case "media_understanding":
		return []agent.Tool{
			newCapTool(proc, cap, "analyze", "analyze_media_"+cap.Name, "Analyze media (image/PDF/audio) via "+cap.Name,
				`{"type":"object","properties":{"data":{"type":"string","description":"Base64-encoded media data or URL"},"media_type":{"type":"string","description":"Type of media (image, pdf, audio)"},"prompt":{"type":"string","description":"Analysis prompt"}},"required":["data"]}`),
		}
	case "context_engine":
		return []agent.Tool{
			newCapTool(proc, cap, "query", "context_"+cap.Name, "Query context/RAG engine: "+cap.Name,
				`{"type":"object","properties":{"query":{"type":"string","description":"Search query for context retrieval"}},"required":["query"]}`),
		}
	case "interactive":
		return []agent.Tool{
			newCapTool(proc, cap, "start", "interactive_"+cap.Name, "Start interactive handler: "+cap.Name,
				`{"type":"object","properties":{"action":{"type":"string","description":"Action to perform (start, respond, cancel)"},"params":{"type":"object","description":"Action parameters"}},"required":["action"]}`),
		}
	default:
		return nil
	}
}

func newCapTool(proc *skill.PluginProcess, cap skill.RegisteredCapability, action, name, desc, schema string) *openclawCapabilityTool {
	return &openclawCapabilityTool{
		proc:   proc,
		kind:   cap.Kind,
		capN:   cap.Name,
		action: action,
		tName:  name,
		tDesc:  desc,
		tParam: json.RawMessage(schema),
	}
}

// openclawCapabilityTool wraps a plugin capability as an agent tool.
type openclawCapabilityTool struct {
	proc   *skill.PluginProcess
	kind   string
	capN   string
	action string
	tName  string
	tDesc  string
	tParam json.RawMessage
}

func (t *openclawCapabilityTool) Name() string               { return t.tName }
func (t *openclawCapabilityTool) Description() string         { return t.tDesc }
func (t *openclawCapabilityTool) Parameters() json.RawMessage { return t.tParam }

func (t *openclawCapabilityTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	res, err := t.proc.InvokeCapability(ctx, t.kind, t.capN, t.action, params)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("capability error: %v", err), IsError: true}, nil
	}
	return agent.ToolResult{Content: res.Content, IsError: res.IsError}, nil
}

// openclawMemoryPrompt implements MemoryPromptBuilder for OpenClaw plugins.
type openclawMemoryPrompt struct {
	proc        *skill.PluginProcess
	sectionName string
}

func (m *openclawMemoryPrompt) Name() string { return m.sectionName }

func (m *openclawMemoryPrompt) Build(ctx context.Context, sessionID string) (string, error) {
	return m.proc.InvokeMemoryPrompt(ctx, m.sectionName, sessionID)
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

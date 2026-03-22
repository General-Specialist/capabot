package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// WASMMemoryStore is the subset of a memory store that WASM host functions need.
// Using a minimal interface avoids an import cycle with internal/memory.
type WASMMemoryStore interface {
	StoreMemory(ctx context.Context, tenantID, key string, value []byte) error
	RecallMemory(ctx context.Context, tenantID, key string) ([]byte, error)
}

// WASMHostConfig controls which host capabilities are granted to a WASM skill.
// By default all are disabled (deny-by-default).
type WASMHostConfig struct {
	// AllowHTTPGet grants read-only HTTP access. Requests are made from the
	// host process. The WASM module cannot directly open sockets.
	AllowHTTPGet bool

	// AllowMemory grants access to the memory store for persistent key/value storage.
	// Requires MemoryStore to be non-nil.
	AllowMemory bool

	// MemoryStore is the backend for memory host functions.
	MemoryStore WASMMemoryStore

	// MemoryTenantID namespaces all memory operations for this skill instance.
	// Defaults to "wasm" if empty.
	MemoryTenantID string

	// MaxResponseBytes caps HTTP response bodies. Defaults to 256 KiB.
	MaxResponseBytes int64
}

// WASMExecutor runs a compiled WASM skill module in a sandboxed wazero runtime.
// The WASM module communicates via a simple ABI:
//
//   - Host exports "capabot_write_input(len) ptr" so the module can read its JSON input.
//   - The module exports "run()" which executes the skill logic.
//   - The module calls host import "capabot.set_output(ptr, len)" to return results.
//
// Optional host capabilities (HTTP, memory) are enabled via WASMHostConfig.
// Strict sandbox by default: no filesystem access, no network, no environment variables.
type WASMExecutor struct {
	runtime    wazero.Runtime
	module     wazero.CompiledModule
	hostConfig WASMHostConfig
}

// NewWASMExecutor compiles a WASM binary and returns an executor ready to run it.
// The returned executor is reusable across multiple Execute calls (each call
// instantiates a fresh module instance for isolation).
// Use NewWASMExecutorWithConfig to enable optional host capabilities.
func NewWASMExecutor(ctx context.Context, wasmBytes []byte) (*WASMExecutor, error) {
	return NewWASMExecutorWithConfig(ctx, wasmBytes, WASMHostConfig{})
}

// NewWASMExecutorWithConfig compiles a WASM binary and returns an executor with
// the specified host capability grants.
func NewWASMExecutorWithConfig(ctx context.Context, wasmBytes []byte, cfg WASMHostConfig) (*WASMExecutor, error) {
	// Instantiate a new runtime with no filesystem or network capabilities.
	r := wazero.NewRuntime(ctx)

	// Instantiate WASI — required by most WASM toolchains even for simple modules.
	// We use the minimal snapshot_preview1 implementation.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		r.Close(ctx)
		return nil, fmt.Errorf("wasi instantiate: %w", err)
	}

	// Compile the module once; reuse the compiled form across Execute calls.
	compiled, err := r.CompileModule(ctx, wasmBytes)
	if err != nil {
		r.Close(ctx)
		return nil, fmt.Errorf("wasm compile: %w", err)
	}

	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = 256 * 1024 // 256 KiB default
	}
	if cfg.MemoryTenantID == "" {
		cfg.MemoryTenantID = "wasm"
	}
	return &WASMExecutor{runtime: r, module: compiled, hostConfig: cfg}, nil
}

// NewWASMExecutorFromFile reads the .wasm file at path and compiles it with
// a default (fully sandboxed) host config.
func NewWASMExecutorFromFile(ctx context.Context, path string) (*WASMExecutor, error) {
	return NewWASMExecutorFromFileWithConfig(ctx, path, WASMHostConfig{})
}

// NewWASMExecutorFromFileWithConfig reads the .wasm file at path and compiles
// it with the provided host capability config.
func NewWASMExecutorFromFileWithConfig(ctx context.Context, path string, cfg WASMHostConfig) (*WASMExecutor, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading wasm file %q: %w", path, err)
	}
	return NewWASMExecutorWithConfig(ctx, b, cfg)
}

// Execute runs the compiled WASM module with the provided JSON input and
// returns the JSON output. Each call spawns an isolated module instance that
// is torn down after the call completes.
func (e *WASMExecutor) Execute(ctx context.Context, inputJSON []byte) ([]byte, error) {
	// outputBuf accumulates bytes written via the capabot_set_output host function.
	var outputBuf []byte

	// Build the "capabot" host module with all allowed functions.
	hostBuilder := e.runtime.NewHostModuleBuilder("capabot")

	// capabot.set_output(ptr, len) — always available; skill writes result here.
	hostBuilder = hostBuilder.
		NewFunctionBuilder().
		WithFunc(func(_ context.Context, m api.Module, ptr, length uint32) {
			buf, ok := m.Memory().Read(ptr, length)
			if ok {
				outputBuf = append(outputBuf, buf...)
			}
		}).
		Export("set_output")

	// capabot.http_get(urlPtr, urlLen, outPtr, outMaxLen) i32
	// Returns the number of bytes written to outPtr (truncated to outMaxLen).
	// Returns -1 on error. Only registered when AllowHTTPGet is true.
	if e.hostConfig.AllowHTTPGet {
		maxBytes := e.hostConfig.MaxResponseBytes
		hostBuilder = hostBuilder.
			NewFunctionBuilder().
			WithFunc(func(callCtx context.Context, m api.Module, urlPtr, urlLen, outPtr, outMax uint32) int32 {
				urlBytes, ok := m.Memory().Read(urlPtr, urlLen)
				if !ok {
					return -1
				}
				rawURL := string(urlBytes)
				// Only allow http/https
				if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
					return -1
				}
				req, err := http.NewRequestWithContext(callCtx, http.MethodGet, rawURL, nil)
				if err != nil {
					return -1
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					return -1
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
				if err != nil {
					return -1
				}
				if uint32(len(body)) > outMax {
					body = body[:outMax]
				}
				if !m.Memory().Write(outPtr, body) {
					return -1
				}
				return int32(len(body))
			}).
			Export("http_get")
	}

	// capabot.memory_store(keyPtr, keyLen, valPtr, valLen) i32
	// Returns 0 on success, -1 on error.
	// capabot.memory_recall(keyPtr, keyLen, outPtr, outMax) i32
	// Returns bytes written (>=0) on success, -1 on error.
	if e.hostConfig.AllowMemory && e.hostConfig.MemoryStore != nil {
		ms := e.hostConfig.MemoryStore
		tid := e.hostConfig.MemoryTenantID

		hostBuilder = hostBuilder.
			NewFunctionBuilder().
			WithFunc(func(callCtx context.Context, m api.Module, keyPtr, keyLen, valPtr, valLen uint32) int32 {
				keyBytes, ok1 := m.Memory().Read(keyPtr, keyLen)
				valBytes, ok2 := m.Memory().Read(valPtr, valLen)
				if !ok1 || !ok2 {
					return -1
				}
				if err := ms.StoreMemory(callCtx, tid, string(keyBytes), valBytes); err != nil {
					return -1
				}
				return 0
			}).
			Export("memory_store")

		hostBuilder = hostBuilder.
			NewFunctionBuilder().
			WithFunc(func(callCtx context.Context, m api.Module, keyPtr, keyLen, outPtr, outMax uint32) int32 {
				keyBytes, ok := m.Memory().Read(keyPtr, keyLen)
				if !ok {
					return -1
				}
				val, err := ms.RecallMemory(callCtx, tid, string(keyBytes))
				if err != nil {
					return -1
				}
				if uint32(len(val)) > outMax {
					val = val[:outMax]
				}
				if !m.Memory().Write(outPtr, val) {
					return -1
				}
				return int32(len(val))
			}).
			Export("memory_recall")
	}

	if _, err := hostBuilder.Instantiate(ctx); err != nil {
		return nil, fmt.Errorf("instantiating host module: %w", err)
	}

	// Instantiate the compiled skill module (fresh instance per Execute).
	mod, err := e.runtime.InstantiateModule(ctx, e.module, wazero.NewModuleConfig().
		WithName("skill_instance").
		// Explicitly disable filesystem access — no filesystem mounts means
		// the module cannot read or write files even via WASI.
		WithStartFunctions(), // do not auto-call _start / _initialize
	)
	if err != nil {
		return nil, fmt.Errorf("instantiating wasm module: %w", err)
	}
	defer mod.Close(ctx)

	// Write inputJSON into the module's linear memory via "capabot_write_input".
	// The module allocates memory and returns (ptr, len).
	writeInput := mod.ExportedFunction("capabot_write_input")
	if writeInput == nil {
		return nil, fmt.Errorf("wasm module missing export: capabot_write_input")
	}

	results, err := writeInput.Call(ctx, uint64(len(inputJSON)))
	if err != nil {
		return nil, fmt.Errorf("capabot_write_input call: %w", err)
	}
	if len(results) < 1 {
		return nil, fmt.Errorf("capabot_write_input: expected ptr result")
	}
	ptr := uint32(results[0])
	if !mod.Memory().Write(ptr, inputJSON) {
		return nil, fmt.Errorf("writing input to wasm memory at ptr=%d len=%d", ptr, len(inputJSON))
	}

	// Call the module's "run" export to execute the skill.
	run := mod.ExportedFunction("run")
	if run == nil {
		return nil, fmt.Errorf("wasm module missing export: run")
	}
	if _, err := run.Call(ctx); err != nil {
		return nil, fmt.Errorf("wasm run: %w", err)
	}

	if len(outputBuf) == 0 {
		return nil, fmt.Errorf("wasm skill produced no output")
	}
	return outputBuf, nil
}

// Close releases the wazero runtime and all compiled modules.
// Must be called when the executor is no longer needed.
func (e *WASMExecutor) Close(ctx context.Context) error {
	return e.runtime.Close(ctx)
}

// WASMSkillResult is the JSON envelope that WASM skills must return.
type WASMSkillResult struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

// ParseWASMResult decodes the raw bytes returned by Execute into a WASMSkillResult.
func ParseWASMResult(raw []byte) (WASMSkillResult, error) {
	var r WASMSkillResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return WASMSkillResult{}, fmt.Errorf("parsing wasm result: %w", err)
	}
	return r, nil
}

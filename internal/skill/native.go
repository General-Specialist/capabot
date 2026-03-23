package skill

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// NativeExecutor compiles and runs a Go-based skill as a subprocess.
// The skill reads JSON input from stdin and writes a JSON result to stdout
// using the same envelope as WASM skills: {"content":"...","is_error":false}.
type NativeExecutor struct {
	binPath string // path to compiled binary
	mu      sync.Mutex
}

// NewNativeExecutor compiles the Go source in skillDir and returns an executor.
// The skill directory must contain a main.go (and optionally a go.mod).
// The compiled binary is cached at skillDir/skill.bin.
func NewNativeExecutor(ctx context.Context, skillDir string) (*NativeExecutor, error) {
	binPath := filepath.Join(skillDir, "skill.bin")

	// Check if binary already exists and is newer than main.go
	mainPath := filepath.Join(skillDir, "main.go")
	needsBuild := true
	if binInfo, err := os.Stat(binPath); err == nil {
		if srcInfo, err := os.Stat(mainPath); err == nil {
			if binInfo.ModTime().After(srcInfo.ModTime()) {
				needsBuild = false
			}
		}
	}

	if needsBuild {
		if err := compileBinary(ctx, skillDir, binPath); err != nil {
			return nil, err
		}
	}

	return &NativeExecutor{binPath: binPath}, nil
}

// compileBinary runs `go build` in the skill directory.
func compileBinary(ctx context.Context, skillDir, binPath string) error {
	// If there's no go.mod, create a temporary one so `go build` works.
	goModPath := filepath.Join(skillDir, "go.mod")
	createdMod := false
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		modContent := []byte("module skill\n\ngo 1.21\n")
		if err := os.WriteFile(goModPath, modContent, 0o644); err != nil {
			return fmt.Errorf("creating go.mod: %w", err)
		}
		createdMod = true
	}

	cmd := exec.CommandContext(ctx, "go", "build", "-o", binPath, ".")
	cmd.Dir = skillDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Clean up temp go.mod on failure
		if createdMod {
			os.Remove(goModPath)
		}
		return fmt.Errorf("compiling skill: %s: %w", stderr.String(), err)
	}

	// Clean up temp go.mod after successful build
	if createdMod {
		os.Remove(goModPath)
	}

	return nil
}

// Execute runs the compiled binary with inputJSON piped to stdin.
// Returns the raw stdout bytes (expected to be JSON).
func (e *NativeExecutor) Execute(ctx context.Context, inputJSON []byte) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	cmd := exec.CommandContext(ctx, e.binPath)
	cmd.Stdin = bytes.NewReader(inputJSON)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("running skill: %s: %w", stderr.String(), err)
	}

	out := stdout.Bytes()
	if len(out) == 0 {
		return nil, fmt.Errorf("skill produced no output")
	}
	return out, nil
}

// Recompile forces a rebuild of the skill binary.
func (e *NativeExecutor) Recompile(ctx context.Context, skillDir string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return compileBinary(ctx, skillDir, e.binPath)
}

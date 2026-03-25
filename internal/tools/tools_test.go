package tools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/polymath/gostaff/internal/tools"
)

// ---- web_search ----

func TestWebSearch_DuckDuckGo_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"AbstractText":"Go is a programming language.","RelatedTopics":[{"Text":"Go was created at Google."}]}`))
	}))
	defer srv.Close()

	tool := tools.NewWebSearchToolWithURL(tools.WebSearchConfig{Backend: "duckduckgo"}, srv.URL)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"golang"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
}

func TestWebSearch_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tool := tools.NewWebSearchToolWithURL(tools.WebSearchConfig{Backend: "duckduckgo"}, srv.URL)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result on HTTP 500")
	}
}

func TestWebSearch_EmptyQuery(t *testing.T) {
	tool := tools.NewWebSearchTool(tools.WebSearchConfig{})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for empty query")
	}
}

// ---- web_fetch ----

func TestWebFetch_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>Test</title></head><body><p>Hello <b>World</b></p></body></html>`))
	}))
	defer srv.Close()

	tool := tools.NewWebFetchTool()
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	// HTML should be stripped
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
}

func TestWebFetch_InvalidURL(t *testing.T) {
	tool := tools.NewWebFetchTool()
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"ftp://example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for ftp URL")
	}
}

func TestWebFetch_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tool := tools.NewWebFetchTool()
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error on 404")
	}
}

// ---- file_read ----

func TestFileRead_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644)

	tool := tools.NewFileReadTool([]string{dir})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"`+path+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if result.Content == "" {
		t.Error("expected content")
	}
}

func TestFileRead_LineRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("alpha\nbeta\ngamma\ndelta\n"), 0o644)

	tool := tools.NewFileReadTool(nil) // no restrictions
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"`+path+`","start_line":2,"end_line":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "beta\ngamma" {
		t.Errorf("unexpected content: %q", result.Content)
	}
}

func TestFileRead_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	tool := tools.NewFileReadTool([]string{dir})

	// Try to escape via ..
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"`+dir+`/../etc/passwd"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected path traversal to be blocked")
	}
}

// ---- file_write ----

func TestFileWrite_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	tool := tools.NewFileWriteTool([]string{dir})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"`+path+`","content":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "hello" {
		t.Errorf("unexpected file content: %q", string(data))
	}
}

func TestFileWrite_Append(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	os.WriteFile(path, []byte("first"), 0o644)

	tool := tools.NewFileWriteTool([]string{dir})
	tool.Execute(context.Background(), json.RawMessage(`{"path":"`+path+`","content":" second","mode":"append"}`))

	data, _ := os.ReadFile(path)
	if string(data) != "first second" {
		t.Errorf("unexpected file content: %q", string(data))
	}
}

func TestFileWrite_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	tool := tools.NewFileWriteTool([]string{dir})

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"`+dir+`/../evil.txt","content":"evil"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected path traversal to be blocked")
	}
}

// ---- shell_exec ----

func TestShellExec_AllowedCommand(t *testing.T) {
	tool := tools.NewShellExecTool([]string{"echo"}, 10, nil, nil)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo","args":["hello"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestShellExec_DisallowedCommand(t *testing.T) {
	tool := tools.NewShellExecTool([]string{"echo"}, 10, nil, nil)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"rm","args":["-rf","/"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected disallowed command to be blocked")
	}
}

func TestShellExec_EmptyAllowlist(t *testing.T) {
	tool := tools.NewShellExecTool([]string{}, 10, nil, nil)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected empty allowlist to block everything")
	}
}

func TestShellExec_Timeout(t *testing.T) {
	tool := tools.NewShellExecTool([]string{"sleep"}, 1, nil, nil)
	ctx := context.Background()
	result, err := tool.Execute(ctx, json.RawMessage(`{"command":"sleep","args":["10"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected timeout to produce error result")
	}
}

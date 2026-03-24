package tools

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/polymath/capabot/internal/agent"
	"github.com/polymath/capabot/internal/llm"
)

// BrowserTool provides autonomous browser control via Playwright.
// It launches a long-running Node.js subprocess that keeps the browser open
// across multiple tool invocations, preserving sessions and cookies.
type BrowserTool struct {
	mu      sync.Mutex
	proc    *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	started bool
	dataDir string // persistent browser profile directory
}

// NewBrowserTool creates a browser tool. dataDir is where the persistent
// browser profile and Playwright helper are stored (default ~/.capabot/browser).
func NewBrowserTool(dataDir string) *BrowserTool {
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".capabot", "browser")
	}
	return &BrowserTool{dataDir: dataDir}
}

func (t *BrowserTool) Name() string { return "browser" }
func (t *BrowserTool) Description() string {
	return "Control a real browser with your existing sessions. Supports navigate, click, type, get_text, screenshot, evaluate, and close."
}

func (t *BrowserTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["navigate", "click", "type", "get_text", "screenshot", "evaluate", "close"],
				"description": "Browser action to perform"
			},
			"url":        {"type": "string", "description": "URL for navigate action"},
			"selector":   {"type": "string", "description": "CSS selector for click, type, get_text actions"},
			"text":       {"type": "string", "description": "Text to type for type action"},
			"expression": {"type": "string", "description": "JavaScript expression for evaluate action"},
			"full_page":  {"type": "boolean", "description": "Capture full page screenshot (default false)"},
			"headless":   {"type": "boolean", "description": "Run headless (default false — opens visible browser)"},
			"browser":    {"type": "string", "enum": ["chromium", "firefox", "webkit"], "description": "Browser engine (default chromium)"}
		},
		"required": ["action"]
	}`)
}

type browserCmd struct {
	Action     string `json:"action"`
	URL        string `json:"url,omitempty"`
	Selector   string `json:"selector,omitempty"`
	Text       string `json:"text,omitempty"`
	Expression string `json:"expression,omitempty"`
	FullPage   bool   `json:"full_page,omitempty"`
	Headless   bool   `json:"headless,omitempty"`
	Browser    string `json:"browser,omitempty"`
	DataDir    string `json:"data_dir,omitempty"`
}

type browserResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Text  string `json:"text,omitempty"`
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
	Image string `json:"image,omitempty"` // base64 PNG
	Value string `json:"value,omitempty"` // evaluate result
}

func (t *BrowserTool) Execute(ctx context.Context, params json.RawMessage) (agent.ToolResult, error) {
	var p struct {
		Action     string `json:"action"`
		URL        string `json:"url"`
		Selector   string `json:"selector"`
		Text       string `json:"text"`
		Expression string `json:"expression"`
		FullPage   bool   `json:"full_page"`
		Headless   bool   `json:"headless"`
		Browser    string `json:"browser"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return agent.ToolResult{Content: "invalid parameters", IsError: true}, nil
	}
	if p.Action == "" {
		return agent.ToolResult{Content: "action is required", IsError: true}, nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Start subprocess on first use
	if !t.started {
		if err := t.start(p.Headless, p.Browser); err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("failed to start browser: %v", err), IsError: true}, nil
		}
	}

	cmd := browserCmd{
		Action:     p.Action,
		URL:        p.URL,
		Selector:   p.Selector,
		Text:       p.Text,
		Expression: p.Expression,
		FullPage:   p.FullPage,
	}

	resp, err := t.send(cmd)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("browser error: %v", err), IsError: true}, nil
	}
	if !resp.OK {
		return agent.ToolResult{Content: fmt.Sprintf("browser error: %s", resp.Error), IsError: true}, nil
	}

	// Handle close: clean up subprocess
	if p.Action == "close" {
		t.stop()
		return agent.ToolResult{Content: "browser closed"}, nil
	}

	// Build response
	switch p.Action {
	case "navigate":
		return agent.ToolResult{Content: fmt.Sprintf("navigated to %s (title: %s)", resp.URL, resp.Title)}, nil

	case "click":
		return agent.ToolResult{Content: "clicked"}, nil

	case "type":
		return agent.ToolResult{Content: "typed"}, nil

	case "get_text":
		text := resp.Text
		if len(text) > 50000 {
			text = text[:50000] + "\n...(truncated)"
		}
		return agent.ToolResult{Content: text}, nil

	case "screenshot":
		if resp.Image == "" {
			return agent.ToolResult{Content: "screenshot captured (no data)"}, nil
		}
		imgData, err := base64.StdEncoding.DecodeString(resp.Image)
		if err != nil {
			return agent.ToolResult{Content: "failed to decode screenshot", IsError: true}, nil
		}
		return agent.ToolResult{
			Content: fmt.Sprintf("[screenshot: %d bytes]", len(imgData)),
			Parts:   []llm.MediaPart{{MimeType: "image/png", Data: imgData, Name: "screenshot.png"}},
		}, nil

	case "evaluate":
		return agent.ToolResult{Content: resp.Value}, nil

	default:
		return agent.ToolResult{Content: "done"}, nil
	}
}

// start launches the Node.js Playwright subprocess.
func (t *BrowserTool) start(headless bool, browserType string) error {
	if err := t.ensureHelper(); err != nil {
		return fmt.Errorf("setup: %w", err)
	}

	serverJS := filepath.Join(t.dataDir, "server.js")

	if browserType == "" {
		browserType = "chromium"
	}

	profileDir := filepath.Join(t.dataDir, "profile")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return err
	}

	//nolint:gosec // controlled path
	t.proc = exec.Command("node", serverJS)
	t.proc.Dir = t.dataDir
	t.proc.Env = append(cleanEnv(),
		"BROWSER_DATA_DIR="+profileDir,
		"BROWSER_HEADLESS="+fmt.Sprintf("%t", headless),
		"BROWSER_TYPE="+browserType,
	)

	var err error
	t.stdin, err = t.proc.StdinPipe()
	if err != nil {
		return err
	}

	stdout, err := t.proc.StdoutPipe()
	if err != nil {
		return err
	}
	t.scanner = bufio.NewScanner(stdout)
	t.scanner.Buffer(make([]byte, 0, 10*1024*1024), 10*1024*1024) // 10MB buffer for screenshots

	// Capture stderr to a buffer so we can include it in error messages
	var stderrBuf strings.Builder
	t.proc.Stderr = &stderrBuf

	if err := t.proc.Start(); err != nil {
		return fmt.Errorf("start node: %w", err)
	}

	// Wait for ready signal
	if !t.scanner.Scan() {
		return fmt.Errorf("subprocess exited before ready: %s", stderrBuf.String())
	}
	var ready browserResp
	if err := json.Unmarshal(t.scanner.Bytes(), &ready); err != nil || !ready.OK {
		errMsg := ready.Error
		if errMsg == "" {
			errMsg = "unknown"
		}
		if s := stderrBuf.String(); s != "" {
			errMsg += "\n" + s
		}
		return fmt.Errorf("subprocess not ready: %s", errMsg)
	}

	t.started = true
	return nil
}

// send writes a command and reads the response.
func (t *BrowserTool) send(cmd browserCmd) (browserResp, error) {
	data, err := json.Marshal(cmd)
	if err != nil {
		return browserResp{}, err
	}
	data = append(data, '\n')

	if _, err := t.stdin.Write(data); err != nil {
		return browserResp{}, fmt.Errorf("write: %w", err)
	}

	// Read response with timeout
	done := make(chan struct{})
	var resp browserResp
	var scanErr error

	go func() {
		defer close(done)
		if !t.scanner.Scan() {
			scanErr = fmt.Errorf("subprocess closed")
			if t.scanner.Err() != nil {
				scanErr = t.scanner.Err()
			}
			return
		}
		scanErr = json.Unmarshal(t.scanner.Bytes(), &resp)
	}()

	select {
	case <-done:
		if scanErr != nil {
			return browserResp{}, scanErr
		}
		return resp, nil
	case <-time.After(60 * time.Second):
		return browserResp{}, fmt.Errorf("browser action timed out after 60s")
	}
}

// stop kills the subprocess.
func (t *BrowserTool) stop() {
	if t.stdin != nil {
		t.stdin.Close()
	}
	if t.proc != nil && t.proc.Process != nil {
		t.proc.Process.Kill()
		t.proc.Wait() //nolint:errcheck
	}
	t.started = false
	t.proc = nil
	t.stdin = nil
	t.scanner = nil
}

// ensureHelper writes the Node.js server script and installs Playwright if needed.
func (t *BrowserTool) ensureHelper() error {
	if err := os.MkdirAll(t.dataDir, 0o755); err != nil {
		return err
	}

	// Write server.js
	serverPath := filepath.Join(t.dataDir, "server.js")
	if err := os.WriteFile(serverPath, []byte(browserServerJS), 0o644); err != nil {
		return err
	}

	// Write package.json if missing
	pkgPath := filepath.Join(t.dataDir, "package.json")
	if _, err := os.Stat(pkgPath); os.IsNotExist(err) {
		pkg := `{"private":true,"dependencies":{"playwright":"^1.52.0"}}`
		if err := os.WriteFile(pkgPath, []byte(pkg), 0o644); err != nil {
			return err
		}
	}

	// Install if node_modules missing
	nmPath := filepath.Join(t.dataDir, "node_modules")
	if _, err := os.Stat(nmPath); os.IsNotExist(err) {
		env := cleanEnv()

		install := exec.Command("npm", "install")
		install.Dir = t.dataDir
		install.Env = env
		out, err := install.CombinedOutput()
		if err != nil {
			return fmt.Errorf("npm install failed: %w\n%s", err, out)
		}

		// Install browser binaries
		npx := exec.Command("npx", "playwright", "install")
		npx.Dir = t.dataDir
		npx.Env = env
		out, err = npx.CombinedOutput()
		if err != nil {
			return fmt.Errorf("playwright install failed: %w\n%s", err, out)
		}
	}

	return nil
}


const browserServerJS = `
const readline = require('readline');

let context, page;

async function launch() {
  const browserType = process.env.BROWSER_TYPE || 'chromium';
  const headless = process.env.BROWSER_HEADLESS === 'true';
  const dataDir = process.env.BROWSER_DATA_DIR;

  const pw = require('playwright');
  const bt = pw[browserType];
  if (!bt) throw new Error('unknown browser type: ' + browserType);

  context = await bt.launchPersistentContext(dataDir, {
    headless,
    viewport: null,
    args: browserType === 'chromium' ? ['--disable-blink-features=AutomationControlled'] : [],
  });

  page = context.pages()[0] || await context.newPage();
}

async function ensurePage() {
  if (!context) await launch();
  if (!page || page.isClosed()) {
    page = await context.newPage();
  }
}

async function handle(cmd) {
  switch (cmd.action) {
    case 'navigate': {
      await ensurePage();
      await page.goto(cmd.url, { waitUntil: 'domcontentloaded', timeout: 30000 });
      return { ok: true, title: await page.title(), url: page.url() };
    }
    case 'click': {
      await ensurePage();
      await page.click(cmd.selector, { timeout: 10000 });
      return { ok: true };
    }
    case 'type': {
      await ensurePage();
      await page.fill(cmd.selector, cmd.text, { timeout: 10000 });
      return { ok: true };
    }
    case 'get_text': {
      await ensurePage();
      let text;
      if (cmd.selector) {
        text = await page.$eval(cmd.selector, el => el.innerText);
      } else {
        text = await page.innerText('body');
      }
      return { ok: true, text: (text || '').substring(0, 100000) };
    }
    case 'screenshot': {
      await ensurePage();
      const buf = await page.screenshot({ fullPage: !!cmd.full_page });
      return { ok: true, image: buf.toString('base64') };
    }
    case 'evaluate': {
      await ensurePage();
      const result = await page.evaluate(cmd.expression);
      return { ok: true, value: JSON.stringify(result, null, 2) };
    }
    case 'close': {
      if (context) {
        await context.close().catch(() => {});
        context = null;
        page = null;
      }
      return { ok: true };
    }
    default:
      return { ok: false, error: 'unknown action: ' + cmd.action };
  }
}

// Boot
(async () => {
  try {
    await launch();
    const msg = 'browser launched';
    process.stdout.write(JSON.stringify({ ok: true, text: msg }) + '\n');
  } catch (err) {
    process.stdout.write(JSON.stringify({ ok: false, error: err.message }) + '\n');
    process.exit(1);
  }

  const rl = readline.createInterface({ input: process.stdin, terminal: false });
  rl.on('line', async (line) => {
    try {
      const cmd = JSON.parse(line);
      const result = await handle(cmd);
      process.stdout.write(JSON.stringify(result) + '\n');
    } catch (err) {
      process.stdout.write(JSON.stringify({ ok: false, error: err.message }) + '\n');
    }
  });

  rl.on('close', () => {
    if (context) context.close().catch(() => {});
    process.exit(0);
  });
})();
`

// cleanEnv returns os.Environ() with conda-related variables stripped out.
// npm/npx lifecycle scripts spawn a shell that sources .zshrc which calls
// conda init — with parallel automation runs this creates many redundant
// conda processes. Stripping the vars prevents conda from activating.
func cleanEnv() []string {
	skip := map[string]bool{
		"CONDA_DEFAULT_ENV": true,
		"CONDA_EXE":        true,
		"CONDA_PREFIX":     true,
		"CONDA_PROMPT_MODIFIER": true,
		"CONDA_PYTHON_EXE": true,
		"CONDA_SHLVL":      true,
		"_CE_CONDA":        true,
		"_CE_M":            true,
	}
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key := kv
		if i := strings.Index(kv, "="); i >= 0 {
			key = kv[:i]
		}
		if !skip[key] {
			out = append(out, kv)
		}
	}
	return out
}

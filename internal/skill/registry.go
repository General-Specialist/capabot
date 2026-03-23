package skill

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Registry holds loaded skills from one or more directories.
// Skills loaded from earlier directories take precedence over later ones
// (workspace > user > bundled).
type Registry struct {
	mu         sync.RWMutex
	skills     map[string]*ParsedSkill // keyed by skill name
	skillPaths map[string]string       // skill name → directory path on disk
	wasmPaths  map[string]string       // skill name → absolute path to .wasm file (Tier 3)
	dirs       []string                // directories loaded in precedence order
}

// NewRegistry creates an empty skill registry.
func NewRegistry() *Registry {
	return &Registry{
		skills:     make(map[string]*ParsedSkill),
		skillPaths: make(map[string]string),
		wasmPaths:  make(map[string]string),
	}
}

// LoadDir reads all skill directories within dir and registers them.
// A skill directory is any subdirectory containing a SKILL.md file.
// Skills already registered (from higher-precedence dirs) are not overwritten.
// Returns the number of skills successfully loaded and any errors encountered.
func (r *Registry) LoadDir(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("reading skill dir %q: %w", dir, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.dirs = append(r.dirs, dir)

	loaded := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillMDPath := filepath.Join(dir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(skillMDPath)
		if err != nil {
			// Not a skill directory — skip silently
			continue
		}

		parsed, err := ParseSkillMD(data)
		if err != nil {
			// Parsing failure — skip but don't block other skills
			continue
		}

		// Resolve name: manifest name first, then directory name
		name := parsed.Manifest.Name
		if name == "" {
			name = entry.Name()
			parsed.Manifest.Name = name
		}

		// Lower precedence dirs don't overwrite earlier registrations
		if _, exists := r.skills[name]; exists {
			continue
		}

		skillDir := filepath.Join(dir, entry.Name())
		r.skills[name] = parsed
		r.skillPaths[name] = skillDir

		// Detect Tier 3: companion skill.wasm alongside the SKILL.md
		wasmPath := filepath.Join(skillDir, "skill.wasm")
		if _, err := os.Stat(wasmPath); err == nil {
			r.wasmPaths[name] = wasmPath
		}

		loaded++
	}

	return loaded, nil
}

// SkillPath returns the on-disk directory of the named skill, or ("", false).
func (r *Registry) SkillPath(name string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.skillPaths[name]
	return p, ok
}

// Unregister removes a skill from the in-memory registry by name.
// It does NOT delete files from disk — callers must do that separately.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.skills, name)
	delete(r.skillPaths, name)
	delete(r.wasmPaths, name)
}

// WASMPath returns the path to the compiled WASM module for the skill with
// the given name. Returns ("", false) if the skill has no WASM companion.
func (r *Registry) WASMPath(name string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.wasmPaths[name]
	return p, ok
}

// WASMSkillNames returns the names of all skills that have a companion .wasm file.
func (r *Registry) WASMSkillNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.wasmPaths))
	for name := range r.wasmPaths {
		names = append(names, name)
	}
	return names
}

// Get returns the parsed skill by name, or nil if not found.
func (r *Registry) Get(name string) *ParsedSkill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.skills[name]
}

// List returns all registered skills in undefined order.
func (r *Registry) List() []*ParsedSkill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*ParsedSkill, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, s)
	}
	return out
}

// Len returns the number of registered skills.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.skills)
}

// CheckRequirements checks whether all binary requirements declared by a skill
// are present in the host PATH. Returns a list of missing binary names.
func CheckRequirements(s *ParsedSkill) []string {
	if s == nil {
		return nil
	}

	meta := s.Manifest.Metadata.Resolved()
	if meta == nil {
		return nil
	}

	var missing []string
	for _, bin := range meta.Requires.Bins {
		bin = strings.TrimSpace(bin)
		if bin == "" {
			continue
		}
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, bin)
		}
	}

	// anyBins: satisfied if at least one is present
	if len(meta.Requires.AnyBins) > 0 {
		found := false
		for _, bin := range meta.Requires.AnyBins {
			bin = strings.TrimSpace(bin)
			if bin == "" {
				continue
			}
			if _, err := exec.LookPath(bin); err == nil {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, strings.Join(meta.Requires.AnyBins, "|"))
		}
	}

	return missing
}

// DefaultDirs returns the standard skill directory search path:
// workspace (.capabot/skills), user (~/.capabot/skills), system (/etc/capabot/skills).
// The caller is responsible for passing these to LoadDir in order.
func DefaultDirs(workspaceRoot string) []string {
	dirs := []string{}

	// Workspace-local skills (highest precedence)
	if workspaceRoot != "" {
		dirs = append(dirs, filepath.Join(workspaceRoot, ".capabot", "skills"))
	}

	// User-local skills
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".capabot", "skills"))
	}

	// System-level skills
	dirs = append(dirs, "/etc/capabot/skills")

	return dirs
}

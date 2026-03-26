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
	mu          sync.RWMutex
	skills      map[string]*ParsedSkill // keyed by skill name
	skillPaths  map[string]string       // skill name → directory path on disk
	pluginPaths map[string]string       // skill name → directory with script entry point (Tier 3)
	nativePaths map[string]string       // skill name → directory containing main.go (Tier 2)
	dirs        []string                // directories loaded in precedence order
}

// NewRegistry creates an empty skill registry.
func NewRegistry() *Registry {
	return &Registry{
		skills:      make(map[string]*ParsedSkill),
		skillPaths:  make(map[string]string),
		pluginPaths: make(map[string]string),
		nativePaths: make(map[string]string),
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

		skillDir := filepath.Join(dir, entry.Name())

		// Try to load SKILL.md if present; otherwise synthesize a minimal parsed skill.
		var parsed *ParsedSkill
		skillMDPath := filepath.Join(skillDir, "SKILL.md")
		if data, err := os.ReadFile(skillMDPath); err == nil {
			p, err := ParseSkillMD(data)
			if err != nil {
				continue
			}
			parsed = p
		} else {
			// No SKILL.md — only register if an executable is present.
			hasExec := false
			if _, err := os.Stat(filepath.Join(skillDir, "main.go")); err == nil {
				hasExec = true
			}
			if !hasExec {
				for _, ep := range pluginEntryPoints {
					if _, err := os.Stat(filepath.Join(skillDir, ep)); err == nil {
						hasExec = true
						break
					}
				}
			}
			if !hasExec {
				continue
			}
			parsed = &ParsedSkill{Manifest: SkillManifest{Name: entry.Name()}}
		}

		// Resolve name: manifest name first, then directory name.
		name := parsed.Manifest.Name
		if name == "" {
			name = entry.Name()
			parsed.Manifest.Name = name
		}

		// Lower precedence dirs don't overwrite earlier registrations.
		if _, exists := r.skills[name]; exists {
			continue
		}

		r.skills[name] = parsed
		r.skillPaths[name] = skillDir

		// Detect Tier 3: script entry point (plugin skill).
		for _, ep := range pluginEntryPoints {
			if _, err := os.Stat(filepath.Join(skillDir, ep)); err == nil {
				r.pluginPaths[name] = skillDir
				break
			}
		}

		// Detect Tier 2: main.go (native Go skill).
		if _, err := os.Stat(filepath.Join(skillDir, "main.go")); err == nil {
			r.nativePaths[name] = skillDir
		}

		loaded++
	}

	return loaded, nil
}

// LoadNewSkills re-scans all previously loaded directories and registers any
// skills that weren't present before. Returns the names of newly loaded skills.
func (r *Registry) LoadNewSkills() []string {
	r.mu.Lock()
	dirs := append([]string{}, r.dirs...)
	r.mu.Unlock()

	var added []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			skillDir := filepath.Join(dir, entry.Name())

			var parsed *ParsedSkill
			if data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md")); err == nil {
				p, err := ParseSkillMD(data)
				if err != nil {
					continue
				}
				parsed = p
			} else {
				hasExec := false
				if _, err := os.Stat(filepath.Join(skillDir, "main.go")); err == nil {
					hasExec = true
				}
				if !hasExec {
					for _, ep := range pluginEntryPoints {
						if _, err := os.Stat(filepath.Join(skillDir, ep)); err == nil {
							hasExec = true
							break
						}
					}
				}
				if !hasExec {
					continue
				}
				parsed = &ParsedSkill{Manifest: SkillManifest{Name: entry.Name()}}
			}

			name := parsed.Manifest.Name
			if name == "" {
				name = entry.Name()
				parsed.Manifest.Name = name
			}

			r.mu.Lock()
			if _, exists := r.skills[name]; exists {
				r.mu.Unlock()
				continue
			}

			r.skills[name] = parsed
			r.skillPaths[name] = skillDir

			for _, ep := range pluginEntryPoints {
				if _, err := os.Stat(filepath.Join(skillDir, ep)); err == nil {
					r.pluginPaths[name] = skillDir
					break
				}
			}

			if _, err := os.Stat(filepath.Join(skillDir, "main.go")); err == nil {
				r.nativePaths[name] = skillDir
			}

			r.mu.Unlock()
			added = append(added, name)
		}
	}
	return added
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
	delete(r.pluginPaths, name)
	delete(r.nativePaths, name)
}

// PluginPath returns the directory of the named plugin skill, or ("", false).
func (r *Registry) PluginPath(name string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.pluginPaths[name]
	return p, ok
}

// PluginSkillNames returns the names of all skills that have a script entry point.
func (r *Registry) PluginSkillNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.pluginPaths))
	for name := range r.pluginPaths {
		names = append(names, name)
	}
	return names
}

// NativePath returns the directory of the named native Go skill, or ("", false).
func (r *Registry) NativePath(name string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.nativePaths[name]
	return p, ok
}

// NativeSkillNames returns the names of all skills that have a companion main.go.
func (r *Registry) NativeSkillNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.nativePaths))
	for name := range r.nativePaths {
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
// workspace (.gostaff/skills), user (~/.gostaff/skills), system (/etc/gostaff/skills).
// The caller is responsible for passing these to LoadDir in order.
func DefaultDirs(workspaceRoot string) []string {
	dirs := []string{}

	// Workspace-local skills (highest precedence)
	if workspaceRoot != "" {
		dirs = append(dirs, filepath.Join(workspaceRoot, ".gostaff", "skills"))
	}

	// User-local skills
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".gostaff", "skills"))
	}

	// System-level skills
	dirs = append(dirs, "/etc/gostaff/skills")

	return dirs
}

package skill

// InstallSpec describes how to install a required dependency.
type InstallSpec struct {
	Kind    string   `yaml:"kind"`    // brew, node, go, uv
	Package string   `yaml:"package"` // package identifier
	Bins    []string `yaml:"bins"`    // binaries provided by this package
	Label   string   `yaml:"label"`   // human-readable install description
}

// SkillRequires declares runtime dependencies for a skill.
type SkillRequires struct {
	Env     []string `yaml:"env"`
	Bins    []string `yaml:"bins"`
	AnyBins []string `yaml:"anyBins"`
	Config  []string `yaml:"config"`
}

// SkillMetadataInner holds the OpenClaw-specific metadata block.
// Supports aliases: metadata.openclaw, metadata.clawdbot, metadata.clawdis
type SkillMetadataInner struct {
	Requires   SkillRequires `yaml:"requires"`
	Install    []InstallSpec `yaml:"install"`
	PrimaryEnv string        `yaml:"primaryEnv"`
	Always     bool          `yaml:"always"`
	SkillKey   string        `yaml:"skillKey"`
	Emoji      string        `yaml:"emoji"`
	Homepage   string        `yaml:"homepage"`
	OS         []string      `yaml:"os"`
}

// SkillMeta represents the _meta.json provenance file from ClawHub.
type SkillMeta struct {
	Owner       string `json:"owner"`
	Slug        string `json:"slug"`
	DisplayName string `json:"displayName"`
}

// SkillMetadata wraps the three alias keys that OpenClaw supports.
type SkillMetadata struct {
	OpenClaw *SkillMetadataInner `yaml:"openclaw"`
	ClawdBot *SkillMetadataInner `yaml:"clawdbot"`
	Clawdis  *SkillMetadataInner `yaml:"clawdis"`
}

// Resolved returns the first non-nil metadata inner block,
// checking openclaw -> clawdbot -> clawdis in precedence order.
func (m SkillMetadata) Resolved() *SkillMetadataInner {
	if m.OpenClaw != nil {
		return m.OpenClaw
	}
	if m.ClawdBot != nil {
		return m.ClawdBot
	}
	return m.Clawdis
}

// SkillManifest represents the parsed YAML frontmatter from a SKILL.md file.
type SkillManifest struct {
	Name        string        `yaml:"name"`
	Description string        `yaml:"description"`
	Version     string        `yaml:"version"`
	Homepage    string        `yaml:"homepage"`
	Metadata    SkillMetadata `yaml:"metadata"`

	UserInvocable          *bool  `yaml:"user-invocable"`
	DisableModelInvocation bool   `yaml:"disable-model-invocation"`
	CommandDispatch        string `yaml:"command-dispatch"`
	CommandTool            string `yaml:"command-tool"`
	CommandArgMode         string `yaml:"command-arg-mode"`
}

// ParseWarning describes a non-fatal issue found during parsing.
type ParseWarning struct {
	Line    int
	Message string
}

// ParsedSkill contains the fully parsed skill: manifest, instructions, and any warnings.
type ParsedSkill struct {
	Manifest     SkillManifest
	Instructions string
	Warnings     []ParseWarning
}

package skill

import (
	"fmt"
	"strings"
)

// BuildSystemPrompt constructs the final system prompt by appending active skill
// instructions to the base prompt. Each skill's instructions are wrapped in a
// clearly labelled section so the LLM can distinguish them from the base prompt.
//
// Skills are injected in the order provided. The caller is responsible for
// selecting which skills are active for a given session.
func BuildSystemPrompt(base string, skills []*ParsedSkill) string {
	if len(skills) == 0 {
		return base
	}

	var b strings.Builder
	b.WriteString(strings.TrimSpace(base))

	for _, s := range skills {
		if s == nil || s.Instructions == "" {
			continue
		}
		b.WriteString("\n\n")
		b.WriteString(skillSection(s))
	}

	return b.String()
}

// skillSection formats a single skill's instructions as a labelled block.
func skillSection(s *ParsedSkill) string {
	name := s.Manifest.Name
	if name == "" {
		name = "Unnamed Skill"
	}

	desc := s.Manifest.Description
	header := fmt.Sprintf("## Skill: %s", name)
	if desc != "" {
		header += fmt.Sprintf("\n_%s_", desc)
	}

	return header + "\n\n" + strings.TrimSpace(s.Instructions)
}

// ActiveSkillsFromNames resolves a list of skill names against a registry,
// returning the parsed skills in the same order. Skills not found in the
// registry are silently skipped.
func ActiveSkillsFromNames(reg *Registry, names []string) []*ParsedSkill {
	out := make([]*ParsedSkill, 0, len(names))
	for _, name := range names {
		if s := reg.Get(name); s != nil {
			out = append(out, s)
		}
	}
	return out
}

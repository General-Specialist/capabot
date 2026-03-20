package skill

// openClawToCapabot maps OpenClaw's flat tool names to Capabot equivalents.
// OpenClaw uses flat names (exec, read, write), not hierarchical (system.run).
var openClawToCapabot = map[string]string{
	// Runtime / shell
	"exec":    "shell_exec",
	"bash":    "shell_exec",
	"process": "shell_exec",

	// Filesystem
	"read":        "file_read",
	"write":       "file_write",
	"edit":        "file_edit",
	"apply_patch": "file_edit",

	// Web
	"web_search": "web_search",
	"web_fetch":  "web_fetch",

	// Browser
	"browser": "browser",

	// Memory
	"memory_search": "memory_recall",
	"memory_get":    "memory_recall",

	// Messaging
	"message": "message",

	// Sessions / multi-agent
	"sessions_list":    "agent_list",
	"sessions_history": "agent_history",
	"sessions_send":    "agent_send",
	"sessions_spawn":   "agent_spawn",
	"session_status":   "agent_status",

	// Scheduling
	"cron": "schedule",

	// Media
	"image":          "image",
	"image_generate": "image_generate",
	"pdf":            "pdf",
	"canvas":         "canvas",
}

// MapToolName translates an OpenClaw tool name to its Capabot equivalent.
// Returns the mapped name and true if a mapping exists, or the original
// name and false if no mapping is found (skill may reference a custom tool).
func MapToolName(openClawName string) (string, bool) {
	if mapped, ok := openClawToCapabot[openClawName]; ok {
		return mapped, true
	}
	return openClawName, false
}

// MapToolNames translates a slice of OpenClaw tool names, returning the
// mapped names and any names that had no known mapping.
func MapToolNames(names []string) (mapped []string, unmapped []string) {
	for _, name := range names {
		if capabotName, ok := MapToolName(name); ok {
			mapped = append(mapped, capabotName)
		} else {
			unmapped = append(unmapped, name)
		}
	}
	return mapped, unmapped
}

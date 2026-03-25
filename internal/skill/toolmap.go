package skill

// openClawToGoStaff maps OpenClaw's flat tool names to GoStaff equivalents.
// OpenClaw uses flat names (exec, read, write), not hierarchical (system.run).
var openClawToGoStaff = map[string]string{
	// Runtime / shell
	"exec":    "shell_exec",
	"bash":    "shell_exec",
	"process": "shell_exec",

	// Filesystem
	"read":        "file_read",
	"write":       "file_write",
	"edit":        "file_edit",
	"apply_patch": "file_edit",
	"glob":        "search",
	"grep":        "search",

	// Task tracking
	"todo":       "todo",
	"todo_write": "todo",
	"todo_read":  "todo",

	// Web
	"web_search": "web_search",
	"web_fetch":  "web_fetch",

	// Browser
	"browser": "browser",

	// Memory
	"memory_search": "memory",
	"memory_get":    "memory",

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

	// Media (handled by file_read now)
	"image":    "file_read",
	"pdf":      "file_read",
	"notebook": "notebook",
}

// MapToolName translates an OpenClaw tool name to its GoStaff equivalent.
// Returns the mapped name and true if a mapping exists, or the original
// name and false if no mapping is found (skill may reference a custom tool).
func MapToolName(openClawName string) (string, bool) {
	if mapped, ok := openClawToGoStaff[openClawName]; ok {
		return mapped, true
	}
	return openClawName, false
}

// MapToolNames translates a slice of OpenClaw tool names, returning the
// mapped names and any names that had no known mapping.
func MapToolNames(names []string) (mapped []string, unmapped []string) {
	for _, name := range names {
		if gostaffName, ok := MapToolName(name); ok {
			mapped = append(mapped, gostaffName)
		} else {
			unmapped = append(unmapped, name)
		}
	}
	return mapped, unmapped
}

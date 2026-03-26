package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/polymath/gostaff/internal/agent"
	"github.com/polymath/gostaff/internal/llm"
	"github.com/polymath/gostaff/internal/memory"
	"github.com/polymath/gostaff/internal/tools"
	"github.com/polymath/gostaff/internal/transport"
	"github.com/rs/zerolog"
)

// checkContent wraps a filter check for use in handlers.
// When filter is nil, all messages are allowed.
func checkContent(filter *agent.ContentFilter, text string) (bool, string) {
	if filter == nil {
		return true, ""
	}
	res := filter.Check(text)
	return !res.Blocked, res.Reason
}

// isApprovalResponse checks if a message is a yes/no response to a pending shell command approval.
func isApprovalResponse(text string) (approved bool, permanent bool, isResponse bool) {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch {
	case lower == "yes" || lower == "y" || lower == "allow" || lower == "approve" || lower == "ok" || lower == "sure" || lower == "go ahead":
		return true, false, true
	case strings.HasPrefix(lower, "yes always") || strings.HasPrefix(lower, "always") ||
		strings.HasPrefix(lower, "allow permanently") || strings.HasPrefix(lower, "approve permanently") ||
		strings.HasPrefix(lower, "yes permanently") || lower == "always allow":
		return true, true, true
	case lower == "no" || lower == "n" || lower == "deny" || lower == "cancel" || lower == "nope":
		return false, false, true
	}
	return false, false, false
}

// extractModelTag checks if text contains @model-id matching a known model.
func extractModelTag(text string, router *llm.Router) string {
	if router == nil {
		return ""
	}
	for _, m := range router.Models() {
		if strings.Contains(text, "@"+m.ID) {
			return m.ID
		}
	}
	return ""
}

// resolvePeople checks if text starts with @username, @tag, or a Discord role mention <@&ID>.
// Returns the stripped text and matching people (0 = no match, 1 = direct, N = tag fan-out).
func resolvePeople(ctx context.Context, store *memory.Store, text string, logger zerolog.Logger) (string, []memory.Person) {
	if store == nil || len(text) < 2 {
		return text, nil
	}

	// Check for Discord role mention: <@&ROLE_ID>
	if strings.HasPrefix(text, "<@&") {
		end := strings.Index(text, ">")
		if end > 3 {
			roleID := text[3:end]
			remainder := strings.TrimLeft(text[end+1:], " ")
			if remainder == "" {
				remainder = text
			}
			// Try person role.
			person, err := store.GetPersonByDiscordRoleID(ctx, roleID)
			if err == nil {
				logger.Info().Str("role_id", roleID).Str("person", person.Username).Msg("Discord role mention resolved")
				return remainder, []memory.Person{person}
			}
			// Try tag role.
			tag, err := store.GetTagByDiscordRoleID(ctx, roleID)
			if err == nil {
				tagged, err := store.GetPeopleByTag(ctx, tag)
				if err == nil && len(tagged) > 0 {
					logger.Info().Str("role_id", roleID).Str("tag", tag).Int("count", len(tagged)).Msg("Discord tag role mention resolved")
					return remainder, tagged
				}
			}
		}
	}

	if text[0] != '@' {
		return text, nil
	}
	rest := text[1:]
	name := rest
	remainder := ""
	for i, c := range rest {
		if c == ' ' || c == '\n' {
			name = rest[:i]
			remainder = rest[i+1:]
			break
		}
	}
	if name == "" {
		return text, nil
	}
	if remainder == "" {
		remainder = text
	}

	// Try exact username first (the @mention handle).
	person, err := store.GetPersonByUsername(ctx, name)
	if err == nil {
		return remainder, []memory.Person{person}
	}

	// Try as a tag.
	tagged, err := store.GetPeopleByTag(ctx, name)
	if err == nil && len(tagged) > 0 {
		logger.Info().Str("tag", name).Int("count", len(tagged)).Msg("tag matched people")
		return remainder, tagged
	}

	logger.Debug().Str("mention", name).Msg("no person or tag match, treating as plain text")
	return text, nil
}

// handleModeCmd processes /chat, /execute, and /mode commands.
func handleModeCmd(ctx context.Context, store *memory.Store, t transport.Transport, msg transport.InboundMessage) {
	reply := func(text string) {
		_ = t.Send(ctx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: text})
	}

	var mode string
	switch {
	case strings.HasPrefix(msg.Text, "/chat"):
		mode = "chat"
	case strings.HasPrefix(msg.Text, "/execute"):
		mode = "execute"
	case strings.HasPrefix(msg.Text, "/mode"):
		arg := strings.TrimSpace(strings.TrimPrefix(msg.Text, "/mode"))
		if arg == "" {
			current := store.GetActiveMode(ctx)
			reply(fmt.Sprintf("Current mode: **%s**", current))
			return
		}
		mode = arg
	}

	if err := store.SetActiveMode(ctx, mode); err != nil {
		reply("Failed to switch mode.")
		return
	}

	desc := ""
	switch mode {
	case "chat":
		desc = " (no tools — faster & cheaper)"
	case "execute":
		desc = " (full tools enabled)"
	}
	reply(fmt.Sprintf("Switched to **%s** mode%s.", mode, desc))
}

// handleDefaultRoleCmd processes the /default_role command.
// /default_role @tag      → bind this channel to all people with that tag
// /default_role @person   → bind this channel to a single person
// /default_role none      → clear binding
// /default_role           → show current binding
func handleDefaultRoleCmd(ctx context.Context, store *memory.Store, t transport.Transport, msg transport.InboundMessage, logger zerolog.Logger) {
	reply := func(text string) {
		_ = t.Send(ctx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: text})
	}

	arg := strings.TrimSpace(strings.TrimPrefix(msg.Text, "/default_role"))

	// Strip Discord role mention format <@&ID>.
	if strings.HasPrefix(arg, "<@&") {
		if end := strings.Index(arg, ">"); end > 0 {
			roleID := arg[3:end]
			// Try tag role first.
			if tag, err := store.GetTagByDiscordRoleID(ctx, roleID); err == nil {
				arg = tag
			} else if person, err := store.GetPersonByDiscordRoleID(ctx, roleID); err == nil {
				// Person role — bind directly to this person.
				binding := "persona:" + person.Username
				if err := store.SetChannelBinding(ctx, msg.ChannelID, binding); err != nil {
					reply("Failed to set binding.")
					return
				}
				reply(fmt.Sprintf("Default role set to **%s**. All messages in this channel will be answered by this person.", person.Name))
				return
			} else {
				reply("Unknown role.")
				return
			}
		}
	}
	arg = strings.TrimPrefix(arg, "@")

	if arg == "" {
		binding, _ := store.GetChannelBinding(ctx, msg.ChannelID)
		if binding == "" {
			reply("No default role set for this channel.")
		} else if strings.HasPrefix(binding, "persona:") {
			reply(fmt.Sprintf("Default role for this channel: person **%s**", strings.TrimPrefix(binding, "persona:")))
		} else {
			reply(fmt.Sprintf("Default role for this channel: tag **%s**", binding))
		}
		return
	}

	if arg == "none" || arg == "clear" {
		if err := store.DeleteChannelBinding(ctx, msg.ChannelID); err != nil {
			reply("Failed to clear binding.")
			return
		}
		reply("Default role cleared for this channel.")
		return
	}

	// Try as person username first.
	if person, err := store.GetPersonByUsername(ctx, arg); err == nil {
		if err := store.SetChannelBinding(ctx, msg.ChannelID, "persona:"+person.Username); err != nil {
			reply("Failed to set binding.")
			return
		}
		reply(fmt.Sprintf("Default role set to **%s**. All messages in this channel will be answered by this person.", person.Name))
		return
	}

	// Try as tag.
	tagged, err := store.GetPeopleByTag(ctx, arg)
	if err != nil || len(tagged) == 0 {
		reply(fmt.Sprintf("No person or tag found matching **%s**.", arg))
		return
	}

	if err := store.SetChannelBinding(ctx, msg.ChannelID, arg); err != nil {
		reply("Failed to set binding.")
		return
	}

	names := make([]string, len(tagged))
	for i, p := range tagged {
		names[i] = p.Name
	}
	reply(fmt.Sprintf("Default role set to tag **%s** (%s). All messages in this channel will be answered by these people.", arg, strings.Join(names, ", ")))
}

// makeMessageHandler returns a factory that wires inbound messages to the agent runner.
// Transports don't support streaming, so onEvent is passed as nil.
func makeMessageHandler(
	runAgent func(context.Context, string, string, string, []llm.ChatMessage, func(agent.AgentEvent)) (*agent.RunResult, error),
	store *memory.Store,
	router *llm.Router,
	filter *agent.ContentFilter,
	shellTool *tools.ShellExecTool,
	logger zerolog.Logger,
) func(t transport.Transport) func(context.Context, transport.InboundMessage) {
	return func(t transport.Transport) func(context.Context, transport.InboundMessage) {
		return func(msgCtx context.Context, msg transport.InboundMessage) {
			if ok, reason := checkContent(filter, msg.Text); !ok {
				logger.Warn().Str("reason", reason).Str("transport", t.Name()).Msg("message blocked by content filter")
				_ = t.Send(msgCtx, transport.OutboundMessage{
					ChannelID: msg.ChannelID,
					Text:      "Sorry, I can't process that message.",
				})
				return
			}

			// Check for pending shell command approval before anything else.
			if shellTool != nil && shellTool.Mode() == tools.ShellModePrompt {
				if pending, ok := shellTool.TakePending(msg.ChannelID); ok {
					approved, permanent, isResponse := isApprovalResponse(msg.Text)
					if isResponse {
						if !approved {
							_ = t.Send(msgCtx, transport.OutboundMessage{
								ChannelID: msg.ChannelID,
								Text:      fmt.Sprintf("Denied `%s`.", pending.Command),
							})
							return
						}
						// Approve and execute.
						if permanent {
							shellTool.ApprovePermanent(msgCtx, pending.Command)
						} else {
							shellTool.ApproveSession(pending.Command)
						}
						output, err := shellTool.RunCommand(msgCtx, pending)
						if err != nil {
							_ = t.Send(msgCtx, transport.OutboundMessage{
								ChannelID: msg.ChannelID,
								Text:      fmt.Sprintf("Error: %v", err),
							})
							return
						}
						scope := "this session"
						if permanent {
							scope = "permanently"
						}
						_ = t.Send(msgCtx, transport.OutboundMessage{
							ChannelID: msg.ChannelID,
							Text:      fmt.Sprintf("Approved `%s` (%s).\n```\n%s\n```", pending.Command, scope, output),
						})
						return
					}
					// Not a yes/no — put the pending command back so it's not lost.
					shellTool.SetPending(msg.ChannelID, pending)
				}
			}

			// Handle bot commands.
			if strings.HasPrefix(msg.Text, "/default_role") {
				handleDefaultRoleCmd(msgCtx, store, t, msg, logger)
				return
			}
			if strings.HasPrefix(msg.Text, "/chat") || strings.HasPrefix(msg.Text, "/execute") || strings.HasPrefix(msg.Text, "/mode") {
				handleModeCmd(msgCtx, store, t, msg)
				return
			}

			// Extract @model-id tag from the message.
			modelID := extractModelTag(msg.Text, router)
			if modelID != "" {
				msg.Text = strings.Replace(msg.Text, "@"+modelID, "", 1)
				msg.Text = strings.TrimSpace(msg.Text)
			}

			// Detect @PersonName or @tag mention at the start of the message.
			text, people := resolvePeople(msgCtx, store, msg.Text, logger)

			// Load per-channel configuration (includes binding, system prompt, model, etc.)
			var chanCfg *memory.ChannelConfig
			if store != nil {
				chanCfg, _ = store.GetChannelConfig(msgCtx, msg.ChannelID)
			}

			// If no @mention, check for channel binding (auto-route to bound tag or person).
			if len(people) == 0 && chanCfg != nil && chanCfg.Tag != "" {
				if strings.HasPrefix(chanCfg.Tag, "persona:") {
					username := strings.TrimPrefix(chanCfg.Tag, "persona:")
					if p, err := store.GetPersonByUsername(msgCtx, username); err == nil {
						people = []memory.Person{p}
						logger.Info().Str("channel", msg.ChannelID).Str("person", username).Msg("channel binding auto-routed to person")
					}
				} else {
					if tagged, err := store.GetPeopleByTag(msgCtx, chanCfg.Tag); err == nil && len(tagged) > 0 {
						people = tagged
						logger.Info().Str("channel", msg.ChannelID).Str("tag", chanCfg.Tag).Int("count", len(tagged)).Msg("channel binding auto-routed to tag")
					}
				}
			}
			messages := []llm.ChatMessage{{Role: "user", Content: text}}

			// Apply per-channel model override (if no @model tag was used).
			if modelID == "" && chanCfg != nil && chanCfg.Model != "" {
				modelID = chanCfg.Model
			}

			// Determine session channel key — isolate memory per channel if configured.
			sessionChannel := msg.ChannelID
			if chanCfg != nil && chanCfg.MemoryIsolated {
				sessionChannel = "isolated:" + msg.ChannelID
			}

			// Attach channel ID to context so tools (e.g. shell_exec) can key pending state per channel.
			msgCtx = tools.WithSessionID(msgCtx, msg.ChannelID)

			// Load global system prompt (prepended to every person's prompt).
			globalSysPrompt, _ := store.GetSystemPrompt(msgCtx)

			// Apply per-channel system prompt override.
			if chanCfg != nil && chanCfg.SystemPrompt != "" {
				globalSysPrompt = chanCfg.SystemPrompt
			}

			sendResponse := func(result *agent.RunResult, displayName, avatarData string) {
				text := strings.TrimSpace(result.Response)
				if text == "" {
					text = "(empty response)"
					logger.Warn().Str("display_name", displayName).Msg("agent returned empty response")
				}
				if err := t.Send(msgCtx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: text, DisplayName: displayName, AvatarData: avatarData}); err != nil {
					logger.Error().Err(err).Str("display_name", displayName).Msg("transport send failed")
				}
			}

			if len(people) == 0 {
				result, err := runAgent(msgCtx, globalSysPrompt, modelID, sessionChannel, messages, nil)
				if err != nil {
					logger.Error().Err(err).Str("session", msg.ChannelID).Str("transport", t.Name()).Msg("agent run failed")
					_ = t.Send(msgCtx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: fmt.Sprintf("error: %v", err)})
					return
				}
				sendResponse(result, "", "")
			} else if len(people) == 1 {
				p := people[0]
				displayName := p.Username
				if displayName == "" {
					displayName = p.Name
				}
				prompt := p.Prompt
				if globalSysPrompt != "" {
					prompt = globalSysPrompt + "\n\n" + prompt
				}
				result, err := runAgent(msgCtx, prompt, modelID, sessionChannel, messages, nil)
				if err != nil {
					logger.Error().Err(err).Str("session", msg.ChannelID).Str("person", p.Name).Msg("agent run failed")
					_ = t.Send(msgCtx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: fmt.Sprintf("error: %v", err)})
					return
				}
				sendResponse(result, displayName, transport.AvatarToDataURI(p.AvatarURL))
			} else {
				// Multiple people — run in parallel, send as each finishes.
				type personResult struct {
					person memory.Person
					result *agent.RunResult
					err    error
				}
				resultCh := make(chan personResult, len(people))
				for _, p := range people {
					go func(person memory.Person) {
						prompt := person.Prompt
						if globalSysPrompt != "" {
							prompt = globalSysPrompt + "\n\n" + prompt
						}
						res, err := runAgent(msgCtx, prompt, modelID, sessionChannel, messages, nil)
						resultCh <- personResult{person: person, result: res, err: err}
					}(p)
				}
				for range people {
					r := <-resultCh
					displayName := r.person.Username
					if displayName == "" {
						displayName = r.person.Name
					}
					if r.err != nil {
						logger.Error().Err(r.err).Str("person", r.person.Name).Msg("person agent failed")
						_ = t.Send(msgCtx, transport.OutboundMessage{ChannelID: msg.ChannelID, Text: fmt.Sprintf("error: %v", r.err), DisplayName: displayName, AvatarData: transport.AvatarToDataURI(r.person.AvatarURL)})
						continue
					}
					sendResponse(r.result, displayName, transport.AvatarToDataURI(r.person.AvatarURL))
				}
			}
		}
	}
}

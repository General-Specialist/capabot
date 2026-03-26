package transport

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"

	"github.com/polymath/gostaff/internal/memory"
	"github.com/rs/zerolog"
)

// SyncPeopleRoles creates Discord roles for any people or tags that don't have one yet.
func SyncPeopleRoles(ctx context.Context, client *DiscordRoleClient, store *memory.Store, logger zerolog.Logger) {
	if client == nil || store == nil {
		return
	}
	people, err := store.ListPeople(ctx)
	if err != nil {
		return
	}

	for _, p := range people {
		if p.DiscordRoleID == "" && p.Username != "" {
			roleID, err := client.CreateRole(ctx, p.Username)
			if err != nil {
				logger.Warn().Err(err).Str("person", p.Username).Msg("failed to sync Discord role")
			} else {
				_ = store.SetPersonDiscordRoleID(ctx, p.ID, roleID)
				logger.Info().Str("person", p.Username).Str("role_id", roleID).Msg("synced Discord person role")
			}
		}
	}

	allTags := make(map[string]bool)
	for _, p := range people {
		for _, t := range p.Tags {
			allTags[t] = true
		}
	}
	existingTagRoles, _ := store.ListDiscordTagRoles(ctx)
	for tag := range allTags {
		if _, exists := existingTagRoles[tag]; !exists {
			roleID, err := client.CreateRole(ctx, tag)
			if err != nil {
				logger.Warn().Err(err).Str("tag", tag).Msg("failed to sync Discord tag role")
			} else {
				_ = store.UpsertDiscordTagRole(ctx, tag, roleID)
				logger.Info().Str("tag", tag).Str("role_id", roleID).Msg("synced Discord tag role")
			}
		}
	}
}

// AvatarToDataURI reads a local avatar file (e.g. /api/avatars/abc.png) and returns
// a base64 data URI suitable for Discord's webhook avatar field.
// Returns empty string for full URLs (Discord can fetch those directly).
func AvatarToDataURI(avatarURL string) string {
	if avatarURL == "" {
		return ""
	}
	// Already a full URL — Discord can fetch it directly, no data URI needed.
	if strings.HasPrefix(avatarURL, "http://") || strings.HasPrefix(avatarURL, "https://") {
		return ""
	}
	// Local path like /api/avatars/abc.png — read from disk.
	filename := strings.TrimPrefix(avatarURL, "/api/avatars/")
	if filename == avatarURL {
		return "" // not an avatar path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".gostaff", "avatars", filename))
	if err != nil {
		return ""
	}
	// Detect mime type from extension.
	ext := strings.ToLower(filepath.Ext(filename))
	mime := "image/png"
	switch ext {
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	case ".gif":
		mime = "image/gif"
	case ".webp":
		mime = "image/webp"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DiscordRoleClient manages Discord guild roles for person sync.
// It uses the Discord REST API directly — no gateway connection needed.
type DiscordRoleClient struct {
	token   string
	guildID string
	client  *http.Client
}

// NewDiscordRoleClient creates a client for managing Discord roles.
// Returns nil if token or guildID is empty (Discord not configured).
func NewDiscordRoleClient(token, guildID string) *DiscordRoleClient {
	if token == "" || guildID == "" {
		return nil
	}
	return &DiscordRoleClient{
		token:   token,
		guildID: guildID,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

// CreateRole creates a Discord role and returns its ID.
func (c *DiscordRoleClient) CreateRole(ctx context.Context, name string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"name":        name,
		"mentionable": true,
	})
	url := fmt.Sprintf("%s/guilds/%s/roles", discordAPIBase, c.guildID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("discord: create role returned %d", resp.StatusCode)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.ID, nil
}

// UpdateRole renames a Discord role.
func (c *DiscordRoleClient) UpdateRole(ctx context.Context, roleID, name string) error {
	body, _ := json.Marshal(map[string]any{
		"name":        name,
		"mentionable": true,
	})
	url := fmt.Sprintf("%s/guilds/%s/roles/%s", discordAPIBase, c.guildID, roleID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord: update role returned %d", resp.StatusCode)
	}
	return nil
}

// DeleteRole removes a Discord role.
func (c *DiscordRoleClient) DeleteRole(ctx context.Context, roleID string) error {
	url := fmt.Sprintf("%s/guilds/%s/roles/%s", discordAPIBase, c.guildID, roleID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord: delete role returned %d", resp.StatusCode)
	}
	return nil
}

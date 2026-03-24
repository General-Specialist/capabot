package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Send delivers an outbound message to a Discord channel, splitting on the
// 2000-character limit when necessary. If DisplayName is set, sends via a
// channel webhook so the message appears under a custom name/avatar.
func (t *DiscordTransport) Send(ctx context.Context, msg OutboundMessage) error {
	if msg.DisplayName != "" {
		return t.sendViaWebhook(ctx, msg)
	}
	chunks := splitMessage(msg.Text, discordMaxMsgLen)
	for i, chunk := range chunks {
		var replyID string
		// Only attach the reply reference on the first chunk.
		if i == 0 {
			replyID = msg.ReplyToID
		}
		if err := t.postMessage(ctx, msg.ChannelID, chunk, replyID); err != nil {
			return err
		}
	}
	return nil
}

// postMessage sends a single message payload to the Discord REST API.
func (t *DiscordTransport) postMessage(ctx context.Context, channelID, content, replyToID string) error {
	body := map[string]interface{}{
		"content": content,
	}
	if replyToID != "" {
		body["message_reference"] = map[string]string{
			"message_id": replyToID,
		}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("discord: marshal send body: %w", err)
	}

	url := discordAPIBase + "/channels/" + channelID + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("discord: create send request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+t.token)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("discord: send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord: send message returned %d", resp.StatusCode)
	}
	return nil
}

// sendViaWebhook sends a message through a channel webhook, allowing a custom
// display name and avatar. Webhooks are created lazily and cached per channel.
func (t *DiscordTransport) sendViaWebhook(ctx context.Context, msg OutboundMessage) error {
	webhookURL, err := t.getOrCreateWebhook(ctx, msg.ChannelID)
	if err != nil {
		// Fall back to normal bot message if webhook creation fails.
		t.logger.Warn().Err(err).Msg("discord: webhook fallback to bot message")
		chunks := splitMessage(msg.Text, discordMaxMsgLen)
		for _, chunk := range chunks {
			if err := t.postMessage(ctx, msg.ChannelID, chunk, ""); err != nil {
				return err
			}
		}
		return nil
	}

	chunks := splitMessage(msg.Text, discordMaxMsgLen)
	for _, chunk := range chunks {
		body := map[string]interface{}{
			"content":  chunk,
			"username": msg.DisplayName,
		}
		if msg.AvatarURL != "" {
			body["avatar_url"] = msg.AvatarURL
		}
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("discord: marshal webhook body: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("discord: create webhook request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := t.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("discord: webhook send: %w", err)
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("discord: webhook send returned %d", resp.StatusCode)
		}
	}
	return nil
}

// getOrCreateWebhook returns a webhook URL for the given channel, creating one if needed.
func (t *DiscordTransport) getOrCreateWebhook(ctx context.Context, channelID string) (string, error) {
	t.webhooksMu.Lock()
	if t.webhooks == nil {
		t.webhooks = make(map[string]string)
	}
	if url, ok := t.webhooks[channelID]; ok {
		t.webhooksMu.Unlock()
		return url, nil
	}
	t.webhooksMu.Unlock()

	// Check for existing webhooks we own.
	listURL := discordAPIBase + "/channels/" + channelID + "/webhooks"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+t.token)
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var hooks []struct {
		ID    string `json:"id"`
		Token string `json:"token"`
		Name  string `json:"name"`
	}
	if resp.StatusCode == http.StatusOK {
		_ = json.NewDecoder(resp.Body).Decode(&hooks)
	}
	for _, h := range hooks {
		if h.Name == "capabot-persona" && h.Token != "" {
			url := discordAPIBase + "/webhooks/" + h.ID + "/" + h.Token
			t.webhooksMu.Lock()
			t.webhooks[channelID] = url
			t.webhooksMu.Unlock()
			return url, nil
		}
	}

	// Create a new webhook.
	createBody, _ := json.Marshal(map[string]string{"name": "capabot-persona"})
	createReq, err := http.NewRequestWithContext(ctx, http.MethodPost, listURL, bytes.NewReader(createBody))
	if err != nil {
		return "", err
	}
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("Authorization", "Bot "+t.token)
	createResp, err := t.httpClient.Do(createReq)
	if err != nil {
		return "", err
	}
	defer createResp.Body.Close()
	if createResp.StatusCode < 200 || createResp.StatusCode >= 300 {
		return "", fmt.Errorf("discord: create webhook returned %d", createResp.StatusCode)
	}

	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		return "", fmt.Errorf("discord: decode webhook response: %w", err)
	}
	url := discordAPIBase + "/webhooks/" + created.ID + "/" + created.Token
	t.webhooksMu.Lock()
	t.webhooks[channelID] = url
	t.webhooksMu.Unlock()
	return url, nil
}

// splitMessage splits text into chunks of at most maxLen runes,
// breaking on whitespace where possible to avoid splitting mid-word.
func splitMessage(text string, maxLen int) []string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(runes) > 0 {
		if len(runes) <= maxLen {
			chunks = append(chunks, string(runes))
			break
		}

		// Try to find a whitespace boundary to split on.
		cutAt := maxLen
		for i := maxLen - 1; i > maxLen/2; i-- {
			if runes[i] == ' ' || runes[i] == '\n' {
				cutAt = i + 1
				break
			}
		}

		chunks = append(chunks, string(runes[:cutAt]))
		runes = runes[cutAt:]
	}
	return chunks
}

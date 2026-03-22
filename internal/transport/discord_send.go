package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Send delivers an outbound message to a Discord channel, splitting on the
// 2000-character limit when necessary.
func (t *DiscordTransport) Send(ctx context.Context, msg OutboundMessage) error {
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

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/polymath/gostaff/internal/memory"
)

func (s *Server) handleDefaultModelGet(w http.ResponseWriter, r *http.Request) {
	v, _ := s.store.GetSetting(r.Context(), "default_model")
	writeJSON(w, map[string]string{"default_model": v})
}

func (s *Server) handleDefaultModelPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DefaultModel string `json:"default_model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.store.SetSetting(r.Context(), "default_model", body.DefaultModel); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSummarizationModelGet(w http.ResponseWriter, r *http.Request) {
	v, _ := s.store.GetSetting(r.Context(), "summarization_model")
	writeJSON(w, map[string]string{"summarization_model": v})
}

func (s *Server) handleSummarizationModelPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SummarizationModel string `json:"summarization_model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.store.SetSetting(r.Context(), "summarization_model", body.SummarizationModel); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSystemPromptGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.GetSystemPrompt(r.Context())
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"system_prompt": v})
}

func (s *Server) handleSystemPromptPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SystemPrompt string `json:"system_prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.store.SetSystemPrompt(r.Context(), body.SystemPrompt); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePeopleList(w http.ResponseWriter, r *http.Request) {
	people, err := s.store.ListPeople(r.Context())
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if people == nil {
		people = []memory.Person{}
	}
	writeJSON(w, people)
}

func (s *Server) handlePeopleCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name           string   `json:"name"`
		Prompt         string   `json:"prompt"`
		Username       string   `json:"username"`
		AvatarURL      string   `json:"avatar_url"`
		AvatarPosition string   `json:"avatar_position"`
		Tags           []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Name == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}
	if body.Username != "" && strings.ContainsAny(body.Username, " \t\n") {
		writeError(w, "username cannot contain spaces", http.StatusBadRequest)
		return
	}
	id, err := s.store.CreatePerson(r.Context(), memory.Person{
		Name:           body.Name,
		Prompt:         body.Prompt,
		Username:       body.Username,
		AvatarURL:      body.AvatarURL,
		AvatarPosition: body.AvatarPosition,
		Tags:           body.Tags,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "duplicate key") {
			writeError(w, "a person named \""+body.Name+"\" already exists", http.StatusConflict)
			return
		}
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Sync Discord roles if configured.
	if s.discordRoles != nil {
		if body.Username != "" {
			roleID, err := s.discordRoles.CreateRole(r.Context(), body.Username)
			if err != nil {
				s.logger.Warn().Err(err).Str("person", body.Username).Msg("failed to create Discord role")
			} else {
				_ = s.store.SetPersonDiscordRoleID(r.Context(), id, roleID)
			}
		}
		s.syncTagRoles(r.Context(), body.Tags)
	}

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"id": id})
}

func (s *Server) handlePeopleUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var body struct {
		Name           string   `json:"name"`
		Prompt         string   `json:"prompt"`
		Username       string   `json:"username"`
		AvatarURL      string   `json:"avatar_url"`
		AvatarPosition string   `json:"avatar_position"`
		Tags           []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.store.UpdatePerson(r.Context(), memory.Person{
		ID:             id,
		Name:           body.Name,
		Prompt:         body.Prompt,
		Username:       body.Username,
		AvatarURL:      body.AvatarURL,
		AvatarPosition: body.AvatarPosition,
		Tags:           body.Tags,
	}); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Sync Discord roles on update.
	if s.discordRoles != nil {
		if body.Username != "" {
			existing, err := s.store.GetPersonByName(r.Context(), body.Name)
			if err == nil && existing.DiscordRoleID != "" {
				_ = s.discordRoles.UpdateRole(r.Context(), existing.DiscordRoleID, body.Username)
			} else if err == nil && existing.DiscordRoleID == "" {
				roleID, err := s.discordRoles.CreateRole(r.Context(), body.Username)
				if err == nil {
					_ = s.store.SetPersonDiscordRoleID(r.Context(), id, roleID)
				}
			}
		}
		s.syncTagRoles(r.Context(), body.Tags)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePeopleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	// Delete Discord role before removing from DB.
	if s.discordRoles != nil {
		people, _ := s.store.ListPeople(r.Context())
		for _, p := range people {
			if p.ID == id && p.DiscordRoleID != "" {
				_ = s.discordRoles.DeleteRole(r.Context(), p.DiscordRoleID)
				break
			}
		}
	}
	if err := s.store.DeletePerson(r.Context(), id); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// syncTagRoles creates Discord roles for any new tags that don't have one yet.
func (s *Server) syncTagRoles(ctx context.Context, tags []string) {
	if s.discordRoles == nil || s.store == nil {
		return
	}
	existing, _ := s.store.ListDiscordTagRoles(ctx)
	for _, tag := range tags {
		if _, ok := existing[tag]; !ok {
			roleID, err := s.discordRoles.CreateRole(ctx, tag)
			if err != nil {
				s.logger.Warn().Err(err).Str("tag", tag).Msg("failed to create Discord tag role")
			} else {
				_ = s.store.UpsertDiscordTagRole(ctx, tag, roleID)
			}
		}
	}
}

func (s *Server) avatarsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gostaff", "avatars")
}

func (s *Server) handleAvatarUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20) // 2 MB
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, "file required (max 2MB)", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".gif" && ext != ".webp" {
		writeError(w, "only png, jpg, gif, webp allowed", http.StatusBadRequest)
		return
	}

	dir := s.avatarsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, "failed to create avatars dir", http.StatusInternalServerError)
		return
	}

	b := make([]byte, 8)
	_, _ = rand.Read(b)
	filename := hex.EncodeToString(b) + ext

	dst, err := os.Create(filepath.Join(dir, filename))
	if err != nil {
		writeError(w, "failed to save file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		writeError(w, "failed to write file", http.StatusInternalServerError)
		return
	}

	url := fmt.Sprintf("/api/avatars/%s", filename)
	writeJSON(w, map[string]string{"url": url})
}

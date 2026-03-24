package api

import (
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

	"github.com/polymath/capabot/internal/memory"
)

func (s *Server) handlePersonasList(w http.ResponseWriter, r *http.Request) {
	personas, err := s.store.ListPersonas(r.Context())
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if personas == nil {
		personas = []memory.Persona{}
	}
	writeJSON(w, personas)
}

func (s *Server) handlePersonasCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string   `json:"name"`
		Prompt    string   `json:"prompt"`
		Username  string   `json:"username"`
		AvatarURL string   `json:"avatar_url"`
		Tags      []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Name == "" {
		writeError(w, "name is required", http.StatusBadRequest)
		return
	}
	if strings.ContainsAny(body.Name, " \t\n") {
		writeError(w, "name cannot contain spaces", http.StatusBadRequest)
		return
	}
	id, err := s.store.CreatePersona(r.Context(), memory.Persona{
		Name:      body.Name,
		Prompt:    body.Prompt,
		Username:  body.Username,
		AvatarURL: body.AvatarURL,
		Tags:      body.Tags,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "duplicate key") {
			writeError(w, "a persona named \""+body.Name+"\" already exists", http.StatusConflict)
			return
		}
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"id": id})
}

func (s *Server) handlePersonasUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	var body struct {
		Name      string   `json:"name"`
		Prompt    string   `json:"prompt"`
		Username  string   `json:"username"`
		AvatarURL string   `json:"avatar_url"`
		Tags      []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.store.UpdatePersona(r.Context(), memory.Persona{
		ID:        id,
		Name:      body.Name,
		Prompt:    body.Prompt,
		Username:  body.Username,
		AvatarURL: body.AvatarURL,
		Tags:      body.Tags,
	}); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePersonasDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := s.store.DeletePersona(r.Context(), id); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) avatarsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".capabot", "avatars")
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

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
)

const maxArtifactSize = uint64(4 << 30)

// ArtifactRecord is metadata only. Bytes live in the controller artifact store,
// keeping large files out of the main JSON state snapshot.
type ArtifactRecord struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   uint64 `json:"size"`
	SHA256 string `json:"sha256"`
	Kind   string `json:"kind,omitempty"`
}

func (c *Controller) artifactStoreDir() string {
	if c.artifactDir != "" {
		return c.artifactDir
	}
	if configured := strings.TrimSpace(os.Getenv("NODREN_ARTIFACT_DIR")); configured != "" {
		return configured
	}
	if c.statePath != "" {
		return c.statePath + ".artifacts"
	}
	return filepath.Join(os.TempDir(), "nodren-artifacts")
}

func (c *Controller) artifactPath(id string) string {
	return filepath.Join(c.artifactStoreDir(), id+".bin")
}

func (c *Controller) handleArtifacts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		digest := strings.TrimSpace(r.URL.Query().Get("sha256"))
		if len(digest) != sha256.Size*2 {
			http.Error(w, "sha256 lookup requires a 64-character digest", http.StatusBadRequest)
			return
		}
		if _, err := hex.DecodeString(digest); err != nil {
			http.Error(w, "sha256 lookup requires hexadecimal digest", http.StatusBadRequest)
			return
		}
		requestedSize := uint64(0)
		if value := strings.TrimSpace(r.URL.Query().Get("size")); value != "" {
			parsed, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				http.Error(w, "invalid artifact size", http.StatusBadRequest)
				return
			}
			requestedSize = parsed
		}
		c.mu.RLock()
		for _, record := range c.artifacts {
			if strings.EqualFold(record.SHA256, digest) && (requestedSize == 0 || record.Size == requestedSize) {
				copy := *record
				c.mu.RUnlock()
				writeJSON(w, http.StatusOK, copy)
				return
			}
		}
		c.mu.RUnlock()
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := os.MkdirAll(c.artifactStoreDir(), 0o755); err != nil {
		http.Error(w, "artifact storage unavailable", http.StatusInternalServerError)
		return
	}
	temporary, err := os.CreateTemp(c.artifactStoreDir(), ".upload-*")
	if err != nil {
		http.Error(w, "artifact storage unavailable", http.StatusInternalServerError)
		return
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	hasher := sha256.New()
	limited := io.LimitReader(r.Body, int64(maxArtifactSize)+1)
	size, copyErr := io.Copy(io.MultiWriter(temporary, hasher), limited)
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil {
		http.Error(w, "artifact upload failed", http.StatusBadRequest)
		return
	}
	if uint64(size) > maxArtifactSize {
		http.Error(w, "artifact exceeds 4 GiB limit", http.StatusRequestEntityTooLarge)
		return
	}
	name := strings.TrimSpace(r.Header.Get("X-Nodren-Artifact-Name"))
	if name == "" {
		name = strings.TrimSpace(r.URL.Query().Get("name"))
	}
	if name == "" {
		name = "artifact"
	}
	if !safeArtifactName(name) {
		http.Error(w, "artifact name must be a relative file name", http.StatusBadRequest)
		return
	}
	kind := strings.TrimSpace(r.Header.Get("X-Nodren-Artifact-Kind"))
	if kind == "" {
		kind = "input"
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if expected := strings.TrimSpace(r.Header.Get("X-Nodren-Artifact-SHA256")); expected != "" && !strings.EqualFold(expected, digest) {
		http.Error(w, "artifact checksum mismatch", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	for _, existing := range c.artifacts {
		if existing.Size == uint64(size) && strings.EqualFold(existing.SHA256, digest) {
			c.mu.Unlock()
			writeJSON(w, http.StatusOK, *existing)
			return
		}
	}
	c.mu.Unlock()
	id := fmt.Sprintf("ART-%06d", atomic.AddUint64(&c.nextArtifact, 1))
	finalPath := c.artifactPath(id)
	if err := os.Rename(temporaryName, finalPath); err != nil {
		http.Error(w, "artifact storage unavailable", http.StatusInternalServerError)
		return
	}
	record := &ArtifactRecord{ID: id, Name: name, Size: uint64(size), SHA256: digest, Kind: kind}
	c.mu.Lock()
	c.artifacts[id] = record
	c.persistLocked()
	c.mu.Unlock()
	writeJSON(w, http.StatusCreated, *record)
}

func safeArtifactName(name string) bool {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return false
	}
	for _, value := range name {
		if value == 0 || value == '/' || value == '\\' || value == '"' || value == '\r' || value == '\n' || value < 0x20 {
			return false
		}
	}
	return true
}

func (c *Controller) handleArtifact(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/artifacts/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	c.mu.RLock()
	record, ok := c.artifacts[id]
	if ok {
		copy := *record
		record = &copy
	}
	c.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "metadata" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, *record)
		return
	}
	if len(parts) != 1 || r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	file, err := os.Open(c.artifactPath(id))
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "artifact storage unavailable", http.StatusInternalServerError)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+record.Name+`"`)
	w.Header().Set("X-Nodren-Artifact-SHA256", record.SHA256)
	w.Header().Set("Content-Length", strconv.FormatUint(record.Size, 10))
	if _, err := io.Copy(w, file); err != nil {
		return
	}
}

func (c *Controller) artifactMetadata(id string) (ArtifactRecord, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	record, ok := c.artifacts[id]
	if !ok {
		return ArtifactRecord{}, false
	}
	return *record, true
}

func decodeArtifactJSON(data []byte) (ArtifactRecord, error) {
	var record ArtifactRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return record, err
	}
	if record.ID == "" || record.SHA256 == "" {
		return record, errors.New("artifact metadata is incomplete")
	}
	return record, nil
}

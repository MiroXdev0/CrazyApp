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

func (c *Controller) beginWorkerArtifact(nodeID string, data []byte) error {
	taskID, spec, err := decodeArtifactBegin(data)
	if err != nil {
		return err
	}
	if spec.ID == "" || !safeArtifactName(spec.Name) || spec.Size > maxArtifactSize {
		return errors.New("invalid worker output artifact metadata")
	}
	c.mu.Lock()
	jobID := c.taskToJob[taskID]
	job := c.jobs[jobID]
	if job == nil || c.taskToNode[taskID] != nodeID || job.Task == nil {
		c.mu.Unlock()
		return errors.New("worker output artifact is not associated with an active task")
	}
	declared := false
	for _, output := range job.Task.OutputArtifacts {
		if output.ID == spec.ID && output.Name == spec.Name {
			declared = true
			break
		}
	}
	if !declared {
		c.mu.Unlock()
		return fmt.Errorf("worker output artifact %s was not declared by task", spec.ID)
	}
	if previous := c.workerArtifacts[taskID]; previous != nil {
		_ = os.Remove(previous.path)
	}
	if err := os.MkdirAll(c.artifactStoreDir(), 0o755); err != nil {
		c.mu.Unlock()
		return err
	}
	temporary, err := os.CreateTemp(c.artifactStoreDir(), ".worker-output-*")
	if err != nil {
		c.mu.Unlock()
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporary.Name())
		c.mu.Unlock()
		return err
	}
	c.workerArtifacts[taskID] = &workerArtifactUpload{taskID: taskID, nodeID: nodeID, spec: spec, path: temporary.Name()}
	c.mu.Unlock()
	return nil
}

func (c *Controller) appendWorkerArtifact(nodeID string, data []byte) error {
	taskID, artifactID, offset, chunk, err := decodeArtifactChunk(data)
	if err != nil {
		return err
	}
	c.mu.Lock()
	upload := c.workerArtifacts[taskID]
	if upload == nil || upload.nodeID != nodeID || upload.spec.ID != artifactID {
		c.mu.Unlock()
		return errors.New("worker output artifact was not started")
	}
	if offset != upload.nextSize || upload.nextSize > upload.spec.Size || uint64(len(chunk)) > upload.spec.Size-upload.nextSize {
		c.mu.Unlock()
		return errors.New("invalid worker output artifact offset or size")
	}
	file, err := os.OpenFile(upload.path, os.O_APPEND|os.O_WRONLY, 0)
	if err == nil {
		_, err = file.Write(chunk)
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err == nil {
		upload.nextSize += uint64(len(chunk))
	}
	c.mu.Unlock()
	return err
}

func (c *Controller) finishWorkerArtifact(nodeID string, data []byte) error {
	taskID, artifactID, err := decodeArtifactEnd(data)
	if err != nil {
		return err
	}
	c.mu.Lock()
	upload := c.workerArtifacts[taskID]
	if upload == nil || upload.nodeID != nodeID || upload.spec.ID != artifactID {
		c.mu.Unlock()
		return errors.New("worker output artifact was not started")
	}
	delete(c.workerArtifacts, taskID)
	c.mu.Unlock()
	if upload.nextSize != upload.spec.Size {
		_ = os.Remove(upload.path)
		return errors.New("worker output artifact size mismatch")
	}
	file, err := os.Open(upload.path)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	size, copyErr := io.Copy(hasher, file)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(upload.path)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(upload.path)
		return closeErr
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if uint64(size) != upload.spec.Size || !strings.EqualFold(digest, upload.spec.SHA256) {
		_ = os.Remove(upload.path)
		return errors.New("worker output artifact checksum mismatch")
	}
	finalPath := c.artifactPath(upload.spec.ID)
	if err := os.Rename(upload.path, finalPath); err != nil {
		_ = os.Remove(upload.path)
		return err
	}
	record := &ArtifactRecord{ID: upload.spec.ID, Name: upload.spec.Name, Size: upload.spec.Size, SHA256: digest, Kind: "output"}
	c.mu.Lock()
	if existing := c.artifacts[record.ID]; existing != nil && (existing.SHA256 != record.SHA256 || existing.Size != record.Size) {
		c.mu.Unlock()
		_ = os.Remove(finalPath)
		return errors.New("worker output artifact id already exists with different content")
	}
	c.artifacts[record.ID] = record
	c.taskArtifacts[taskID] = append(c.taskArtifacts[taskID], TaskArtifact{ID: record.ID, Name: record.Name, Size: record.Size, SHA256: record.SHA256, Kind: record.Kind})
	c.persistLocked()
	c.mu.Unlock()
	return nil
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

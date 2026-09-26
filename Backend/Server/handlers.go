package main

import (
	"encoding/json"
	"net/http"
	"sync"
)

type Job struct {
	ID        string `json:"id"`
	Command   string `json:"command"`
	Priority  int    `json:"priority"`
	Status    string `json:"status"`
	WorkerID  string `json:"worker_id,omitempty"`
	CPUCores  int    `json:"cpu_cores,omitempty"`
	RAMGB     int    `json:"ram_gb,omitempty"`
	GPUReq    bool   `json:"gpu_required,omitempty"`
}

type Node struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	CPUCores int    `json:"cpu_cores"`
	RAMGB    int    `json:"ram_gb"`
	GPU      string `json:"gpu,omitempty"`
	Online   bool   `json:"online"`
}

type ServiceState struct {
	mu      sync.RWMutex
	Nodes   map[string]Node
	Jobs    map[string]Job
	Version string
}

func newServiceState() *ServiceState {
	return &ServiceState{
		Nodes: map[string]Node{
			"node-01": {ID: "node-01", Host: "desktop-01", OS: "Windows", Arch: "x64", CPUCores: 12, RAMGB: 16, GPU: "RTX", Online: true},
			"node-02": {ID: "node-02", Host: "desktop-02", OS: "Linux", Arch: "arm64", CPUCores: 8, RAMGB: 32, GPU: "NVIDIA", Online: true},
		},
		Jobs: map[string]Job{
			"JOB-1847": {ID: "JOB-1847", Command: "simulation.exe", Priority: 5, Status: "queued", CPUCores: 4, RAMGB: 8},
		},
		Version: "1.0.0",
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *ServiceState) healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "nexus-controller",
		"version": s.Version,
		"nodes":   len(s.Nodes),
		"jobs":    len(s.Jobs),
	})
}

func (s *ServiceState) nodesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	nodes := make([]Node, 0, len(s.Nodes))
	for _, node := range s.Nodes {
		nodes = append(nodes, node)
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (s *ServiceState) jobsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		jobs := make([]Job, 0, len(s.Jobs))
		for _, job := range s.Jobs {
			jobs = append(jobs, job)
		}
		writeJSON(w, http.StatusOK, jobs)
	case http.MethodPost:
		var job Job
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if job.ID == "" {
			job.ID = "JOB-auto"
		}
		if job.Status == "" {
			job.Status = "queued"
		}
		s.mu.Lock()
		s.Jobs[job.ID] = job
		s.mu.Unlock()
		writeJSON(w, http.StatusCreated, job)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *ServiceState) statsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"online_nodes": len(s.Nodes),
		"queued_jobs":  len(s.Jobs),
		"version":      s.Version,
	})
}

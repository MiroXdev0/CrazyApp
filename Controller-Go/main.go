package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type NodeState string

const (
	NodeReady   NodeState = "READY"
	NodeBusy    NodeState = "BUSY"
	NodeLost    NodeState = "LOST"
	NodeOffline NodeState = "OFFLINE"
)

type NodeRecord struct {
	Info          NodeInfo  `json:"info"`
	State         NodeState `json:"state"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	ConnectedAt   time.Time `json:"connected_at"`
}

type JobStatus string

const (
	JobQueued    JobStatus = "QUEUED"
	JobRunning   JobStatus = "RUNNING"
	JobCompleted JobStatus = "COMPLETED"
	JobFailed    JobStatus = "FAILED"
)

type Job struct {
	ID           string               `json:"id"`
	Command      string               `json:"command"`
	Priority     uint8                `json:"priority"`
	Requirements ResourceRequirements `json:"requirements"`
	PayloadB64   string               `json:"payload_base64,omitempty"`
	Status       JobStatus            `json:"status"`
	NodeID       string               `json:"node_id,omitempty"`
	Result       *TaskResult          `json:"result,omitempty"`
	CreatedAt    time.Time            `json:"created_at"`
	UpdatedAt    time.Time            `json:"updated_at"`
}

type jobRequest struct {
	ID           string               `json:"id"`
	Command      string               `json:"command"`
	Priority     uint8                `json:"priority"`
	Requirements ResourceRequirements `json:"requirements"`
	PayloadB64   string               `json:"payload_base64,omitempty"`
}

type session struct {
	conn     net.Conn
	send     chan []byte
	done     chan struct{}
	closeOne sync.Once
	nodeID   string
}

func newSession(conn net.Conn) *session {
	return &session{
		conn: conn,
		send: make(chan []byte, 256),
		done: make(chan struct{}),
	}
}

func (s *session) enqueue(typ MessageType, requestID uint64, payload []byte) error {
	var buf []byte
	if typ == 0 {
		return errors.New("invalid message type")
	}
	buf = make([]byte, 0, frameHeaderSize+len(payload))
	// Build the frame once so the writer loop owns only I/O.
	header := make([]byte, frameHeaderSize)
	header[0] = 0x4E
	header[1] = 0x44
	header[2] = 0x52
	header[3] = 0x4E
	header[4] = byte(protocolVersion)
	header[5] = byte(protocolVersion >> 8)
	header[6] = byte(typ)
	header[7] = byte(typ >> 8)
	for i := 0; i < 8; i++ {
		header[8+i] = byte(requestID >> (8 * i))
	}
	n := uint32(len(payload))
	header[16] = byte(n)
	header[17] = byte(n >> 8)
	header[18] = byte(n >> 16)
	header[19] = byte(n >> 24)
	buf = append(buf, header...)
	buf = append(buf, payload...)

	select {
	case s.send <- buf:
		return nil
	case <-s.done:
		return errors.New("session closed")
	default:
		return errors.New("session outbound queue saturated")
	}
}

func (s *session) close() {
	s.closeOne.Do(func() {
		close(s.done)
		_ = s.conn.Close()
	})
}

type Controller struct {
	mu         sync.RWMutex
	nodes      map[string]*NodeRecord
	sessions   map[string]*session
	jobs       map[string]*Job
	taskToJob  map[uint64]string
	taskToNode map[uint64]string

	nextTask uint64
	nextJob  uint64

	tcpAddr  string
	httpAddr string
}

func NewController(tcpAddr, httpAddr string) *Controller {
	return &Controller{
		nodes:      make(map[string]*NodeRecord),
		sessions:   make(map[string]*session),
		jobs:       make(map[string]*Job),
		taskToJob:  make(map[uint64]string),
		taskToNode: make(map[uint64]string),
		tcpAddr:    tcpAddr,
		httpAddr:   httpAddr,
	}
}

func (c *Controller) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", c.tcpAddr)
	if err != nil {
		return fmt.Errorf("node listener: %w", err)
	}
	defer listener.Close()

	httpServer := &http.Server{
		Addr:              c.httpAddr,
		Handler:           c.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http server: %v", err)
		}
	}()

	go c.scheduleLoop(ctx)
	go c.healthLoop(ctx)

	log.Printf("Nodren controller: nodes=%s http=%s cpu=%d", c.tcpAddr, c.httpAddr, runtime.NumCPU())

	go func() {
		<-ctx.Done()
		_ = listener.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		c.mu.Lock()
		for _, s := range c.sessions {
			s.close()
		}
		c.mu.Unlock()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				if errors.Is(err, net.ErrClosed) {
					return nil
				}
				continue
			}
		}
		s := newSession(conn)
		go c.handleSession(s)
	}
}

func (c *Controller) handleSession(s *session) {
	defer s.close()

	go func() {
		for {
			select {
			case data := <-s.send:
				if _, err := s.conn.Write(data); err != nil {
					s.close()
					return
				}
			case <-s.done:
				return
			}
		}
	}()

	for {
		_ = s.conn.SetReadDeadline(time.Now().Add(45 * time.Second))
		f, err := readFrame(s.conn)
		if err != nil {
			c.handleDisconnect(s)
			return
		}

		switch f.Type {
		case MsgRegister:
			info, err := decodeRegister(f.Payload)
			if err != nil {
				_ = s.enqueue(MsgError, f.RequestID, encodeError(err.Error()))
				return
			}
			now := time.Now()
			c.mu.Lock()
			if old := c.sessions[info.ID]; old != nil && old != s {
				old.close()
			}
			s.nodeID = info.ID
			c.sessions[info.ID] = s
			c.nodes[info.ID] = &NodeRecord{
				Info: info, State: NodeReady,
				LastHeartbeat: now, ConnectedAt: now,
			}
			c.mu.Unlock()

			_ = s.enqueue(MsgRegisterAck, f.RequestID, []byte("registered"))
			_ = s.enqueue(MsgReady, f.RequestID, []byte("ready"))

		case MsgHeartbeat:
			if s.nodeID == "" {
				return
			}
			if _, err := decodeHeartbeat(f.Payload); err != nil {
				_ = s.enqueue(MsgError, f.RequestID, encodeError(err.Error()))
				return
			}
			c.mu.Lock()
			if n := c.nodes[s.nodeID]; n != nil {
				n.LastHeartbeat = time.Now()
				if n.State == NodeLost {
					n.State = NodeReady
				}
			}
			c.mu.Unlock()
			_ = s.enqueue(MsgHeartbeatAck, f.RequestID, encodeHeartbeat(time.Now().UnixMilli()))

		case MsgTaskResultBatch:
			results, err := decodeTaskResultBatch(f.Payload)
			if err != nil {
				_ = s.enqueue(MsgError, f.RequestID, encodeError(err.Error()))
				return
			}
			c.applyResults(results)

		case MsgGoodbye:
			c.handleDisconnect(s)
			return

		default:
			_ = s.enqueue(MsgError, f.RequestID, encodeError("unsupported message type"))
		}
	}
}

func (c *Controller) handleDisconnect(s *session) {
	if s.nodeID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	current := c.sessions[s.nodeID]
	if current == s {
		delete(c.sessions, s.nodeID)
	}
	if node := c.nodes[s.nodeID]; node != nil {
		node.State = NodeLost
	}

	for _, job := range c.jobs {
		if job.NodeID == s.nodeID && job.Status == JobRunning {
			job.Status = JobQueued
			job.NodeID = ""
			job.UpdatedAt = time.Now()
		}
	}
}

func (c *Controller) canRun(info NodeInfo, req ResourceRequirements) bool {
	if info.CPUCores < req.CPUCores || info.RAMGB < req.RAMGB {
		return false
	}
	if req.GPURequired && info.GPU.Model == "" {
		return false
	}
	return true
}

func (c *Controller) chooseNode(req ResourceRequirements) (string, *session) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var bestID string
	var bestScore uint64 = ^uint64(0)

	for id, node := range c.nodes {
		s := c.sessions[id]
		if node.State != NodeReady || s == nil {
			continue
		}
		if !c.canRun(node.Info, req) {
			continue
		}

		cpuSlack := uint64(node.Info.CPUCores - req.CPUCores)
		ramSlack := node.Info.RAMGB - req.RAMGB
		score := cpuSlack*100 + ramSlack
		if req.GPURequired {
			score -= uint64(min(int64(score), int64(minNonZero(node.Info.GPU.VRAMGB))))
		}
		if score < bestScore {
			bestScore = score
			bestID = id
		}
	}

	if bestID == "" {
		return "", nil
	}
	return bestID, c.sessions[bestID]
}

func minNonZero(v uint64) uint64 {
	if v == 0 {
		return 1
	}
	return v
}

func (c *Controller) scheduleLoop(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.scheduleOnce()
		case <-ctx.Done():
			return
		}
	}
}

func (c *Controller) scheduleOnce() {
	c.mu.RLock()
	queued := make([]*Job, 0)
	for _, job := range c.jobs {
		if job.Status == JobQueued {
			copy := *job
			queued = append(queued, &copy)
		}
	}
	c.mu.RUnlock()

	for _, job := range queued {
		payload, err := base64.StdEncoding.DecodeString(job.PayloadB64)
		if err != nil {
			c.setJobFailed(job.ID, "invalid payload base64: "+err.Error())
			continue
		}

		nodeID, sess := c.chooseNode(job.Requirements)
		if sess == nil {
			continue
		}

		taskID := atomic.AddUint64(&c.nextTask, 1)
		task := Task{
			ID:           taskID,
			JobID:        job.ID,
			Command:      job.Command,
			Priority:     job.Priority,
			Requirements: job.Requirements,
			Payload:      payload,
		}
		data, err := encodeTaskBatch([]Task{task})
		if err != nil {
			c.setJobFailed(job.ID, err.Error())
			continue
		}
		if err := sess.enqueue(MsgTaskBatch, taskID, data); err != nil {
			continue
		}

		c.mu.Lock()
		if live := c.jobs[job.ID]; live != nil && live.Status == JobQueued {
			live.Status = JobRunning
			live.NodeID = nodeID
			live.UpdatedAt = time.Now()
			c.taskToJob[taskID] = job.ID
			c.taskToNode[taskID] = nodeID
			if n := c.nodes[nodeID]; n != nil {
				n.State = NodeBusy
			}
		}
		c.mu.Unlock()
	}
}

func (c *Controller) applyResults(results []TaskResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, result := range results {
		jobID := c.taskToJob[result.TaskID]
		if jobID == "" {
			jobID = result.JobID
		}
		job := c.jobs[jobID]
		if job == nil {
			continue
		}

		if result.Status == "COMPLETED" {
			job.Status = JobCompleted
		} else {
			job.Status = JobFailed
		}
		job.Result = &result
		job.NodeID = result.NodeID
		job.UpdatedAt = time.Now()

		nodeID := c.taskToNode[result.TaskID]
		if nodeID == "" {
			nodeID = result.NodeID
		}
		if n := c.nodes[nodeID]; n != nil && n.State == NodeBusy {
			n.State = NodeReady
		}
		delete(c.taskToJob, result.TaskID)
		delete(c.taskToNode, result.TaskID)
	}
}

func (c *Controller) setJobFailed(id, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if job := c.jobs[id]; job != nil {
		job.Status = JobFailed
		job.UpdatedAt = time.Now()
		job.Result = &TaskResult{JobID: id, Status: "FAILED", Error: reason}
	}
}

func (c *Controller) healthLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			c.mu.Lock()
			for _, n := range c.nodes {
				if now.Sub(n.LastHeartbeat) > 15*time.Second {
					n.State = NodeLost
				}
			}
			c.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

func (c *Controller) createJob(req jobRequest) (*Job, error) {
	if strings.TrimSpace(req.Command) == "" {
		return nil, errors.New("command is required")
	}
	if req.Priority > 100 {
		return nil, errors.New("priority must be 0..100")
	}

	id := req.ID
	if id == "" {
		id = fmt.Sprintf("JOB-%06d", atomic.AddUint64(&c.nextJob, 1))
	}
	now := time.Now()
	job := &Job{
		ID: id, Command: req.Command, Priority: req.Priority,
		Requirements: req.Requirements, PayloadB64: req.PayloadB64,
		Status: JobQueued, CreatedAt: now, UpdatedAt: now,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.jobs[id]; exists {
		return nil, errors.New("job already exists")
	}
	c.jobs[id] = job
	return job, nil
}

func (c *Controller) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", c.handleHealth)
	mux.HandleFunc("/v1/nodes", c.handleNodes)
	mux.HandleFunc("/v1/jobs", c.handleJobs)
	mux.HandleFunc("/v1/jobs/", c.handleJob)
	return mux
}

func (c *Controller) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "nodren-controller",
		"version": "2.0.0",
		"nodes":   c.nodeCount(),
	})
}

func (c *Controller) handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	c.mu.RLock()
	nodes := make([]NodeRecord, 0, len(c.nodes))
	for _, n := range c.nodes {
		nodes = append(nodes, *n)
	}
	c.mu.RUnlock()
	writeJSON(w, http.StatusOK, nodes)
}

func (c *Controller) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		c.mu.RLock()
		jobs := make([]Job, 0, len(c.jobs))
		for _, j := range c.jobs {
			jCopy := *j
			jobs = append(jobs, jCopy)
		}
		c.mu.RUnlock()
		writeJSON(w, http.StatusOK, jobs)

	case http.MethodPost:
		var req jobRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		job, err := c.createJob(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, job)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (c *Controller) handleJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	c.mu.RLock()
	job := c.jobs[id]
	c.mu.RUnlock()
	if job == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (c *Controller) nodeCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.nodes)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tcpAddr := getenv("NODREN_NODE_ADDR", ":9000")
	httpAddr := getenv("NODREN_HTTP_ADDR", ":8080")

	controller := NewController(tcpAddr, httpAddr)
	if err := controller.Start(ctx); err != nil {
		log.Fatal(err)
	}
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func init() {
	_ = strconv.IntSize
}

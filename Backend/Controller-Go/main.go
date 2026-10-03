package main

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sort"
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
	NodePaused  NodeState = "PAUSED"
	NodeLost    NodeState = "LOST"
	NodeOffline NodeState = "OFFLINE"
)

type NodeRecord struct {
	Info               NodeInfo        `json:"info"`
	State              NodeState       `json:"state"`
	AssignedJobs       []string        `json:"assigned_jobs,omitempty"`
	AllocatedCPUCores  uint32          `json:"allocated_cpu_cores"`
	AllocatedRAMGB     uint64          `json:"allocated_ram_gb"`
	AllocatedGPUCount  uint32          `json:"allocated_gpu_count,omitempty"`
	AllocatedVRAMGB    uint64          `json:"allocated_vram_gb,omitempty"`
	AvailableCPUCores  uint32          `json:"available_cpu_cores"`
	AvailableRAMGB     uint64          `json:"available_ram_gb"`
	CapacityScore      float64         `json:"capacity_score"`
	EffectiveCapacity  float64         `json:"effective_capacity"`
	Telemetry          WorkerTelemetry `json:"telemetry"`
	CompletedTasks     uint64          `json:"completed_tasks"`
	FailedTasks        uint64          `json:"failed_tasks"`
	TotalExecutionUS   uint64          `json:"total_execution_us"`
	ObservedThroughput float64         `json:"observed_throughput_units_per_second"`
	PerformanceFactor  float64         `json:"performance_factor"`
	LastHeartbeat      time.Time       `json:"last_heartbeat"`
	ConnectedAt        time.Time       `json:"connected_at"`
}

type JobStatus string

const (
	JobQueued    JobStatus = "QUEUED"
	JobRunning   JobStatus = "RUNNING"
	JobPaused    JobStatus = "PAUSED"
	JobCompleted JobStatus = "COMPLETED"
	JobFailed    JobStatus = "FAILED"
	JobCancelled JobStatus = "CANCELLED"
	JobTimedOut  JobStatus = "TIMED_OUT"
)

type DistributionMode string

const (
	DistributionAutomatic DistributionMode = "automatic"
	DistributionManual    DistributionMode = "manual"
)

type PartitionState string

const (
	PartitionQueued    PartitionState = "QUEUED"
	PartitionAssigned  PartitionState = "ASSIGNED"
	PartitionRunning   PartitionState = "RUNNING"
	PartitionCompleted PartitionState = "COMPLETED"
	PartitionFailed    PartitionState = "FAILED"
	PartitionRequeued  PartitionState = "REQUEUED"
	PartitionCancelled PartitionState = "CANCELLED"
)

func validJobTransition(from, to JobStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case JobQueued:
		return to == JobRunning || to == JobPaused || to == JobCancelled || to == JobFailed || to == JobTimedOut
	case JobRunning:
		return to == JobPaused || to == JobCompleted || to == JobFailed || to == JobCancelled || to == JobTimedOut || to == JobQueued
	case JobPaused:
		return to == JobQueued || to == JobRunning || to == JobCancelled
	default:
		return false
	}
}

func isTerminalJob(status JobStatus) bool {
	return status == JobCompleted || status == JobFailed || status == JobCancelled || status == JobTimedOut
}

func transitionJobLocked(job *Job, next JobStatus) error {
	if job == nil {
		return errors.New("job not found")
	}
	if !validJobTransition(job.Status, next) {
		return fmt.Errorf("invalid job transition %s -> %s", job.Status, next)
	}
	now := time.Now()
	if job.Status != next {
		if next == JobRunning && job.StartedAt == nil {
			job.StartedAt = &now
			job.QueueTimeMS = now.Sub(job.CreatedAt).Milliseconds()
		}
		if next == JobCompleted || next == JobFailed || next == JobCancelled || next == JobTimedOut {
			job.CompletedAt = &now
			if job.StartedAt != nil {
				job.ElapsedMS = now.Sub(*job.StartedAt).Milliseconds()
			}
		}
	}
	job.Status = next
	job.UpdatedAt = now
	return nil
}

func validPartitionTransition(from, to PartitionState) bool {
	if from == to {
		return true
	}
	switch from {
	case PartitionQueued:
		return to == PartitionAssigned || to == PartitionCancelled || to == PartitionFailed
	case PartitionRequeued:
		return to == PartitionAssigned || to == PartitionCancelled || to == PartitionFailed
	case PartitionAssigned, PartitionRunning:
		return to == PartitionRunning || to == PartitionCompleted || to == PartitionFailed || to == PartitionRequeued || to == PartitionCancelled || to == PartitionQueued
	default:
		return false
	}
}

func transitionPartition(partition *Partition, next PartitionState) error {
	if partition == nil {
		return errors.New("partition not found")
	}
	if !validPartitionTransition(partition.State, next) {
		return fmt.Errorf("invalid partition transition %s -> %s", partition.State, next)
	}
	now := time.Now()
	if partition.State != next {
		switch next {
		case PartitionQueued, PartitionRequeued:
			partition.StartedAt = nil
			partition.CompletedAt = nil
			partition.ExecutionDurationUS = 0
		case PartitionAssigned:
			partition.AssignedAt = &now
		case PartitionRunning:
			partition.StartedAt = &now
		case PartitionCompleted, PartitionFailed, PartitionCancelled:
			partition.CompletedAt = &now
			if partition.StartedAt != nil {
				partition.ExecutionDurationUS = uint64(now.Sub(*partition.StartedAt).Microseconds())
			}
		}
	}
	partition.State = next
	partition.UpdatedAt = now
	return nil
}

type Partition struct {
	ID                  string         `json:"id"`
	Index               int            `json:"index"`
	Units               uint64         `json:"units"`
	State               PartitionState `json:"state"`
	TaskID              uint64         `json:"task_id,omitempty"`
	NodeID              string         `json:"node_id,omitempty"`
	Attempt             uint32         `json:"attempt"`
	Result              *TaskResult    `json:"result,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	AssignedAt          *time.Time     `json:"assigned_at,omitempty"`
	StartedAt           *time.Time     `json:"started_at,omitempty"`
	CompletedAt         *time.Time     `json:"completed_at,omitempty"`
	ExecutionDurationUS uint64         `json:"execution_duration_us,omitempty"`
	AssignmentReason    string         `json:"assignment_reason,omitempty"`
	payload             []byte
}

type DistributionInfo struct {
	Mode                DistributionMode `json:"mode"`
	Partitionable       bool             `json:"partitionable"`
	TotalPartitions     int              `json:"total_partitions"`
	CompletedPartitions int              `json:"completed_partitions"`
	RunningPartitions   int              `json:"running_partitions"`
	PendingPartitions   int              `json:"pending_partitions"`
	FailedPartitions    int              `json:"failed_partitions"`
	RequeuedPartitions  int              `json:"requeued_partitions"`
	TotalUnits          uint64           `json:"total_units"`
	CompletedUnits      uint64           `json:"completed_units"`
	ProgressPercent     float64          `json:"progress_percent"`
	ManualAllocations   map[string]uint8 `json:"manual_allocations,omitempty"`
}

type Job struct {
	ID                    string               `json:"id"`
	Command               string               `json:"command"`
	Priority              uint8                `json:"priority"`
	Requirements          ResourceRequirements `json:"requirements"`
	PayloadB64            string               `json:"payload_base64,omitempty"`
	Status                JobStatus            `json:"status"`
	NodeID                string               `json:"node_id,omitempty"`
	NodeIDs               []string             `json:"node_ids,omitempty"`
	Result                *TaskResult          `json:"result,omitempty"`
	Distribution          DistributionInfo     `json:"distribution"`
	Partitions            []Partition          `json:"partitions,omitempty"`
	CreatedAt             time.Time            `json:"created_at"`
	UpdatedAt             time.Time            `json:"updated_at"`
	StartedAt             *time.Time           `json:"started_at,omitempty"`
	CompletedAt           *time.Time           `json:"completed_at,omitempty"`
	QueueTimeMS           int64                `json:"queue_time_ms,omitempty"`
	ElapsedMS             int64                `json:"elapsed_ms,omitempty"`
	Task                  *GeneralTaskSpec     `json:"task,omitempty"`
	Execution             *GeneralTaskResult   `json:"execution_result,omitempty"`
	BatchID               string               `json:"batch_id,omitempty"`
	partitionableOverride *bool
}

type jobRequest struct {
	ID                string               `json:"id"`
	Command           string               `json:"command"`
	Priority          uint8                `json:"priority"`
	Requirements      ResourceRequirements `json:"requirements"`
	PayloadB64        string               `json:"payload_base64,omitempty"`
	DistributionMode  DistributionMode     `json:"distribution_mode,omitempty"`
	ManualAllocations map[string]uint8     `json:"manual_allocations,omitempty"`
	Partitionable     *bool                `json:"partitionable,omitempty"`
}

type distributionRequest struct {
	Mode              DistributionMode `json:"mode"`
	ManualAllocations map[string]uint8 `json:"manual_allocations,omitempty"`
}

type controllerEvent struct {
	Type      string    `json:"type"`
	Resource  string    `json:"resource,omitempty"`
	ID        string    `json:"id,omitempty"`
	Status    string    `json:"status,omitempty"`
	Progress  float64   `json:"progress,omitempty"`
	Stream    string    `json:"stream,omitempty"`
	OutputB64 string    `json:"output_base64,omitempty"`
	Final     bool      `json:"final,omitempty"`
	Time      time.Time `json:"time"`
}

type workerStatsResponse struct {
	Node              NodeRecord `json:"node"`
	CurrentPartitions []string   `json:"current_partitions,omitempty"`
	TelemetryAgeMS    int64      `json:"telemetry_age_ms"`
	SchedulerWeight   float64    `json:"scheduler_weight"`
}

type jobStatsResponse struct {
	Job              Job      `json:"job"`
	QueueTimeMS      int64    `json:"queue_time_ms"`
	ElapsedMS        int64    `json:"elapsed_ms"`
	ActiveWorkers    []string `json:"active_workers,omitempty"`
	SchedulerReasons []string `json:"scheduler_reasons,omitempty"`
}

// clusterStatusResponse separates advertised capacity (total/available
// cores and RAM) from measurements reported by workers. A negative dynamic
// percentage means no connected worker has supplied that measurement yet.
type clusterStatusResponse struct {
	Status                   string    `json:"status"`
	Service                  string    `json:"service"`
	Version                  string    `json:"version"`
	StartedAt                time.Time `json:"started_at"`
	UptimeSeconds            uint64    `json:"uptime_seconds"`
	Workers                  int       `json:"workers"`
	OnlineWorkers            int       `json:"online_workers"`
	OfflineWorkers           int       `json:"offline_workers"`
	StaleWorkers             int       `json:"stale_workers"`
	PausedWorkers            int       `json:"paused_workers"`
	ActiveJobs               int       `json:"active_jobs"`
	QueuedJobs               int       `json:"queued_jobs"`
	CompletedJobs            int       `json:"completed_jobs"`
	FailedJobs               int       `json:"failed_jobs"`
	ActiveTasks              uint64    `json:"active_tasks"`
	TotalCompletedTasks      uint64    `json:"total_completed_tasks"`
	TotalFailedTasks         uint64    `json:"total_failed_tasks"`
	TotalCPUCores            uint64    `json:"total_cpu_cores"`
	AvailableCPUCores        uint64    `json:"available_cpu_cores"`
	TotalRAMGB               uint64    `json:"total_ram_gb"`
	AvailableRAMGB           uint64    `json:"available_ram_gb"`
	TotalGPUs                uint64    `json:"total_gpus"`
	TotalVRAMGB              uint64    `json:"total_vram_gb"`
	AvailableVRAMGB          uint64    `json:"available_vram_gb"`
	MemoryAvailableGB        uint64    `json:"memory_available_gb"`
	CPUUtilizationPercent    float64   `json:"cpu_utilization_percent"`
	MemoryUtilizationPercent float64   `json:"memory_utilization_percent"`
	GPUUtilizationPercent    float64   `json:"gpu_utilization_percent"`
	TelemetryWorkers         int       `json:"telemetry_workers"`
	MemoryTelemetryWorkers   int       `json:"memory_telemetry_workers"`
	GPUTelemetryWorkers      int       `json:"gpu_telemetry_workers"`
	ThroughputUnitsPerSecond float64   `json:"throughput_units_per_second"`
}

type session struct {
	conn     net.Conn
	send     chan []byte
	done     chan struct{}
	closeOne sync.Once
	nodeID   string
}

type workerArtifactUpload struct {
	taskID   uint64
	nodeID   string
	spec     TaskArtifact
	path     string
	nextSize uint64
}

func newSession(conn net.Conn) *session {
	return &session{
		conn: conn,
		send: make(chan []byte, 256),
		done: make(chan struct{}),
	}
}

func (s *session) enqueue(typ MessageType, requestID uint64, payload []byte) error {
	if typ == 0 {
		return errors.New("invalid message type")
	}
	buf := encodeSessionFrame(typ, requestID, payload)
	select {
	case s.send <- buf:
		return nil
	case <-s.done:
		return errors.New("session closed")
	default:
		return errors.New("session outbound queue saturated")
	}
}

// enqueueBlocking is used only for streamed artifact chunks. It applies
// backpressure from the worker's socket writer instead of failing a large
// transfer after the bounded queue fills.
func (s *session) enqueueBlocking(typ MessageType, requestID uint64, payload []byte) error {
	if typ == 0 {
		return errors.New("invalid message type")
	}
	buf := encodeSessionFrame(typ, requestID, payload)
	select {
	case s.send <- buf:
		return nil
	case <-s.done:
		return errors.New("session closed")
	}
}

func encodeSessionFrame(typ MessageType, requestID uint64, payload []byte) []byte {
	buf := make([]byte, 0, frameHeaderSize+len(payload))
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
	return buf
}

func (s *session) close() {
	s.closeOne.Do(func() {
		close(s.done)
		if s.conn != nil {
			_ = s.conn.Close()
		}
	})
}

type Controller struct {
	mu              sync.RWMutex
	nodes           map[string]*NodeRecord
	sessions        map[string]*session
	jobs            map[string]*Job
	taskToJob       map[uint64]string
	taskToNode      map[uint64]string
	taskToPartition map[uint64]string

	nextTask        uint64
	nextJob         uint64
	nextArtifact    uint64
	nextAIExecution uint64
	artifacts       map[string]*ArtifactRecord
	artifactDir     string
	aiPlans         map[string]*AIExecutionPlan
	aiExecutions    map[string]*AIExecution
	workerArtifacts map[uint64]*workerArtifactUpload
	taskArtifacts   map[uint64][]TaskArtifact

	scheduleNotify     chan struct{}
	eventMu            sync.RWMutex
	eventSubscribers   map[uint64]chan controllerEvent
	nextSubscriber     uint64
	lastTelemetryEvent map[string]time.Time
	statePath          string
	shutdownToken      string
	shutdown           func()

	tcpAddr   string
	httpAddr  string
	startedAt time.Time
}

func NewController(tcpAddr, httpAddr string) *Controller {
	return &Controller{
		nodes:              make(map[string]*NodeRecord),
		sessions:           make(map[string]*session),
		jobs:               make(map[string]*Job),
		taskToJob:          make(map[uint64]string),
		taskToNode:         make(map[uint64]string),
		taskToPartition:    make(map[uint64]string),
		artifacts:          make(map[string]*ArtifactRecord),
		aiPlans:            make(map[string]*AIExecutionPlan),
		aiExecutions:       make(map[string]*AIExecution),
		workerArtifacts:    make(map[uint64]*workerArtifactUpload),
		taskArtifacts:      make(map[uint64][]TaskArtifact),
		scheduleNotify:     make(chan struct{}, 1),
		eventSubscribers:   make(map[uint64]chan controllerEvent),
		lastTelemetryEvent: make(map[string]time.Time),
		tcpAddr:            tcpAddr,
		httpAddr:           httpAddr,
		startedAt:          time.Now().UTC(),
	}
}

func (c *Controller) shouldPublishTelemetry(nodeID string) bool {
	now := time.Now()
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	previous := c.lastTelemetryEvent[nodeID]
	if !previous.IsZero() && now.Sub(previous) < 2*time.Second {
		return false
	}
	c.lastTelemetryEvent[nodeID] = now
	return true
}

func (c *Controller) publishEvent(event controllerEvent) {
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	c.eventMu.RLock()
	defer c.eventMu.RUnlock()
	for _, subscriber := range c.eventSubscribers {
		select {
		case subscriber <- event:
		default:
			// Slow clients must not block scheduling or worker sessions.
		}
	}
}

func (c *Controller) subscribeEvents() (uint64, <-chan controllerEvent, func()) {
	subscriber := make(chan controllerEvent, 64)
	c.eventMu.Lock()
	c.nextSubscriber++
	id := c.nextSubscriber
	c.eventSubscribers[id] = subscriber
	c.eventMu.Unlock()
	return id, subscriber, func() {
		c.eventMu.Lock()
		if current, ok := c.eventSubscribers[id]; ok {
			delete(c.eventSubscribers, id)
			close(current)
		}
		c.eventMu.Unlock()
	}
}

func (c *Controller) triggerSchedule() {
	select {
	case c.scheduleNotify <- struct{}{}:
	default:
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
		// The event stream is intentionally long-lived; a write deadline here
		// would disconnect subscribed UI clients after a fixed interval.
		WriteTimeout: 0,
		IdleTimeout:  30 * time.Second,
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
		log.Printf("worker connected remote=%s", conn.RemoteAddr())
		go c.handleSession(s)
	}
}

func (c *Controller) handleSession(s *session) {
	defer s.close()

	go func() {
		for {
			select {
			case data := <-s.send:
				if err := writeAll(s.conn, data); err != nil {
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
			old := c.sessions[info.ID]
			if old != nil && old != s {
				c.requeueNodeJobsLocked(info.ID)
				old.close()
				log.Printf("worker connection replaced id=%s", info.ID)
			}
			s.nodeID = info.ID
			c.sessions[info.ID] = s
			node := &NodeRecord{
				Info: info, State: NodeOffline,
				LastHeartbeat: now, ConnectedAt: now,
			}
			if previousNode := c.nodes[info.ID]; previousNode != nil {
				// A reconnect is the same logical worker. Preserve controller-side
				// history while replacing static registration data and the session.
				node.CompletedTasks = previousNode.CompletedTasks
				node.FailedTasks = previousNode.FailedTasks
				node.TotalExecutionUS = previousNode.TotalExecutionUS
				node.ObservedThroughput = previousNode.ObservedThroughput
				node.PerformanceFactor = previousNode.PerformanceFactor
			}
			node.Telemetry = WorkerTelemetry{Timestamp: now.UTC(), CPUUtilizationPercent: -1, MemoryUtilizationPercent: -1, GPUUtilizationPercent: -1}
			updateNodeCapacity(node)
			c.nodes[info.ID] = node
			c.persistLocked()
			c.mu.Unlock()
			c.publishEvent(controllerEvent{Type: "worker.connected", Resource: "worker", ID: info.ID, Status: string(node.State)})
			log.Printf("worker registered id=%s", info.ID)

			_ = s.enqueue(MsgRegisterAck, f.RequestID, []byte("registered"))

		case MsgHeartbeat:
			if s.nodeID == "" {
				return
			}
			_, telemetry, err := decodeHeartbeat(f.Payload)
			if err != nil {
				_ = s.enqueue(MsgError, f.RequestID, encodeError(err.Error()))
				return
			}
			c.mu.Lock()
			if c.sessions[s.nodeID] == s {
				if n := c.nodes[s.nodeID]; n != nil {
					n.LastHeartbeat = time.Now()
					telemetry.Timestamp = n.LastHeartbeat.UTC()
					if telemetry.MemoryAvailableKnown && n.Info.RAMGB > 0 {
						telemetry.MemoryUtilizationPercent = math.Max(0, math.Min(100, (1-float64(telemetry.MemoryAvailableGB)/float64(n.Info.RAMGB))*100))
					}
					n.Telemetry = telemetry
					updateNodeCapacity(n)
					if n.State == NodeLost {
						n.State = NodeReady
					}
				}
			}
			c.mu.Unlock()
			if c.shouldPublishTelemetry(s.nodeID) {
				c.publishEvent(controllerEvent{Type: "worker.telemetry", Resource: "worker", ID: s.nodeID, Status: string(NodeReady)})
			}
			_ = s.enqueue(MsgHeartbeatAck, f.RequestID, encodeHeartbeat(time.Now().UnixMilli()))

		case MsgReady:
			if s.nodeID == "" {
				_ = s.enqueue(MsgError, f.RequestID, encodeError("worker must register before READY"))
				return
			}
			c.mu.Lock()
			if c.sessions[s.nodeID] == s {
				if n := c.nodes[s.nodeID]; n != nil {
					if n.State != NodePaused {
						n.State = NodeReady
					}
					n.LastHeartbeat = time.Now()
				}
			}
			c.persistLocked()
			c.mu.Unlock()
			c.publishEvent(controllerEvent{Type: "worker.state_changed", Resource: "worker", ID: s.nodeID, Status: string(NodeReady)})
			c.triggerSchedule()
			log.Printf("worker ready id=%s", s.nodeID)

		case MsgTaskResultBatch:
			results, err := decodeTaskResultBatch(f.Payload)
			if err != nil {
				_ = s.enqueue(MsgError, f.RequestID, encodeError(err.Error()))
				return
			}
			c.applyResults(results)

		case MsgTaskResult:
			result, err := decodeGeneralTaskResult(f.Payload)
			if err != nil {
				_ = s.enqueue(MsgError, f.RequestID, encodeError(err.Error()))
				return
			}
			c.applyGeneralTaskResult(result)

		case MsgTaskState:
			output, err := decodeTaskOutput(f.Payload)
			if err != nil {
				_ = s.enqueue(MsgError, f.RequestID, encodeError(err.Error()))
				return
			}
			c.applyTaskOutput(s.nodeID, output)

		case MsgArtifactBegin:
			if err := c.beginWorkerArtifact(s.nodeID, f.Payload); err != nil {
				_ = s.enqueue(MsgError, f.RequestID, encodeError(err.Error()))
			}

		case MsgArtifactChunk:
			if err := c.appendWorkerArtifact(s.nodeID, f.Payload); err != nil {
				_ = s.enqueue(MsgError, f.RequestID, encodeError(err.Error()))
			}

		case MsgArtifactEnd:
			if err := c.finishWorkerArtifact(s.nodeID, f.Payload); err != nil {
				_ = s.enqueue(MsgError, f.RequestID, encodeError(err.Error()))
			}

		case MsgGoodbye:
			c.handleDisconnect(s)
			return

		default:
			_ = s.enqueue(MsgError, f.RequestID, encodeError("unsupported message type"))
		}
	}
}

func writeAll(conn net.Conn, data []byte) error {
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("short frame write")
		}
		data = data[n:]
	}
	return nil
}

func (c *Controller) handleDisconnect(s *session) {
	if s.nodeID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	current := c.sessions[s.nodeID]
	if current != s {
		// A newer connection owns this worker identity. The old handler must
		// not mark the replacement session lost or requeue its work.
		return
	}
	delete(c.sessions, s.nodeID)
	if node := c.nodes[s.nodeID]; node != nil {
		node.State = NodeLost
	}
	for taskID, upload := range c.workerArtifacts {
		if upload.nodeID == s.nodeID {
			_ = os.Remove(upload.path)
			delete(c.workerArtifacts, taskID)
		}
	}
	c.requeueNodeJobsLocked(s.nodeID)
	c.persistLocked()
	c.triggerSchedule()
	log.Printf("worker disconnected id=%s", s.nodeID)
	c.publishEvent(controllerEvent{Type: "worker.disconnected", Resource: "worker", ID: s.nodeID, Status: string(NodeLost)})
}

func (c *Controller) requeueNodeJobsLocked(nodeID string) {
	for _, job := range c.jobs {
		if len(job.Partitions) > 0 {
			changed := false
			for index := range job.Partitions {
				partition := &job.Partitions[index]
				if partition.NodeID != nodeID ||
					(partition.State != PartitionAssigned && partition.State != PartitionRunning) {
					continue
				}
				if partition.TaskID != 0 {
					delete(c.taskToJob, partition.TaskID)
					delete(c.taskToNode, partition.TaskID)
					delete(c.taskToPartition, partition.TaskID)
				}
				c.releaseResourcesLocked(nodeID, job.Requirements)
				if err := transitionPartition(partition, PartitionRequeued); err != nil {
					continue
				}
				partition.TaskID = 0
				partition.NodeID = ""
				partition.Attempt++
				changed = true
			}
			if changed {
				if err := transitionJobLocked(job, JobQueued); err != nil {
					continue
				}
				job.NodeID = ""
				c.updateJobPlacementLocked(job)
				c.publishEvent(controllerEvent{Type: "partition.requeued", Resource: "job", ID: job.ID, Status: string(JobQueued)})
			}
			continue
		}
		if job.NodeID == nodeID && job.Status == JobRunning {
			c.releaseResourcesLocked(nodeID, job.Requirements)
			if err := transitionJobLocked(job, JobQueued); err != nil {
				continue
			}
			job.NodeID = ""
			c.removeAssignedJobLocked(nodeID, job.ID)
			c.publishEvent(controllerEvent{Type: "job.queued", Resource: "job", ID: job.ID, Status: string(JobQueued)})
		}
	}
	if node := c.nodes[nodeID]; node != nil {
		node.AssignedJobs = nil
		node.AllocatedCPUCores = 0
		node.AllocatedRAMGB = 0
		updateNodeCapacity(node)
	}
	for taskID, mappedNodeID := range c.taskToNode {
		if mappedNodeID == nodeID {
			delete(c.taskToNode, taskID)
			delete(c.taskToJob, taskID)
			delete(c.taskToPartition, taskID)
		}
	}
}

func (c *Controller) findPartitionLocked(job *Job, partitionID string) *Partition {
	for index := range job.Partitions {
		if job.Partitions[index].ID == partitionID {
			return &job.Partitions[index]
		}
	}
	return nil
}

func (c *Controller) activePartitionOnNodeLocked(jobID, nodeID string) bool {
	job := c.jobs[jobID]
	if job == nil {
		return false
	}
	for _, partition := range job.Partitions {
		if partition.NodeID == nodeID &&
			(partition.State == PartitionAssigned || partition.State == PartitionRunning) {
			return true
		}
	}
	return false
}

func (c *Controller) updateJobPlacementLocked(job *Job) {
	unique := make(map[string]struct{})
	for _, partition := range job.Partitions {
		if partition.NodeID != "" {
			unique[partition.NodeID] = struct{}{}
		}
	}
	job.NodeIDs = job.NodeIDs[:0]
	for nodeID := range unique {
		job.NodeIDs = append(job.NodeIDs, nodeID)
	}
	sort.Strings(job.NodeIDs)
	if len(job.NodeIDs) == 1 {
		job.NodeID = job.NodeIDs[0]
	} else {
		job.NodeID = ""
	}
}

func (c *Controller) updateJobProgressLocked(job *Job) {
	previousStatus := job.Status
	previousProgress := job.Distribution.ProgressPercent
	completed := 0
	failed := 0
	running := 0
	pending := 0
	requeued := 0
	var completedUnits uint64
	active := false
	queued := false
	for _, partition := range job.Partitions {
		switch partition.State {
		case PartitionCompleted:
			completed++
			completedUnits += partition.Units
		case PartitionFailed:
			failed++
		case PartitionAssigned, PartitionRunning:
			active = true
			running++
		case PartitionQueued:
			queued = true
			pending++
		case PartitionRequeued:
			queued = true
			requeued++
		}
	}
	job.Distribution.CompletedPartitions = completed
	job.Distribution.FailedPartitions = failed
	job.Distribution.RunningPartitions = running
	job.Distribution.PendingPartitions = pending
	job.Distribution.RequeuedPartitions = requeued
	job.Distribution.CompletedUnits = completedUnits
	if job.Distribution.TotalUnits > 0 {
		job.Distribution.ProgressPercent = math.Round((float64(completedUnits)/float64(job.Distribution.TotalUnits))*10000) / 100
	}
	if job.Status != JobPaused && job.Status != JobCancelled {
		if failed > 0 {
			_ = transitionJobLocked(job, JobFailed)
		} else if completed == len(job.Partitions) && completed > 0 {
			_ = transitionJobLocked(job, JobCompleted)
		} else if active {
			_ = transitionJobLocked(job, JobRunning)
		} else if queued {
			_ = transitionJobLocked(job, JobQueued)
		}
	}
	c.updateJobPlacementLocked(job)
	if job.Status != previousStatus {
		c.publishEvent(controllerEvent{Type: "job.state_changed", Resource: "job", ID: job.ID, Status: string(job.Status), Progress: job.Distribution.ProgressPercent})
	} else if job.Distribution.ProgressPercent != previousProgress {
		c.publishEvent(controllerEvent{Type: "job.progress_changed", Resource: "job", ID: job.ID, Status: string(job.Status), Progress: job.Distribution.ProgressPercent})
	}
}

func (c *Controller) releasePartitionLocked(job *Job, partition *Partition) {
	if partition.NodeID == "" {
		return
	}
	nodeID := partition.NodeID
	c.releaseResourcesLocked(nodeID, job.Requirements)
	activeOther := false
	for index := range job.Partitions {
		other := &job.Partitions[index]
		if other != partition && other.NodeID == nodeID &&
			(other.State == PartitionAssigned || other.State == PartitionRunning) {
			activeOther = true
			break
		}
	}
	if !activeOther {
		c.removeAssignedJobLocked(nodeID, job.ID)
	}
	if node := c.nodes[nodeID]; node != nil {
		c.updateNodeStateLocked(node)
	}
}

func (c *Controller) canRun(info NodeInfo, req ResourceRequirements) bool {
	if info.CPUCores < req.CPUCores || info.RAMGB < req.RAMGB {
		return false
	}
	requiredGPU := requiredGPUCount(req)
	availableGPU := availableGPUCount(info)
	if requiredGPU > availableGPU {
		return false
	}
	if req.GPURequired && info.GPU.Model == "" && availableGPU == 0 {
		return false
	}
	if req.VRAMGB > info.GPU.VRAMGB {
		return false
	}
	if req.AcceleratorType != "" && !strings.EqualFold(req.AcceleratorType, info.GPU.Vendor) && !strings.EqualFold(req.AcceleratorType, info.GPU.Runtime) {
		return false
	}
	for _, capability := range req.GPUCapabilities {
		if !containsFold(info.GPU.Capabilities, capability) {
			return false
		}
	}
	return true
}

func requiredCPUCores(req ResourceRequirements) uint32 {
	if req.CPUCores == 0 {
		return 1
	}
	return req.CPUCores
}

func requiredRAMGB(req ResourceRequirements) uint64 {
	return req.RAMGB
}

func requiredGPUCount(req ResourceRequirements) uint32 {
	if req.GPUCount > 0 {
		return req.GPUCount
	}
	if req.GPURequired {
		return 1
	}
	return 0
}

func availableGPUCount(info NodeInfo) uint32 {
	if info.GPU.Count == 0 && info.GPU.Model != "" {
		return 1
	}
	return info.GPU.Count
}

func (c *Controller) canFit(node *NodeRecord, req ResourceRequirements) bool {
	if node == nil || !c.canRun(node.Info, req) {
		return false
	}
	cpu := requiredCPUCores(req)
	ram := requiredRAMGB(req)
	if cpu > node.Info.CPUCores || ram > node.Info.RAMGB {
		return false
	}
	gpu := requiredGPUCount(req)
	availableGPU := availableGPUCount(node.Info)
	if gpu > availableGPU || node.AllocatedGPUCount > availableGPU-gpu {
		return false
	}
	if requiredRAMGB(req) > schedulableRAMGB(node) || req.VRAMGB > schedulableVRAMGB(node) {
		return false
	}
	return node.AllocatedCPUCores <= node.Info.CPUCores-cpu &&
		node.AllocatedRAMGB <= node.Info.RAMGB-ram &&
		node.AllocatedVRAMGB <= node.Info.GPU.VRAMGB-req.VRAMGB
}

// schedulableRAMGB and schedulableVRAMGB combine static capacity with live
// measurements. A missing measurement never becomes a fabricated zero.
func schedulableRAMGB(node *NodeRecord) uint64 {
	if node == nil {
		return 0
	}
	available := node.Info.RAMGB - minUint64(node.AllocatedRAMGB, node.Info.RAMGB)
	if node.Telemetry.MemoryAvailableKnown && node.Telemetry.MemoryAvailableGB < available {
		available = node.Telemetry.MemoryAvailableGB
	}
	return available
}

func schedulableVRAMGB(node *NodeRecord) uint64 {
	if node == nil {
		return 0
	}
	available := node.Info.GPU.VRAMGB - minUint64(node.AllocatedVRAMGB, node.Info.GPU.VRAMGB)
	if node.Telemetry.GPUAvailableVRAMKnown && node.Telemetry.GPUAvailableVRAMGB < available {
		available = node.Telemetry.GPUAvailableVRAMGB
	}
	return available
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func (c *Controller) canRunGeneral(node *NodeRecord, spec *GeneralTaskSpec) bool {
	if node == nil || spec == nil || !c.canRun(node.Info, spec.Requirements) {
		return false
	}
	target := spec.Target
	if target.OS != "" && !strings.EqualFold(target.OS, node.Info.OS) {
		return false
	}
	if target.Arch != "" && !strings.EqualFold(target.Arch, node.Info.Arch) {
		return false
	}
	if len(target.AllowedWorkerIDs) > 0 && !containsFold(target.AllowedWorkerIDs, node.Info.ID) {
		return false
	}
	for _, runtime := range target.RequiredRuntimes {
		if !containsFold(node.Info.Runtimes, runtime) {
			return false
		}
	}
	for _, capability := range target.RequiredCapabilities {
		if !containsFold(node.Info.Capabilities, capability) {
			return false
		}
	}
	if spec.Type != TaskTypeNativeWorkload && !containsFold(node.Info.ExecutionTypes, string(spec.Type)) {
		return false
	}
	return true
}

func (c *Controller) canFitJob(node *NodeRecord, job *Job) bool {
	if job == nil || !c.canFit(node, job.Requirements) {
		return false
	}
	return job.Task == nil || job.Task.Type == TaskTypeNativeWorkload || c.canRunGeneral(node, job.Task)
}

// capacityScore is a deterministic scheduling estimate, not a benchmark.
// CPU contributes one unit per advertised core and memory contributes one
// unit per four GiB. GPU measurements influence eligibility and effective
// capacity, but are not converted into a fabricated performance score.
func capacityScore(info NodeInfo) float64 {
	cpu := info.CPUCores
	if cpu == 0 {
		cpu = 1
	}
	return float64(cpu) + float64(info.RAMGB)/4.0
}

func effectiveCapacity(node *NodeRecord) float64 {
	if node == nil {
		return 0
	}
	availableCPU := uint32(0)
	if node.Info.CPUCores > node.AllocatedCPUCores {
		availableCPU = node.Info.CPUCores - node.AllocatedCPUCores
	}
	availableRAM := uint64(0)
	if node.Info.RAMGB > node.AllocatedRAMGB {
		availableRAM = node.Info.RAMGB - node.AllocatedRAMGB
	}
	cpuFraction := float64(availableCPU) / float64(maxUint32(node.Info.CPUCores, 1))
	ramFraction := 1.0
	if node.Info.RAMGB > 0 {
		ramFraction = float64(availableRAM) / float64(node.Info.RAMGB)
	}
	telemetryFraction := 1.0
	if node.Telemetry.CPUUtilizationPercent >= 0 {
		telemetryFraction = math.Min(telemetryFraction, math.Max(0.05, (100-node.Telemetry.CPUUtilizationPercent)/100))
	}
	if node.Telemetry.MemoryUtilizationPercent >= 0 {
		telemetryFraction = math.Min(telemetryFraction, math.Max(0.05, (100-node.Telemetry.MemoryUtilizationPercent)/100))
	}
	if node.Telemetry.GPUUtilizationKnown && node.Telemetry.GPUUtilizationPercent >= 0 {
		telemetryFraction = math.Min(telemetryFraction, math.Max(0.05, (100-node.Telemetry.GPUUtilizationPercent)/100))
	}
	performance := node.PerformanceFactor
	if performance <= 0 {
		performance = 1
	}
	return capacityScore(node.Info) * math.Min(cpuFraction, ramFraction) * telemetryFraction * performance
}

func maxUint32(value, minimum uint32) uint32 {
	if value < minimum {
		return minimum
	}
	return value
}

func updateNodeCapacity(node *NodeRecord) {
	if node == nil {
		return
	}
	if node.PerformanceFactor <= 0 {
		node.PerformanceFactor = 1
	}
	node.AvailableCPUCores = node.Info.CPUCores - minUint32(node.AllocatedCPUCores, node.Info.CPUCores)
	node.AvailableRAMGB = node.Info.RAMGB - minUint64(node.AllocatedRAMGB, node.Info.RAMGB)
	node.CapacityScore = capacityScore(node.Info)
	node.EffectiveCapacity = effectiveCapacity(node)
}

func (c *Controller) recordNodeResultLocked(nodeID string, units uint64, result TaskResult) {
	node := c.nodes[nodeID]
	if node == nil {
		return
	}
	if result.Status == "COMPLETED" {
		node.CompletedTasks++
	} else {
		node.FailedTasks++
	}
	if result.DurationUS > 0 {
		node.TotalExecutionUS += result.DurationUS
		throughput := float64(units) / (float64(result.DurationUS) / 1_000_000)
		if node.ObservedThroughput == 0 {
			node.ObservedThroughput = throughput
		} else {
			node.ObservedThroughput = node.ObservedThroughput*0.7 + throughput*0.3
		}
		node.PerformanceFactor = math.Max(0.5, math.Min(1.5, 0.5+node.ObservedThroughput/(node.ObservedThroughput+1)))
	}
	updateNodeCapacity(node)
}

func minUint32(left, right uint32) uint32 {
	if left < right {
		return left
	}
	return right
}

func minUint64(left, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}

func (c *Controller) updateNodeStateLocked(node *NodeRecord) {
	if node == nil || node.State == NodeLost || node.State == NodeOffline || node.State == NodePaused {
		return
	}
	if node.AllocatedCPUCores >= node.Info.CPUCores ||
		(node.Info.RAMGB > 0 && node.AllocatedRAMGB >= node.Info.RAMGB) ||
		(availableGPUCount(node.Info) > 0 && node.AllocatedGPUCount >= availableGPUCount(node.Info)) {
		node.State = NodeBusy
	} else {
		node.State = NodeReady
	}
	updateNodeCapacity(node)
}

func (c *Controller) chooseNode(req ResourceRequirements) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.chooseNodeLocked(req)
}

func (c *Controller) chooseNodeLocked(req ResourceRequirements) string {

	var bestID string
	var bestCPUSlack uint32
	var bestRAMSlack uint64
	var bestVRAMSlack uint64
	requiresGPU := requiredGPUCount(req) > 0

	for id, node := range c.nodes {
		s := c.sessions[id]
		if (node.State != NodeReady && node.State != NodeBusy) || s == nil {
			continue
		}
		if !c.canFit(node, req) {
			continue
		}

		cpuSlack := node.Info.CPUCores - node.AllocatedCPUCores - requiredCPUCores(req)
		ramSlack := node.Info.RAMGB - node.AllocatedRAMGB - requiredRAMGB(req)
		vramSlack := schedulableVRAMGB(node) - req.VRAMGB
		// Best-fit placement keeps larger workers available for larger jobs.
		// Node ID is the final tie-breaker so map iteration order cannot affect
		// placement.
		betterGPUFit := requiresGPU && (bestID == "" || vramSlack < bestVRAMSlack)
		betterGeneralFit := !requiresGPU && (bestID == "" || cpuSlack < bestCPUSlack ||
			(cpuSlack == bestCPUSlack && (ramSlack < bestRAMSlack ||
				(ramSlack == bestRAMSlack && id < bestID))))
		tieBreak := requiresGPU && bestID != "" && vramSlack == bestVRAMSlack &&
			(cpuSlack < bestCPUSlack || (cpuSlack == bestCPUSlack && id < bestID))
		if betterGPUFit || betterGeneralFit || tieBreak {
			bestCPUSlack = cpuSlack
			bestRAMSlack = ramSlack
			bestVRAMSlack = vramSlack
			bestID = id
		}
	}

	return bestID
}

func (c *Controller) hasKnownCapacity(req ResourceRequirements) (seen bool, capable bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hasKnownCapacityLocked(req)
}

func (c *Controller) hasKnownCapacityLocked(req ResourceRequirements) (seen bool, capable bool) {

	for _, node := range c.nodes {
		seen = true
		if c.canRun(node.Info, req) {
			return true, true
		}
	}
	return seen, false
}

func (c *Controller) hasKnownTaskCapacityLocked(job *Job) (seen bool, capable bool) {
	for _, node := range c.nodes {
		seen = true
		if c.canRunGeneral(node, job.Task) {
			return true, true
		}
	}
	return seen, false
}

func (c *Controller) addAssignedJobLocked(nodeID, jobID string) {
	node := c.nodes[nodeID]
	if node == nil {
		return
	}
	for _, assignedID := range node.AssignedJobs {
		if assignedID == jobID {
			return
		}
	}
	node.AssignedJobs = append(node.AssignedJobs, jobID)
}

func (c *Controller) allocateResourcesLocked(nodeID string, req ResourceRequirements) {
	node := c.nodes[nodeID]
	if node == nil {
		return
	}
	node.AllocatedCPUCores += requiredCPUCores(req)
	node.AllocatedRAMGB += requiredRAMGB(req)
	node.AllocatedGPUCount += requiredGPUCount(req)
	node.AllocatedVRAMGB += req.VRAMGB
	updateNodeCapacity(node)
}

func (c *Controller) releaseResourcesLocked(nodeID string, req ResourceRequirements) {
	node := c.nodes[nodeID]
	if node == nil {
		return
	}
	cpu := requiredCPUCores(req)
	ram := requiredRAMGB(req)
	if node.AllocatedCPUCores < cpu {
		node.AllocatedCPUCores = 0
	} else {
		node.AllocatedCPUCores -= cpu
	}
	if node.AllocatedRAMGB < ram {
		node.AllocatedRAMGB = 0
	} else {
		node.AllocatedRAMGB -= ram
	}
	gpu := requiredGPUCount(req)
	if node.AllocatedGPUCount < gpu {
		node.AllocatedGPUCount = 0
	} else {
		node.AllocatedGPUCount -= gpu
	}
	if node.AllocatedVRAMGB < req.VRAMGB {
		node.AllocatedVRAMGB = 0
	} else {
		node.AllocatedVRAMGB -= req.VRAMGB
	}
	updateNodeCapacity(node)
}

func (c *Controller) removeAssignedJobLocked(nodeID, jobID string) {
	node := c.nodes[nodeID]
	if node == nil {
		return
	}
	for i, assignedID := range node.AssignedJobs {
		if assignedID == jobID {
			node.AssignedJobs = append(node.AssignedJobs[:i], node.AssignedJobs[i+1:]...)
			return
		}
	}
}

func (c *Controller) reserveTask(jobID string, taskID uint64, nodeID string) (*session, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	job := c.jobs[jobID]
	node := c.nodes[nodeID]
	sess := c.sessions[nodeID]
	if job == nil || node == nil || sess == nil ||
		(node.State != NodeReady && node.State != NodeBusy) ||
		job.Status != JobQueued || !c.canFit(node, job.Requirements) {
		return nil, false
	}

	if err := transitionJobLocked(job, JobRunning); err != nil {
		return nil, false
	}
	job.NodeID = nodeID
	c.taskToJob[taskID] = jobID
	c.taskToNode[taskID] = nodeID
	c.addAssignedJobLocked(nodeID, jobID)
	c.allocateResourcesLocked(nodeID, job.Requirements)
	c.updateNodeStateLocked(node)
	return sess, true
}

func (c *Controller) rollbackTask(taskID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	jobID := c.taskToJob[taskID]
	nodeID := c.taskToNode[taskID]
	if job := c.jobs[jobID]; job != nil && job.Status == JobRunning && job.NodeID == nodeID {
		c.releaseResourcesLocked(nodeID, job.Requirements)
		_ = transitionJobLocked(job, JobQueued)
		job.NodeID = ""
	}
	delete(c.taskToJob, taskID)
	delete(c.taskToNode, taskID)
	delete(c.taskToPartition, taskID)
	c.removeAssignedJobLocked(nodeID, jobID)
	if node := c.nodes[nodeID]; node != nil {
		c.updateNodeStateLocked(node)
	}
}

func (c *Controller) scheduleLoop(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-c.scheduleNotify:
			c.scheduleOnce()
		case <-ticker.C:
			c.scheduleOnce()
		case <-ctx.Done():
			return
		}
	}
}

type queuedJob struct {
	id       string
	priority uint8
}

func (c *Controller) eligibleWorkerCountLocked(job *Job) int {
	count := 0
	for id, node := range c.nodes {
		if c.sessions[id] == nil || (node.State != NodeReady && node.State != NodeBusy) || !c.canRun(node.Info, job.Requirements) {
			continue
		}
		if job.Distribution.Mode == DistributionManual && job.Distribution.ManualAllocations[id] == 0 {
			continue
		}
		count++
	}
	return count
}

func (c *Controller) validateManualDistributionLocked(job *Job) (string, string) {
	if job.Distribution.Mode != DistributionManual || len(c.nodes) == 0 {
		return "", ""
	}
	for workerID := range job.Distribution.ManualAllocations {
		node := c.nodes[workerID]
		if node == nil {
			return "manual_distribution", fmt.Sprintf("manual allocation names unknown worker %s", workerID)
		}
		if job.Task != nil && job.Task.Type != TaskTypeNativeWorkload {
			if !c.canRunGeneral(node, job.Task) {
				return "resource_requirements", fmt.Sprintf("manual allocation worker %s cannot satisfy the task constraints", workerID)
			}
		} else if !c.canRun(node.Info, job.Requirements) {
			return "resource_requirements", fmt.Sprintf("manual allocation worker %s cannot satisfy the workload requirements", workerID)
		}
	}
	return "", ""
}

func (c *Controller) initializeJobLocked(job *Job, payload []byte) (bool, error) {
	if len(job.Partitions) > 0 {
		return true, nil
	}
	if job.Task != nil && job.Task.Type != TaskTypeNativeWorkload {
		if err := validateGeneralTask(*job.Task); err != nil {
			return false, err
		}
		if seen, capable := c.hasKnownTaskCapacityLocked(job); seen && !capable {
			return false, nil
		}
		now := time.Now()
		job.Distribution.Partitionable = false
		job.Distribution.TotalUnits = 1
		job.Distribution.TotalPartitions = 1
		job.Partitions = []Partition{{
			ID: fmt.Sprintf("%s-PART-0001", job.ID), Index: 0, Units: 1,
			State: PartitionQueued, CreatedAt: now, UpdatedAt: now,
		}}
		return true, nil
	}
	definition, knownWorkload := workloadDefinitionFor(job.Command)
	partitionable := knownWorkload
	if job.partitionableOverride != nil {
		if *job.partitionableOverride && !knownWorkload {
			return false, unsupportedWorkloadDefinition(job.Command)
		}
		partitionable = *job.partitionableOverride
	}
	var parts []workloadPartition
	var units uint64
	var err error
	if partitionable {
		workers := c.eligibleWorkerCountLocked(job)
		if workers == 0 {
			return false, nil
		}
		units, err = definition.unitCount(payload)
		if err != nil {
			return false, err
		}
		capacity := 0.0
		for id, node := range c.nodes {
			if c.sessions[id] != nil && (node.State == NodeReady || node.State == NodeBusy) && c.canRun(node.Info, job.Requirements) {
				if job.Distribution.Mode != DistributionManual || job.Distribution.ManualAllocations[id] > 0 {
					capacity += math.Max(0.25, effectiveCapacity(node)/math.Max(1, capacityScore(node.Info)))
				}
			}
		}
		if capacity < float64(workers) {
			capacity = float64(workers)
		}
		parts, err = definition.partition(payload, partitionSizeForCapacity(units, capacity))
		if err != nil {
			return false, err
		}
	} else {
		units = 1
		parts = []workloadPartition{{Payload: append([]byte(nil), payload...), Units: units}}
	}
	if len(parts) == 0 {
		return false, errors.New("workload produced no partitions")
	}
	now := time.Now()
	job.Distribution.Partitionable = partitionable
	job.Distribution.TotalUnits = units
	job.Distribution.TotalPartitions = len(parts)
	job.Partitions = make([]Partition, len(parts))
	for index, part := range parts {
		job.Partitions[index] = Partition{
			ID:        fmt.Sprintf("%s-PART-%04d", job.ID, index+1),
			Index:     index,
			Units:     part.Units,
			State:     PartitionQueued,
			CreatedAt: now,
			UpdatedAt: now,
			payload:   part.Payload,
		}
	}
	return true, nil
}

func (c *Controller) choosePartitionNodeLocked(job *Job) string {
	if job.Distribution.Mode == DistributionAutomatic && job.Distribution.TotalPartitions == 1 {
		return c.chooseNodeLocked(job.Requirements)
	}
	assignedUnits := make(map[string]uint64)
	for _, partition := range job.Partitions {
		if partition.NodeID != "" && (partition.State == PartitionAssigned || partition.State == PartitionRunning || (job.Distribution.Mode == DistributionManual && partition.State == PartitionCompleted)) {
			assignedUnits[partition.NodeID] += partition.Units
		}
	}

	var bestID string
	var bestRatio float64
	var bestWeight float64
	for id, node := range c.nodes {
		if c.sessions[id] == nil || (node.State != NodeReady && node.State != NodeBusy) || !c.canFitJob(node, job) {
			continue
		}
		weight := effectiveCapacity(node)
		if job.Distribution.Mode == DistributionManual {
			allocation := job.Distribution.ManualAllocations[id]
			if allocation == 0 {
				continue
			}
			weight = float64(allocation)
		}
		if weight <= 0 {
			continue
		}
		ratio := float64(assignedUnits[id]) / weight
		if bestID == "" || ratio < bestRatio ||
			(ratio == bestRatio && (weight > bestWeight || (weight == bestWeight && id < bestID))) {
			bestID = id
			bestRatio = ratio
			bestWeight = weight
		}
	}
	return bestID
}

func (c *Controller) reservePartitionLocked(job *Job, partition *Partition, taskID uint64, nodeID string) (*session, bool) {
	node := c.nodes[nodeID]
	sess := c.sessions[nodeID]
	if job == nil || partition == nil || node == nil || sess == nil ||
		(node.State != NodeReady && node.State != NodeBusy) ||
		(partition.State != PartitionQueued && partition.State != PartitionRequeued) ||
		!c.canFitJob(node, job) {
		return nil, false
	}
	weight := effectiveCapacity(node)
	if job.Distribution.Mode == DistributionManual {
		weight = float64(job.Distribution.ManualAllocations[nodeID])
	}
	telemetry := node.Telemetry
	partition.AssignmentReason = fmt.Sprintf("%s scheduling: effective_capacity=%.2f weight=%.2f performance_factor=%.2f cpu=%.1f%% active_tasks=%d", job.Distribution.Mode, effectiveCapacity(node), weight, maxPerformanceFactor(node), telemetry.CPUUtilizationPercent, telemetry.ActiveTasks)
	if err := transitionPartition(partition, PartitionAssigned); err != nil {
		return nil, false
	}
	partition.TaskID = taskID
	partition.NodeID = nodeID
	partition.Attempt++
	if err := transitionJobLocked(job, JobRunning); err != nil {
		return nil, false
	}
	c.taskToJob[taskID] = job.ID
	c.taskToNode[taskID] = nodeID
	c.taskToPartition[taskID] = partition.ID
	c.addAssignedJobLocked(nodeID, job.ID)
	c.allocateResourcesLocked(nodeID, job.Requirements)
	c.updateNodeStateLocked(node)
	c.updateJobPlacementLocked(job)
	c.publishEvent(controllerEvent{Type: "partition.assigned", Resource: "partition", ID: partition.ID, Status: string(partition.State)})
	return sess, true
}

func maxPerformanceFactor(node *NodeRecord) float64 {
	if node == nil || node.PerformanceFactor <= 0 {
		return 1
	}
	return node.PerformanceFactor
}

func (c *Controller) rollbackPartitionLocked(job *Job, partition *Partition, taskID uint64) {
	nodeID := partition.NodeID
	c.releasePartitionLocked(job, partition)
	if err := transitionPartition(partition, PartitionQueued); err != nil {
		return
	}
	partition.TaskID = 0
	partition.NodeID = ""
	delete(c.taskToJob, taskID)
	delete(c.taskToNode, taskID)
	delete(c.taskToPartition, taskID)
	c.updateJobProgressLocked(job)
	if node := c.nodes[nodeID]; node != nil {
		c.updateNodeStateLocked(node)
	}
}

func hasSchedulableWork(job *Job) bool {
	if job == nil {
		return false
	}
	if job.Status == JobQueued {
		return true
	}
	if job.Status == JobRunning {
		for _, partition := range job.Partitions {
			if partition.State == PartitionQueued || partition.State == PartitionRequeued {
				return true
			}
		}
	}
	return false
}

func (c *Controller) scheduleOnce() {
	c.mu.RLock()
	queued := make([]queuedJob, 0)
	for _, job := range c.jobs {
		if hasSchedulableWork(job) {
			queued = append(queued, queuedJob{id: job.ID, priority: job.Priority})
		}
	}
	c.mu.RUnlock()
	sort.Slice(queued, func(i, j int) bool {
		if queued[i].priority != queued[j].priority {
			return queued[i].priority > queued[j].priority
		}
		return queued[i].id < queued[j].id
	})

	for _, candidate := range queued {
		c.mu.Lock()
		job := c.jobs[candidate.id]
		if job == nil || !hasSchedulableWork(job) {
			c.mu.Unlock()
			continue
		}
		if errorCode, reason := c.validateManualDistributionLocked(job); errorCode != "" {
			c.failJobLocked(job, errorCode, reason, "")
			c.mu.Unlock()
			continue
		}
		seen, capable := c.hasKnownCapacityLocked(job.Requirements)
		if job.Task != nil && job.Task.Type != TaskTypeNativeWorkload {
			seen, capable = c.hasKnownTaskCapacityLocked(job)
		}
		if seen && !capable {
			c.failJobLocked(job, "resource_requirements", "no known worker satisfies the workload resource requirements", "")
			c.mu.Unlock()
			continue
		}
		payload, err := base64.StdEncoding.DecodeString(job.PayloadB64)
		if err != nil {
			c.failJobLocked(job, "invalid_payload", "invalid payload base64: "+err.Error(), "")
			c.mu.Unlock()
			continue
		}
		initialized, err := c.initializeJobLocked(job, payload)
		if err != nil {
			c.failJobLocked(job, "malformed_payload", err.Error(), "")
			c.mu.Unlock()
			continue
		}
		if !initialized {
			c.mu.Unlock()
			continue
		}

		for {
			var partition *Partition
			for index := range job.Partitions {
				if job.Partitions[index].State == PartitionQueued || job.Partitions[index].State == PartitionRequeued {
					partition = &job.Partitions[index]
					break
				}
			}
			if partition == nil {
				break
			}
			nodeID := c.choosePartitionNodeLocked(job)
			if nodeID == "" {
				seen, capable := c.hasKnownCapacityLocked(job.Requirements)
				if job.Task != nil && job.Task.Type != TaskTypeNativeWorkload {
					seen, capable = c.hasKnownTaskCapacityLocked(job)
				}
				if seen && !capable {
					c.failJobLocked(job, "resource_requirements", "no known worker satisfies the workload resource requirements", "")
				}
				break
			}
			taskID := atomic.AddUint64(&c.nextTask, 1)
			task := Task{
				ID:           taskID,
				JobID:        job.ID,
				Command:      job.Command,
				Priority:     job.Priority,
				Requirements: job.Requirements,
				Payload:      partition.payload,
			}
			sess, reserved := c.reservePartitionLocked(job, partition, taskID, nodeID)
			if !reserved {
				continue
			}
			var sendErr error
			if job.Task != nil && job.Task.Type != TaskTypeNativeWorkload {
				general := GeneralTaskEnvelope{TaskID: taskID, JobID: job.ID, Attempt: partition.Attempt, Spec: *job.Task}
				data, encodeErr := encodeGeneralTask(general)
				if encodeErr != nil {
					sendErr = encodeErr
				} else {
					sendErr = c.enqueueInputArtifactsLocked(sess, taskID, job.Task.InputArtifacts)
					if sendErr == nil {
						sendErr = sess.enqueue(MsgTaskSubmit, taskID, data)
					}
				}
			} else {
				data, encodeErr := encodeTaskBatch([]Task{task})
				if encodeErr != nil {
					sendErr = encodeErr
				} else {
					sendErr = sess.enqueue(MsgTaskBatch, taskID, data)
				}
			}
			if sendErr != nil {
				c.rollbackPartitionLocked(job, partition, taskID)
				continue
			}
		}
		c.updateJobProgressLocked(job)
		c.persistLocked()
		c.mu.Unlock()
	}
}

func (c *Controller) enqueueInputArtifactsLocked(sess *session, taskID uint64, artifacts []TaskArtifact) error {
	for _, requested := range artifacts {
		record := c.artifacts[requested.ID]
		if record == nil {
			return fmt.Errorf("input artifact %s was not found", requested.ID)
		}
		file, err := os.Open(c.artifactPath(record.ID))
		if err != nil {
			return fmt.Errorf("open input artifact %s: %w", record.ID, err)
		}
		begin, err := encodeArtifactBegin(taskID, TaskArtifact{ID: record.ID, Name: requested.Name, Size: record.Size, SHA256: record.SHA256, Kind: record.Kind})
		if err == nil {
			err = sess.enqueueBlocking(MsgArtifactBegin, taskID, begin)
		}
		if err != nil {
			file.Close()
			return err
		}
		buffer := make([]byte, 64<<10)
		var offset uint64
		for {
			count, readErr := file.Read(buffer)
			if count > 0 {
				chunk, encodeErr := encodeArtifactChunk(taskID, record.ID, offset, buffer[:count])
				if encodeErr == nil {
					encodeErr = sess.enqueueBlocking(MsgArtifactChunk, taskID, chunk)
				}
				if encodeErr != nil {
					file.Close()
					return encodeErr
				}
				offset += uint64(count)
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				file.Close()
				return readErr
			}
		}
		file.Close()
		end, err := encodeArtifactEnd(taskID, record.ID)
		if err != nil {
			return err
		}
		if err := sess.enqueueBlocking(MsgArtifactEnd, taskID, end); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) failJobLocked(job *Job, errorCode, reason, nodeID string) {
	if job == nil || isTerminalJob(job.Status) {
		return
	}
	for index := range job.Partitions {
		partition := &job.Partitions[index]
		if partition.State == PartitionAssigned || partition.State == PartitionRunning {
			if partition.TaskID != 0 {
				delete(c.taskToJob, partition.TaskID)
				delete(c.taskToNode, partition.TaskID)
				delete(c.taskToPartition, partition.TaskID)
			}
			c.releasePartitionLocked(job, partition)
		}
		if partition.State != PartitionCompleted {
			if transitionPartition(partition, PartitionFailed) != nil {
				continue
			}
			partition.TaskID = 0
			partition.NodeID = ""
		}
	}
	if err := transitionJobLocked(job, JobFailed); err != nil {
		return
	}
	job.NodeID = nodeID
	job.Result = &TaskResult{JobID: job.ID, Status: "FAILED", ErrorCode: errorCode, Error: reason, NodeID: nodeID}
	c.updateJobProgressLocked(job)
	c.updateJobPlacementLocked(job)
	c.publishEvent(controllerEvent{Type: "job.failed", Resource: "job", ID: job.ID, Status: string(JobFailed)})
}

func (c *Controller) cancelJobLocked(job *Job, reason string) error {
	if job == nil {
		return errors.New("job not found")
	}
	if isTerminalJob(job.Status) {
		return fmt.Errorf("job is already %s", job.Status)
	}
	var cancellations []struct {
		sess   *session
		taskID uint64
	}
	for index := range job.Partitions {
		partition := &job.Partitions[index]
		if partition.TaskID != 0 {
			if job.Task != nil && job.Task.Type != TaskTypeNativeWorkload {
				if sess := c.sessions[partition.NodeID]; sess != nil {
					cancellations = append(cancellations, struct {
						sess   *session
						taskID uint64
					}{sess: sess, taskID: partition.TaskID})
				}
			}
			delete(c.taskToJob, partition.TaskID)
			delete(c.taskToNode, partition.TaskID)
			delete(c.taskToPartition, partition.TaskID)
		}
		if partition.State == PartitionAssigned || partition.State == PartitionRunning {
			c.releasePartitionLocked(job, partition)
		}
		if partition.State != PartitionCompleted {
			if err := transitionPartition(partition, PartitionCancelled); err != nil {
				return err
			}
			partition.TaskID = 0
			partition.NodeID = ""
		}
	}
	if err := transitionJobLocked(job, JobCancelled); err != nil {
		return err
	}
	job.NodeID = ""
	job.Result = &TaskResult{JobID: job.ID, Status: "CANCELLED", ErrorCode: "cancelled", Error: reason}
	for _, cancellation := range cancellations {
		_ = cancellation.sess.enqueue(MsgTaskCancel, cancellation.taskID, encodeTaskCancel(cancellation.taskID))
	}
	c.updateJobPlacementLocked(job)
	c.publishEvent(controllerEvent{Type: "job.cancelled", Resource: "job", ID: job.ID, Status: string(JobCancelled)})
	return nil
}

func (c *Controller) pauseJobLocked(job *Job) error {
	if job == nil {
		return errors.New("job not found")
	}
	if job.Status != JobQueued {
		if job.Status == JobRunning {
			return errors.New("running jobs cannot be paused; worker protocol task suspension is unsupported")
		}
		return fmt.Errorf("job is %s", job.Status)
	}
	if err := transitionJobLocked(job, JobPaused); err != nil {
		return err
	}
	c.publishEvent(controllerEvent{Type: "job.paused", Resource: "job", ID: job.ID, Status: string(JobPaused)})
	return nil
}

func (c *Controller) resumeJobLocked(job *Job) error {
	if job == nil {
		return errors.New("job not found")
	}
	if job.Status != JobPaused {
		return fmt.Errorf("job is %s", job.Status)
	}
	if err := transitionJobLocked(job, JobQueued); err != nil {
		return err
	}
	c.publishEvent(controllerEvent{Type: "job.resumed", Resource: "job", ID: job.ID, Status: string(JobQueued)})
	return nil
}

func (c *Controller) retryJobLocked(job *Job) error {
	if job == nil {
		return errors.New("job not found")
	}
	if job.Status == JobCompleted {
		return errors.New("completed jobs cannot be retried")
	}
	if !isTerminalJob(job.Status) {
		return fmt.Errorf("job is %s", job.Status)
	}
	// Retry is an explicit operator action that creates a fresh attempt. It is
	// intentionally separate from ordinary lifecycle transitions so terminal
	// results cannot be moved back into execution by stale worker messages.
	job.Status = JobQueued
	job.UpdatedAt = time.Now()
	job.Partitions = nil
	job.NodeID = ""
	job.NodeIDs = nil
	job.Result = nil
	job.Execution = nil
	job.Distribution.CompletedPartitions = 0
	job.Distribution.RunningPartitions = 0
	job.Distribution.PendingPartitions = 0
	job.Distribution.FailedPartitions = 0
	job.Distribution.RequeuedPartitions = 0
	job.Distribution.CompletedUnits = 0
	job.Distribution.ProgressPercent = 0
	c.publishEvent(controllerEvent{Type: "task.retry_requested", Resource: "task", ID: job.ID, Status: string(JobQueued)})
	return nil
}

func validateDistribution(mode DistributionMode, allocations map[string]uint8) (map[string]uint8, error) {
	if mode == "" {
		mode = DistributionAutomatic
	}
	if mode != DistributionAutomatic && mode != DistributionManual {
		return nil, errors.New("distribution mode must be automatic or manual")
	}
	clean := make(map[string]uint8)
	total := 0
	for workerID, allocation := range allocations {
		if strings.TrimSpace(workerID) == "" || allocation == 0 {
			continue
		}
		clean[workerID] = allocation
		total += int(allocation)
	}
	if mode == DistributionManual && total != 100 {
		return nil, errors.New("manual worker allocations must total exactly 100 percent")
	}
	if mode == DistributionAutomatic {
		return nil, nil
	}
	return clean, nil
}

func (c *Controller) updateJobDistribution(id string, request distributionRequest) (*Job, error) {
	allocations, err := validateDistribution(request.Mode, request.ManualAllocations)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	job := c.jobs[id]
	if job == nil {
		return nil, fmt.Errorf("unknown job: %s", id)
	}
	if job.Status != JobQueued {
		return nil, fmt.Errorf("job distribution can only be changed while QUEUED; job is %s", job.Status)
	}
	if request.Mode == DistributionManual {
		for workerID := range allocations {
			if c.nodes[workerID] == nil {
				return nil, fmt.Errorf("manual allocation names unknown worker %s", workerID)
			}
		}
	}
	mode := request.Mode
	if mode == "" {
		mode = DistributionAutomatic
	}
	job.Distribution.Mode = mode
	job.Distribution.ManualAllocations = allocations
	job.UpdatedAt = time.Now()
	c.triggerSchedule()
	c.publishEvent(controllerEvent{Type: "job.distribution_changed", Resource: "job", ID: job.ID, Status: string(job.Status)})
	copy := cloneJob(*job)
	return &copy, nil
}

func (c *Controller) controlWorker(id, action string) (*NodeRecord, error) {
	var closeSession *session
	c.mu.Lock()
	node := c.nodes[id]
	if node == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("unknown worker: %s", id)
	}
	switch action {
	case "pause":
		if node.State == NodeLost || node.State == NodeOffline {
			c.mu.Unlock()
			return nil, fmt.Errorf("worker %s is offline", id)
		}
		node.State = NodePaused
	case "resume":
		if c.sessions[id] == nil || node.State == NodeLost || node.State == NodeOffline {
			c.mu.Unlock()
			return nil, fmt.Errorf("worker %s is offline", id)
		}
		node.State = NodeReady
		c.updateNodeStateLocked(node)
	case "remove":
		if s := c.sessions[id]; s != nil {
			delete(c.sessions, id)
			closeSession = s
		}
		node.State = NodeOffline
		c.requeueNodeJobsLocked(id)
	default:
		c.mu.Unlock()
		return nil, fmt.Errorf("unsupported worker action: %s", action)
	}
	updateNodeCapacity(node)
	c.persistLocked()
	copy := *node
	copy.AssignedJobs = append([]string(nil), node.AssignedJobs...)
	c.mu.Unlock()
	if closeSession != nil {
		closeSession.close()
	}
	if action == "resume" || action == "remove" {
		c.triggerSchedule()
	}
	c.publishEvent(controllerEvent{Type: "worker.state_changed", Resource: "worker", ID: id, Status: string(copy.State)})
	return &copy, nil
}

func (c *Controller) finalizeJobLocked(job *Job) {
	if !job.Distribution.Partitionable {
		if len(job.Partitions) == 1 && job.Partitions[0].Result != nil {
			resultCopy := *job.Partitions[0].Result
			job.Result = &resultCopy
			if job.Result.NodeID == "" && len(job.NodeIDs) == 1 {
				job.Result.NodeID = job.NodeIDs[0]
			}
			job.UpdatedAt = time.Now()
		}
		return
	}
	definition, partitionable := workloadDefinitionFor(job.Command)
	if !partitionable {
		return
	}
	values := make([]int64, len(job.Partitions))
	var duration uint64
	for index, partition := range job.Partitions {
		if partition.Result == nil || partition.Result.Status != "COMPLETED" {
			return
		}
		values[index] = partition.Result.Value
		duration += partition.Result.DurationUS
	}
	value, err := definition.merge(values)
	if err != nil {
		c.failJobLocked(job, "reduction_failed", err.Error(), "")
		return
	}
	job.Result = &TaskResult{JobID: job.ID, Status: "COMPLETED", Value: value, DurationUS: duration}
	if len(job.NodeIDs) == 1 {
		job.Result.NodeID = job.NodeIDs[0]
	}
	job.UpdatedAt = time.Now()
}

func (c *Controller) applyPartitionResultLocked(job *Job, partition *Partition, result TaskResult) {
	partition.Result = &result
	if partition.StartedAt == nil {
		partition.StartedAt = partition.AssignedAt
	}
	if result.DurationUS > 0 {
		partition.ExecutionDurationUS = result.DurationUS
	}
	partition.TaskID = 0
	previousState := partition.State
	if result.Status == "COMPLETED" {
		if transitionPartition(partition, PartitionCompleted) != nil {
			return
		}
	} else {
		if transitionPartition(partition, PartitionFailed) != nil {
			return
		}
	}
	if previousState != partition.State && partition.State == PartitionCompleted {
		c.publishEvent(controllerEvent{Type: "partition.completed", Resource: "partition", ID: partition.ID, Status: string(partition.State)})
	}
	nodeID := partition.NodeID
	c.recordNodeResultLocked(nodeID, partition.Units, result)
	c.releasePartitionLocked(job, partition)
	delete(c.taskToJob, result.TaskID)
	delete(c.taskToNode, result.TaskID)
	delete(c.taskToPartition, result.TaskID)
	if partition.State == PartitionFailed {
		c.failJobLocked(job, result.ErrorCode, result.Error, nodeID)
		return
	}
	c.updateJobProgressLocked(job)
	if job.Status == JobCompleted {
		c.finalizeJobLocked(job)
	}
}

func (c *Controller) applyResults(results []TaskResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, result := range results {
		jobID := c.taskToJob[result.TaskID]
		if jobID == "" {
			// Unknown task IDs are stale or unsolicited results. Ignoring them
			// prevents an old connection from completing a requeued job.
			continue
		}
		job := c.jobs[jobID]
		if job == nil {
			continue
		}
		if result.JobID != "" && result.JobID != job.ID {
			continue
		}
		if mappedNodeID := c.taskToNode[result.TaskID]; mappedNodeID != "" &&
			result.NodeID != "" && result.NodeID != mappedNodeID {
			continue
		}
		if partitionID := c.taskToPartition[result.TaskID]; partitionID != "" {
			partition := c.findPartitionLocked(job, partitionID)
			if partition == nil || partition.TaskID != result.TaskID ||
				(partition.State != PartitionAssigned && partition.State != PartitionRunning) {
				continue
			}
			c.applyPartitionResultLocked(job, partition, result)
			continue
		}

		// Compatibility path for the pre-partition single-task state used by
		// existing in-process scheduler tests.
		if result.Status == "COMPLETED" {
			_ = transitionJobLocked(job, JobCompleted)
		} else {
			_ = transitionJobLocked(job, JobFailed)
		}
		resultCopy := result
		job.Result = &resultCopy
		nodeID := c.taskToNode[result.TaskID]
		if nodeID == "" {
			nodeID = result.NodeID
		}
		c.recordNodeResultLocked(nodeID, 1, result)
		job.NodeID = nodeID
		job.UpdatedAt = time.Now()
		c.removeAssignedJobLocked(nodeID, job.ID)
		c.releaseResourcesLocked(nodeID, job.Requirements)
		if n := c.nodes[nodeID]; n != nil {
			c.updateNodeStateLocked(n)
		}
		delete(c.taskToJob, result.TaskID)
		delete(c.taskToNode, result.TaskID)
	}
	c.persistLocked()
	c.triggerSchedule()
}

func (c *Controller) applyGeneralTaskResult(result GeneralTaskResultEnvelope) {
	c.mu.Lock()
	defer c.mu.Unlock()
	jobID := c.taskToJob[result.TaskID]
	if jobID == "" {
		return
	}
	job := c.jobs[jobID]
	partitionID := c.taskToPartition[result.TaskID]
	if job == nil || partitionID == "" || (result.JobID != "" && result.JobID != job.ID) {
		return
	}
	partition := c.findPartitionLocked(job, partitionID)
	if partition == nil || partition.TaskID != result.TaskID || partition.Attempt != result.Attempt || (partition.State != PartitionAssigned && partition.State != PartitionRunning) {
		return
	}
	nodeID := partition.NodeID
	c.recordNodeResultLocked(nodeID, partition.Units, TaskResult{TaskID: result.TaskID, JobID: job.ID, Status: result.Status, DurationUS: result.DurationUS, NodeID: nodeID})
	c.releasePartitionLocked(job, partition)
	delete(c.taskToJob, result.TaskID)
	delete(c.taskToNode, result.TaskID)
	delete(c.taskToPartition, result.TaskID)
	if result.Status != "COMPLETED" {
		if job.Task != nil && partition.Attempt <= job.Task.Retry.MaxRetries {
			_ = transitionPartition(partition, PartitionRequeued)
			partition.TaskID = 0
			partition.NodeID = ""
			partition.Attempt++
			_ = transitionJobLocked(job, JobQueued)
			job.NodeID = ""
			job.Execution = &GeneralTaskResult{Status: "REQUEUED", Attempt: result.Attempt, ErrorCode: result.ErrorCode, Error: result.Error}
			c.updateJobPlacementLocked(job)
			c.publishEvent(controllerEvent{Type: "task.requeued", Resource: "task", ID: job.ID, Status: string(JobQueued)})
			c.persistLocked()
			c.triggerSchedule()
			return
		}
	}
	job.Execution = &GeneralTaskResult{
		Status: result.Status, ExitCode: result.ExitCode,
		StdoutB64:       base64.StdEncoding.EncodeToString(result.Stdout),
		StderrB64:       base64.StdEncoding.EncodeToString(result.Stderr),
		StdoutTruncated: result.StdoutTruncated, StderrTruncated: result.StderrTruncated,
		DurationUS: result.DurationUS, ErrorCode: result.ErrorCode, Error: result.Error, Attempt: result.Attempt,
		OutputArtifacts: append([]TaskArtifact(nil), c.taskArtifacts[result.TaskID]...),
	}
	if result.Status == "COMPLETED" {
		_ = transitionPartition(partition, PartitionCompleted)
	} else if result.Status == "TIMED_OUT" {
		_ = transitionPartition(partition, PartitionFailed)
		_ = transitionJobLocked(job, JobTimedOut)
	} else if result.Status == "CANCELLED" {
		_ = transitionPartition(partition, PartitionCancelled)
		_ = transitionJobLocked(job, JobCancelled)
	} else {
		_ = transitionPartition(partition, PartitionFailed)
		_ = transitionJobLocked(job, JobFailed)
	}
	partition.Result = &TaskResult{TaskID: result.TaskID, JobID: job.ID, Status: result.Status, ErrorCode: result.ErrorCode, Error: result.Error, DurationUS: result.DurationUS, NodeID: nodeID}
	job.Result = partition.Result
	job.NodeID = nodeID
	job.UpdatedAt = time.Now()
	if result.Status == "COMPLETED" {
		c.updateJobProgressLocked(job)
	}
	c.updateJobPlacementLocked(job)
	c.publishEvent(controllerEvent{Type: "task.completed", Resource: "task", ID: job.ID, Status: string(job.Status), Progress: job.Distribution.ProgressPercent})
	c.persistLocked()
	c.triggerSchedule()
}

func (c *Controller) applyTaskOutput(nodeID string, output TaskOutputChunk) {
	if len(output.Data) == 0 && !output.Final {
		return
	}
	c.mu.RLock()
	jobID := c.taskToJob[output.TaskID]
	job := c.jobs[jobID]
	valid := job != nil && c.taskToNode[output.TaskID] == nodeID
	status := ""
	if valid {
		status = string(job.Status)
	}
	c.mu.RUnlock()
	if !valid {
		return
	}
	c.publishEvent(controllerEvent{
		Type:      "task.output",
		Resource:  "task",
		ID:        jobID,
		Status:    status,
		Stream:    output.Stream,
		OutputB64: base64.StdEncoding.EncodeToString(output.Data),
		Final:     output.Final,
	})
}

func (c *Controller) setJobFailed(id, errorCode, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if job := c.jobs[id]; job != nil {
		c.failJobLocked(job, errorCode, reason, "")
	}
}

func (c *Controller) healthLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.markStaleWorkers(time.Now())
		case <-ctx.Done():
			return
		}
	}
}

func (c *Controller) markStaleWorkers(now time.Time) []string {
	var stale []*session
	var lostIDs []string
	c.mu.Lock()
	for nodeID, n := range c.nodes {
		if now.Sub(n.LastHeartbeat) > 15*time.Second {
			if n.State != NodeLost {
				log.Printf("worker marked offline id=%s reason=heartbeat timeout", nodeID)
				lostIDs = append(lostIDs, nodeID)
			}
			n.State = NodeLost
			if s := c.sessions[nodeID]; s != nil {
				delete(c.sessions, nodeID)
				c.requeueNodeJobsLocked(nodeID)
				stale = append(stale, s)
			}
		}
	}
	if len(lostIDs) > 0 {
		c.persistLocked()
	}
	c.mu.Unlock()
	for _, s := range stale {
		s.close()
	}
	for _, nodeID := range lostIDs {
		c.publishEvent(controllerEvent{Type: "worker.stale", Resource: "worker", ID: nodeID, Status: string(NodeLost)})
	}
	if len(lostIDs) > 0 {
		c.triggerSchedule()
	}
	return lostIDs
}

func (c *Controller) createJob(req jobRequest) (*Job, error) {
	if strings.TrimSpace(req.Command) == "" {
		return nil, errors.New("command is required")
	}
	if req.Priority > 100 {
		return nil, errors.New("priority must be 0..100")
	}
	mode := req.DistributionMode
	if mode == "" {
		mode = DistributionAutomatic
	}
	if mode != DistributionAutomatic && mode != DistributionManual {
		return nil, errors.New("distribution_mode must be automatic or manual")
	}
	manualAllocations, err := validateDistribution(mode, req.ManualAllocations)
	if err != nil {
		return nil, err
	}

	id := req.ID
	if id == "" {
		id = fmt.Sprintf("JOB-%06d", atomic.AddUint64(&c.nextJob, 1))
	}
	now := time.Now()
	job := &Job{
		ID: id, Command: req.Command, Priority: req.Priority,
		Requirements: req.Requirements, PayloadB64: req.PayloadB64,
		Distribution: DistributionInfo{
			Mode:              mode,
			ManualAllocations: manualAllocations,
		},
		Status:                JobQueued,
		CreatedAt:             now,
		UpdatedAt:             now,
		partitionableOverride: req.Partitionable,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.jobs[id]; exists {
		return nil, errors.New("job already exists")
	}
	c.jobs[id] = job
	c.persistLocked()
	c.triggerSchedule()
	c.publishEvent(controllerEvent{Type: "job.created", Resource: "job", ID: job.ID, Status: string(job.Status)})
	return job, nil
}

func (c *Controller) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", c.handleHealth)
	if c.shutdownToken != "" && c.shutdown != nil {
		mux.HandleFunc("/internal/shutdown", c.handleShutdown)
	}
	mux.HandleFunc("/v1/controller/info", c.handleControllerInfo)
	mux.HandleFunc("/v1/cluster/status", c.handleClusterStatus)
	mux.HandleFunc("/v1/events", c.handleEvents)
	mux.HandleFunc("/v1/nodes", c.handleNodes)
	mux.HandleFunc("/v1/nodes/", c.handleNode)
	mux.HandleFunc("/v1/jobs", c.handleJobs)
	mux.HandleFunc("/v1/jobs/active", c.handleActiveJobs)
	mux.HandleFunc("/v1/jobs/", c.handleJob)
	mux.HandleFunc("/v1/tasks", c.handleTasks)
	mux.HandleFunc("/v1/tasks/batch", c.handleTasks)
	mux.HandleFunc("/v1/tasks/", c.handleTask)
	mux.HandleFunc("/v1/ai/plans", c.handleAIPlans)
	mux.HandleFunc("/v1/ai/plans/", c.handleAIPlan)
	mux.HandleFunc("/v1/ai/executions", c.handleAIExecutions)
	mux.HandleFunc("/v1/ai/executions/", c.handleAIExecution)
	mux.HandleFunc("/v1/ai/workers", c.handleAIWorkers)
	mux.HandleFunc("/v1/ai/inspect", c.handleAIInspect)
	mux.HandleFunc("/v1/artifacts", c.handleArtifacts)
	mux.HandleFunc("/v1/artifacts/", c.handleArtifact)
	return mux
}

func (c *Controller) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	ip := net.ParseIP(host)
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if ip == nil || !ip.IsLoopback() || len(token) != len(c.shutdownToken) ||
		subtle.ConstantTimeCompare([]byte(token), []byte(c.shutdownToken)) != 1 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	go c.shutdown()
}

func (c *Controller) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		c.mu.RLock()
		tasks := make([]Job, 0)
		for _, job := range c.jobs {
			if job.Task != nil {
				tasks = append(tasks, cloneJob(*job))
			}
		}
		c.mu.RUnlock()
		writeJSON(w, http.StatusOK, tasks)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/v1/tasks/batch" {
		var request taskBatchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Tasks) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "tasks batch must contain at least one task"})
			return
		}
		if request.BatchID == "" {
			request.BatchID = fmt.Sprintf("BATCH-%06d", atomic.AddUint64(&c.nextJob, 1))
		}
		created := make([]Job, 0, len(request.Tasks))
		for _, task := range request.Tasks {
			task.BatchID = request.BatchID
			job, err := c.createGeneralTask(task)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			created = append(created, cloneJob(*job))
		}
		writeJSON(w, http.StatusCreated, map[string]any{"batch_id": request.BatchID, "tasks": created})
		return
	}
	var request taskRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	job, err := c.createGeneralTask(request)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, cloneJob(*job))
}

func (c *Controller) handleTask(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	c.mu.RLock()
	job := c.jobs[id]
	if job == nil || job.Task == nil {
		c.mu.RUnlock()
		http.NotFound(w, r)
		return
	}
	copy := cloneJob(*job)
	c.mu.RUnlock()
	if len(parts) == 1 && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, copy)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodGet && parts[1] == "logs" {
		writeJSON(w, http.StatusOK, map[string]any{"task_id": id, "execution_result": copy.Execution})
		return
	}
	if len(parts) == 2 && r.Method == http.MethodGet && parts[1] == "result" {
		if copy.Execution == nil {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, copy.Execution)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost || (parts[1] != "cancel" && parts[1] != "retry") {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	c.mu.Lock()
	job = c.jobs[id]
	var err error
	if parts[1] == "cancel" {
		err = c.cancelJobLocked(job, "cancelled by user")
	} else {
		err = c.retryJobLocked(job)
	}
	if err == nil {
		c.persistLocked()
	}
	if job != nil {
		copy = cloneJob(*job)
	}
	c.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	c.triggerSchedule()
	writeJSON(w, http.StatusOK, copy)
}

func (c *Controller) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	_, events, unsubscribe := c.subscribeEvents()
	defer unsubscribe()
	_, _ = w.Write([]byte("event: ready\ndata: {}\n\n"))
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			payload, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: state\ndata: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

func (c *Controller) handleControllerInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service":    "nodren-controller",
		"version":    nodrenVersion,
		"tcp_addr":   c.tcpAddr,
		"http_addr":  c.httpAddr,
		"workers":    c.nodeCount(),
		"started_at": c.startedAt,
	})
}

func (c *Controller) handleClusterStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	now := time.Now()
	c.mu.RLock()
	status := c.clusterStatusLocked(now)
	c.mu.RUnlock()
	writeJSON(w, http.StatusOK, status)
}

func (c *Controller) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	now := time.Now()
	c.mu.RLock()
	status := c.clusterStatusLocked(now)
	c.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "ok",
		"service":         status.Service,
		"version":         status.Version,
		"nodes":           status.Workers,
		"online_workers":  status.OnlineWorkers,
		"offline_workers": status.OfflineWorkers,
		"stale_workers":   status.StaleWorkers,
		"uptime_seconds":  status.UptimeSeconds,
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
		nodeCopy := *n
		nodeCopy.AssignedJobs = append([]string(nil), n.AssignedJobs...)
		updateNodeCapacity(&nodeCopy)
		nodes = append(nodes, nodeCopy)
	}
	c.mu.RUnlock()
	writeJSON(w, http.StatusOK, nodes)
}

func (c *Controller) handleNode(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/nodes/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "stats" && r.Method == http.MethodGet {
		c.mu.RLock()
		node := c.nodes[id]
		if node == nil {
			c.mu.RUnlock()
			http.NotFound(w, r)
			return
		}
		copy := *node
		copy.AssignedJobs = append([]string(nil), node.AssignedJobs...)
		updateNodeCapacity(&copy)
		current := make([]string, 0)
		for _, job := range c.jobs {
			for _, partition := range job.Partitions {
				if partition.NodeID == id && (partition.State == PartitionAssigned || partition.State == PartitionRunning) {
					current = append(current, partition.ID)
				}
			}
		}
		age := int64(0)
		if !copy.Telemetry.Timestamp.IsZero() {
			age = time.Since(copy.Telemetry.Timestamp).Milliseconds()
		}
		stats := workerStatsResponse{Node: copy, CurrentPartitions: current, TelemetryAgeMS: age, SchedulerWeight: effectiveCapacity(&copy)}
		c.mu.RUnlock()
		writeJSON(w, http.StatusOK, stats)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		c.mu.RLock()
		node := c.nodes[id]
		var copy NodeRecord
		if node != nil {
			copy = *node
			copy.AssignedJobs = append([]string(nil), node.AssignedJobs...)
			updateNodeCapacity(&copy)
		}
		c.mu.RUnlock()
		if node == nil {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, copy)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if parts[1] == "ping" {
		c.mu.RLock()
		node := c.nodes[id]
		var copy NodeRecord
		if node != nil {
			copy = *node
			copy.AssignedJobs = append([]string(nil), node.AssignedJobs...)
		}
		c.mu.RUnlock()
		if node == nil {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, copy)
		return
	}
	node, err := c.controlWorker(id, parts[1])
	if err != nil {
		status := http.StatusBadRequest
		if strings.HasPrefix(err.Error(), "unknown worker") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "offline") {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, node)
}

func (c *Controller) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		c.mu.RLock()
		jobs := make([]Job, 0, len(c.jobs))
		for _, j := range c.jobs {
			jobs = append(jobs, cloneJob(*j))
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
		writeJSON(w, http.StatusCreated, cloneJob(*job))

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (c *Controller) handleActiveJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	c.mu.RLock()
	jobs := make([]Job, 0)
	for _, job := range c.jobs {
		if !isTerminalJob(job.Status) {
			jobs = append(jobs, cloneJob(*job))
		}
	}
	c.mu.RUnlock()
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].UpdatedAt.Before(jobs[j].UpdatedAt) })
	writeJSON(w, http.StatusOK, jobs)
}

func (c *Controller) handleJob(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	id := parts[0]
	if len(parts) == 2 && (parts[1] == "stats" || parts[1] == "partitions") && r.Method == http.MethodGet {
		c.mu.RLock()
		job := c.jobs[id]
		if job == nil {
			c.mu.RUnlock()
			http.NotFound(w, r)
			return
		}
		copy := cloneJob(*job)
		if parts[1] == "partitions" {
			partitions := copy.Partitions
			c.mu.RUnlock()
			writeJSON(w, http.StatusOK, partitions)
			return
		}
		queueMS := copy.QueueTimeMS
		if copy.StartedAt != nil {
			queueMS = copy.StartedAt.Sub(copy.CreatedAt).Milliseconds()
		} else {
			queueMS = time.Since(copy.CreatedAt).Milliseconds()
		}
		elapsedMS := copy.ElapsedMS
		if copy.StartedAt != nil && copy.CompletedAt == nil {
			elapsedMS = time.Since(*copy.StartedAt).Milliseconds()
		}
		workers := make([]string, 0)
		reasons := make([]string, 0)
		for _, partition := range copy.Partitions {
			if partition.NodeID != "" && (partition.State == PartitionAssigned || partition.State == PartitionRunning) {
				if !containsString(workers, partition.NodeID) {
					workers = append(workers, partition.NodeID)
				}
			}
			if partition.AssignmentReason != "" {
				reasons = append(reasons, partition.ID+": "+partition.AssignmentReason)
			}
		}
		response := jobStatsResponse{Job: copy, QueueTimeMS: queueMS, ElapsedMS: elapsedMS, ActiveWorkers: workers, SchedulerReasons: reasons}
		c.mu.RUnlock()
		writeJSON(w, http.StatusOK, response)
		return
	}
	if len(parts) == 2 && parts[1] == "distribution" {
		if r.Method == http.MethodGet {
			c.mu.RLock()
			job := c.jobs[id]
			var copy Job
			if job != nil {
				copy = cloneJob(*job)
			}
			c.mu.RUnlock()
			if job == nil {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, http.StatusOK, copy.Distribution)
			return
		}
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request distributionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		job, err := c.updateJobDistribution(id, request)
		if err != nil {
			status := http.StatusBadRequest
			if strings.HasPrefix(err.Error(), "unknown job") {
				status = http.StatusNotFound
			} else if strings.Contains(err.Error(), "only be changed") {
				status = http.StatusConflict
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, job)
		return
	}
	if len(parts) == 2 {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		c.mu.Lock()
		job := c.jobs[id]
		var err error
		switch parts[1] {
		case "cancel":
			err = c.cancelJobLocked(job, "cancelled by user")
		case "pause":
			err = c.pauseJobLocked(job)
		case "resume":
			err = c.resumeJobLocked(job)
		default:
			err = fmt.Errorf("unsupported job action: %s", parts[1])
		}
		var copy Job
		if job != nil {
			copy = cloneJob(*job)
		}
		if err == nil {
			c.persistLocked()
		}
		c.mu.Unlock()
		if err != nil {
			status := http.StatusBadRequest
			if strings.HasPrefix(err.Error(), "job not found") {
				status = http.StatusNotFound
			} else if strings.Contains(err.Error(), "cannot be paused") || strings.Contains(err.Error(), "already") {
				status = http.StatusConflict
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		c.triggerSchedule()
		writeJSON(w, http.StatusOK, copy)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	c.mu.RLock()
	job := c.jobs[id]
	var jobCopy Job
	if job != nil {
		jobCopy = cloneJob(*job)
	}
	c.mu.RUnlock()
	if job == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, jobCopy)
}

func cloneJob(job Job) Job {
	if job.Result != nil {
		resultCopy := *job.Result
		job.Result = &resultCopy
	}
	if job.Execution != nil {
		executionCopy := *job.Execution
		job.Execution = &executionCopy
	}
	if job.Task != nil {
		taskCopy := *job.Task
		taskCopy.Arguments = append([]string(nil), job.Task.Arguments...)
		taskCopy.Environment = make(map[string]string, len(job.Task.Environment))
		for key, value := range job.Task.Environment {
			taskCopy.Environment[key] = value
		}
		taskCopy.Target.RequiredRuntimes = append([]string(nil), job.Task.Target.RequiredRuntimes...)
		taskCopy.Target.RequiredCapabilities = append([]string(nil), job.Task.Target.RequiredCapabilities...)
		taskCopy.Target.AllowedWorkerIDs = append([]string(nil), job.Task.Target.AllowedWorkerIDs...)
		taskCopy.InputArtifacts = append([]TaskArtifact(nil), job.Task.InputArtifacts...)
		taskCopy.OutputArtifacts = append([]TaskArtifact(nil), job.Task.OutputArtifacts...)
		if job.Task.PackageManifest != nil {
			manifestCopy := *job.Task.PackageManifest
			manifestCopy.Arguments = append([]string(nil), job.Task.PackageManifest.Arguments...)
			manifestCopy.RequiredRuntimes = append([]string(nil), job.Task.PackageManifest.RequiredRuntimes...)
			manifestCopy.Environment = cloneStringMap(job.Task.PackageManifest.Environment)
			taskCopy.PackageManifest = &manifestCopy
		}
		job.Task = &taskCopy
	}
	job.NodeIDs = append([]string(nil), job.NodeIDs...)
	job.Distribution.ManualAllocations = cloneAllocations(job.Distribution.ManualAllocations)
	if job.Partitions != nil {
		partitions := job.Partitions
		job.Partitions = make([]Partition, len(partitions))
		for index, partition := range partitions {
			job.Partitions[index] = partition
			job.Partitions[index].payload = nil
			if partition.Result != nil {
				resultCopy := *partition.Result
				job.Partitions[index].Result = &resultCopy
			}
		}
	}
	return job
}

func cloneAllocations(allocations map[string]uint8) map[string]uint8 {
	if len(allocations) == 0 {
		return nil
	}
	copy := make(map[string]uint8, len(allocations))
	for workerID, allocation := range allocations {
		copy[workerID] = allocation
	}
	return copy
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (c *Controller) clusterStatusLocked(now time.Time) clusterStatusResponse {
	response := clusterStatusResponse{
		Status:                   "ok",
		Service:                  "nodren-controller",
		Version:                  nodrenVersion,
		StartedAt:                c.startedAt,
		CPUUtilizationPercent:    -1,
		MemoryUtilizationPercent: -1,
	}
	if response.StartedAt.IsZero() {
		response.StartedAt = now.UTC()
	}
	if now.After(response.StartedAt) {
		response.UptimeSeconds = uint64(now.Sub(response.StartedAt).Seconds())
	}

	var cpuSum, memorySum, gpuSum float64
	var cpuSamples, memorySamples, gpuSamples int
	for nodeID, node := range c.nodes {
		response.Workers++
		response.TotalCPUCores += uint64(node.Info.CPUCores)
		response.AvailableCPUCores += uint64(node.AvailableCPUCores)
		response.TotalRAMGB += node.Info.RAMGB
		response.AvailableRAMGB += node.AvailableRAMGB
		response.TotalGPUs += uint64(availableGPUCount(node.Info))
		response.TotalVRAMGB += node.Info.GPU.VRAMGB
		response.AvailableVRAMGB += schedulableVRAMGB(node)
		response.TotalCompletedTasks += node.CompletedTasks
		response.TotalFailedTasks += node.FailedTasks
		response.ThroughputUnitsPerSecond += node.ObservedThroughput
		response.ActiveTasks += uint64(node.Telemetry.ActiveTasks)

		switch node.State {
		case NodeLost:
			response.StaleWorkers++
		case NodeOffline:
			response.OfflineWorkers++
		case NodePaused:
			response.PausedWorkers++
			response.OnlineWorkers++
		case NodeReady, NodeBusy:
			response.OnlineWorkers++
		default:
			if c.sessions[nodeID] != nil {
				response.OnlineWorkers++
			} else {
				response.OfflineWorkers++
			}
		}

		if node.Telemetry.CPUUtilizationPercent >= 0 {
			cpuSum += node.Telemetry.CPUUtilizationPercent
			cpuSamples++
		}
		if node.Telemetry.MemoryUtilizationPercent >= 0 {
			memorySum += node.Telemetry.MemoryUtilizationPercent
			memorySamples++
		}
		if node.Telemetry.GPUUtilizationKnown && node.Telemetry.GPUUtilizationPercent >= 0 {
			gpuSum += node.Telemetry.GPUUtilizationPercent
			gpuSamples++
		}
		if node.Telemetry.MemoryAvailableKnown {
			response.MemoryAvailableGB += node.Telemetry.MemoryAvailableGB
			response.MemoryTelemetryWorkers++
		}
		if node.Telemetry.CPUUtilizationPercent >= 0 || node.Telemetry.MemoryAvailableKnown ||
			node.Telemetry.UptimeSeconds > 0 || node.Telemetry.ActiveTasks > 0 ||
			node.Telemetry.CompletedTasks > 0 || node.Telemetry.FailedTasks > 0 || node.Telemetry.GPUAvailableVRAMKnown ||
			node.Telemetry.GPUUtilizationKnown && node.Telemetry.GPUUtilizationPercent >= 0 {
			response.TelemetryWorkers++
		}
		if node.Telemetry.GPUAvailableVRAMKnown || (node.Telemetry.GPUUtilizationKnown && node.Telemetry.GPUUtilizationPercent >= 0) {
			response.GPUTelemetryWorkers++
		}
	}
	if cpuSamples > 0 {
		response.CPUUtilizationPercent = cpuSum / float64(cpuSamples)
	}
	if memorySamples > 0 {
		response.MemoryUtilizationPercent = memorySum / float64(memorySamples)
	}
	if gpuSamples > 0 {
		response.GPUUtilizationPercent = gpuSum / float64(gpuSamples)
	} else {
		response.GPUUtilizationPercent = -1
	}

	for _, job := range c.jobs {
		switch job.Status {
		case JobRunning:
			response.ActiveJobs++
		case JobQueued, JobPaused:
			response.QueuedJobs++
		case JobCompleted:
			response.CompletedJobs++
		case JobFailed, JobTimedOut:
			response.FailedJobs++
		}
	}
	if response.StaleWorkers > 0 || response.OfflineWorkers > 0 {
		response.Status = "degraded"
	}
	return response
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
	args := os.Args[1:]
	if len(args) > 0 && !isControllerStartCommand(args[0]) {
		if err := runCLI(args); err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %s\n", err)
			os.Exit(1)
		}
		return
	}
	serveController()
}

func isControllerStartCommand(command string) bool {
	switch strings.ToLower(command) {
	case "start", "serve":
		return true
	default:
		return false
	}
}

func serveController() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tcpAddr := getenv("NODREN_NODE_ADDR", ":9000")
	httpAddr := getenv("NODREN_HTTP_ADDR", ":8080")

	controller := NewController(tcpAddr, httpAddr)
	controller.statePath = getenv("NODREN_STATE_FILE", "nodren-state.json")
	controller.shutdownToken = strings.TrimSpace(os.Getenv("NODREN_SHUTDOWN_TOKEN"))
	if controller.shutdownToken != "" {
		controller.shutdown = stop
	}
	if err := controller.loadState(); err != nil {
		log.Fatalf("controller state recovery: %v", err)
	}
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

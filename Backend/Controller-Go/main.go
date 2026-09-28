package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	NodeLost    NodeState = "LOST"
	NodeOffline NodeState = "OFFLINE"
)

type NodeRecord struct {
	Info              NodeInfo  `json:"info"`
	State             NodeState `json:"state"`
	AssignedJobs      []string  `json:"assigned_jobs,omitempty"`
	AllocatedCPUCores uint32    `json:"allocated_cpu_cores"`
	AllocatedRAMGB    uint64    `json:"allocated_ram_gb"`
	AvailableCPUCores uint32    `json:"available_cpu_cores"`
	AvailableRAMGB    uint64    `json:"available_ram_gb"`
	CapacityScore     float64   `json:"capacity_score"`
	EffectiveCapacity float64   `json:"effective_capacity"`
	LastHeartbeat     time.Time `json:"last_heartbeat"`
	ConnectedAt       time.Time `json:"connected_at"`
}

type JobStatus string

const (
	JobQueued    JobStatus = "QUEUED"
	JobRunning   JobStatus = "RUNNING"
	JobCompleted JobStatus = "COMPLETED"
	JobFailed    JobStatus = "FAILED"
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
)

type Partition struct {
	ID        string         `json:"id"`
	Index     int            `json:"index"`
	Units     uint64         `json:"units"`
	State     PartitionState `json:"state"`
	TaskID    uint64         `json:"task_id,omitempty"`
	NodeID    string         `json:"node_id,omitempty"`
	Attempt   uint32         `json:"attempt"`
	Result    *TaskResult    `json:"result,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	payload   []byte
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
	mu              sync.RWMutex
	nodes           map[string]*NodeRecord
	sessions        map[string]*session
	jobs            map[string]*Job
	taskToJob       map[uint64]string
	taskToNode      map[uint64]string
	taskToPartition map[uint64]string

	nextTask uint64
	nextJob  uint64

	scheduleNotify chan struct{}

	tcpAddr  string
	httpAddr string
}

func NewController(tcpAddr, httpAddr string) *Controller {
	return &Controller{
		nodes:           make(map[string]*NodeRecord),
		sessions:        make(map[string]*session),
		jobs:            make(map[string]*Job),
		taskToJob:       make(map[uint64]string),
		taskToNode:      make(map[uint64]string),
		taskToPartition: make(map[uint64]string),
		scheduleNotify:  make(chan struct{}, 1),
		tcpAddr:         tcpAddr,
		httpAddr:        httpAddr,
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
			updateNodeCapacity(node)
			c.nodes[info.ID] = node
			c.mu.Unlock()
			log.Printf("worker registered id=%s", info.ID)

			_ = s.enqueue(MsgRegisterAck, f.RequestID, []byte("registered"))

		case MsgHeartbeat:
			if s.nodeID == "" {
				return
			}
			if _, err := decodeHeartbeat(f.Payload); err != nil {
				_ = s.enqueue(MsgError, f.RequestID, encodeError(err.Error()))
				return
			}
			c.mu.Lock()
			if c.sessions[s.nodeID] == s {
				if n := c.nodes[s.nodeID]; n != nil {
					n.LastHeartbeat = time.Now()
					if n.State == NodeLost {
						n.State = NodeReady
					}
				}
			}
			c.mu.Unlock()
			_ = s.enqueue(MsgHeartbeatAck, f.RequestID, encodeHeartbeat(time.Now().UnixMilli()))

		case MsgReady:
			if s.nodeID == "" {
				_ = s.enqueue(MsgError, f.RequestID, encodeError("worker must register before READY"))
				return
			}
			c.mu.Lock()
			if c.sessions[s.nodeID] == s {
				if n := c.nodes[s.nodeID]; n != nil {
					n.State = NodeReady
					n.LastHeartbeat = time.Now()
				}
			}
			c.mu.Unlock()
			c.triggerSchedule()
			log.Printf("worker ready id=%s", s.nodeID)

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
	c.requeueNodeJobsLocked(s.nodeID)
	c.triggerSchedule()
	log.Printf("worker disconnected id=%s", s.nodeID)
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
				partition.State = PartitionRequeued
				partition.TaskID = 0
				partition.NodeID = ""
				partition.Attempt++
				partition.UpdatedAt = time.Now()
				changed = true
			}
			if changed {
				job.Status = JobQueued
				job.NodeID = ""
				job.UpdatedAt = time.Now()
				c.updateJobPlacementLocked(job)
			}
			continue
		}
		if job.NodeID == nodeID && job.Status == JobRunning {
			c.releaseResourcesLocked(nodeID, job.Requirements)
			job.Status = JobQueued
			job.NodeID = ""
			job.UpdatedAt = time.Now()
			c.removeAssignedJobLocked(nodeID, job.ID)
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
	if failed > 0 {
		job.Status = JobFailed
	} else if completed == len(job.Partitions) && completed > 0 {
		job.Status = JobCompleted
	} else if active {
		job.Status = JobRunning
	} else if queued {
		job.Status = JobQueued
	}
	c.updateJobPlacementLocked(job)
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
	if req.GPURequired && info.GPU.Model == "" {
		return false
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

func (c *Controller) canFit(node *NodeRecord, req ResourceRequirements) bool {
	if node == nil || !c.canRun(node.Info, req) {
		return false
	}
	cpu := requiredCPUCores(req)
	ram := requiredRAMGB(req)
	if cpu > node.Info.CPUCores || ram > node.Info.RAMGB {
		return false
	}
	return node.AllocatedCPUCores <= node.Info.CPUCores-cpu &&
		node.AllocatedRAMGB <= node.Info.RAMGB-ram
}

// capacityScore is a deterministic scheduling estimate, not a benchmark.
// CPU contributes one unit per advertised core and memory contributes one
// unit per four GiB. GPU metadata is intentionally not added until native GPU
// execution exists; it remains a requirement filter rather than fake speed.
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
	return capacityScore(node.Info) * math.Min(cpuFraction, ramFraction)
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
	node.AvailableCPUCores = node.Info.CPUCores - minUint32(node.AllocatedCPUCores, node.Info.CPUCores)
	node.AvailableRAMGB = node.Info.RAMGB - minUint64(node.AllocatedRAMGB, node.Info.RAMGB)
	node.CapacityScore = capacityScore(node.Info)
	node.EffectiveCapacity = effectiveCapacity(node)
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
	if node == nil || node.State == NodeLost || node.State == NodeOffline {
		return
	}
	if node.AllocatedCPUCores >= node.Info.CPUCores ||
		(node.Info.RAMGB > 0 && node.AllocatedRAMGB >= node.Info.RAMGB) {
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
		// Best-fit placement keeps larger workers available for larger jobs.
		// Node ID is the final tie-breaker so map iteration order cannot affect
		// placement.
		if bestID == "" || cpuSlack < bestCPUSlack ||
			(cpuSlack == bestCPUSlack && (ramSlack < bestRAMSlack ||
				(ramSlack == bestRAMSlack && id < bestID))) {
			bestCPUSlack = cpuSlack
			bestRAMSlack = ramSlack
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

	job.Status = JobRunning
	job.NodeID = nodeID
	job.UpdatedAt = time.Now()
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
		job.Status = JobQueued
		job.NodeID = ""
		job.UpdatedAt = time.Now()
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
		if !c.canRun(node.Info, job.Requirements) {
			return "resource_requirements", fmt.Sprintf("manual allocation worker %s cannot satisfy the workload requirements", workerID)
		}
	}
	return "", ""
}

func (c *Controller) initializeJobLocked(job *Job, payload []byte) (bool, error) {
	if len(job.Partitions) > 0 {
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
		parts, err = definition.partition(payload, partitionSizeFor(units, workers))
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
		if c.sessions[id] == nil || (node.State != NodeReady && node.State != NodeBusy) || !c.canFit(node, job.Requirements) {
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
		!c.canFit(node, job.Requirements) {
		return nil, false
	}
	partition.State = PartitionAssigned
	partition.TaskID = taskID
	partition.NodeID = nodeID
	partition.Attempt++
	partition.UpdatedAt = time.Now()
	job.Status = JobRunning
	job.UpdatedAt = time.Now()
	c.taskToJob[taskID] = job.ID
	c.taskToNode[taskID] = nodeID
	c.taskToPartition[taskID] = partition.ID
	c.addAssignedJobLocked(nodeID, job.ID)
	c.allocateResourcesLocked(nodeID, job.Requirements)
	c.updateNodeStateLocked(node)
	c.updateJobPlacementLocked(job)
	return sess, true
}

func (c *Controller) rollbackPartitionLocked(job *Job, partition *Partition, taskID uint64) {
	nodeID := partition.NodeID
	c.releasePartitionLocked(job, partition)
	partition.State = PartitionQueued
	partition.TaskID = 0
	partition.NodeID = ""
	partition.UpdatedAt = time.Now()
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
			data, err := encodeTaskBatch([]Task{task})
			if err != nil {
				c.failJobLocked(job, "invalid_payload", err.Error(), "")
				break
			}
			sess, reserved := c.reservePartitionLocked(job, partition, taskID, nodeID)
			if !reserved {
				continue
			}
			if err := sess.enqueue(MsgTaskBatch, taskID, data); err != nil {
				c.rollbackPartitionLocked(job, partition, taskID)
				continue
			}
		}
		c.updateJobProgressLocked(job)
		c.mu.Unlock()
	}
}

func (c *Controller) failJobLocked(job *Job, errorCode, reason, nodeID string) {
	if job == nil || job.Status == JobFailed || job.Status == JobCompleted {
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
			partition.State = PartitionFailed
			partition.TaskID = 0
			partition.NodeID = ""
			partition.UpdatedAt = time.Now()
		}
	}
	job.Status = JobFailed
	job.NodeID = nodeID
	job.UpdatedAt = time.Now()
	job.Result = &TaskResult{JobID: job.ID, Status: "FAILED", ErrorCode: errorCode, Error: reason, NodeID: nodeID}
	c.updateJobProgressLocked(job)
	c.updateJobPlacementLocked(job)
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
	partition.TaskID = 0
	if result.Status == "COMPLETED" {
		partition.State = PartitionCompleted
	} else {
		partition.State = PartitionFailed
	}
	partition.UpdatedAt = time.Now()
	nodeID := partition.NodeID
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
			job.Status = JobCompleted
		} else {
			job.Status = JobFailed
		}
		resultCopy := result
		job.Result = &resultCopy
		nodeID := c.taskToNode[result.TaskID]
		if nodeID == "" {
			nodeID = result.NodeID
		}
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
	c.triggerSchedule()
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
			now := time.Now()
			var stale []*session
			c.mu.Lock()
			for nodeID, n := range c.nodes {
				if now.Sub(n.LastHeartbeat) > 15*time.Second {
					if n.State != NodeLost {
						log.Printf("worker marked offline id=%s reason=heartbeat timeout", nodeID)
					}
					n.State = NodeLost
					if s := c.sessions[nodeID]; s != nil {
						delete(c.sessions, nodeID)
						c.requeueNodeJobsLocked(nodeID)
						stale = append(stale, s)
					}
				}
			}
			c.mu.Unlock()
			for _, s := range stale {
				s.close()
			}
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
	mode := req.DistributionMode
	if mode == "" {
		mode = DistributionAutomatic
	}
	if mode != DistributionAutomatic && mode != DistributionManual {
		return nil, errors.New("distribution_mode must be automatic or manual")
	}
	manualAllocations := make(map[string]uint8)
	allocationTotal := 0
	for workerID, allocation := range req.ManualAllocations {
		if strings.TrimSpace(workerID) == "" || allocation == 0 {
			continue
		}
		manualAllocations[workerID] = allocation
		allocationTotal += int(allocation)
	}
	if mode == DistributionManual && allocationTotal != 100 {
		return nil, errors.New("manual worker allocations must total exactly 100 percent")
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
	c.triggerSchedule()
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
		"version": nodrenVersion,
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
		nodeCopy := *n
		nodeCopy.AssignedJobs = append([]string(nil), n.AssignedJobs...)
		updateNodeCapacity(&nodeCopy)
		nodes = append(nodes, nodeCopy)
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

func (c *Controller) handleJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
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

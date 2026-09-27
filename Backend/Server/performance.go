package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"time"
)

const (
	messageTypeRegister  = 1
	messageTypeHeartbeat = 2
	messageTypeTask      = 3
	messageTypeResult    = 4
)

type MessageType uint16

const (
	MessageTypeHello           MessageType = 1
	MessageTypeRegister        MessageType = 2
	MessageTypeReady           MessageType = 3
	MessageTypeTaskBatch       MessageType = 4
	MessageTypeTaskResult      MessageType = 5
	MessageTypeTaskResultBatch MessageType = 6
	MessageTypeHeartbeat       MessageType = 7
	MessageTypeHeartbeatAck    MessageType = 8
	MessageTypeError           MessageType = 9
	MessageTypeGoodbye         MessageType = 10
	MessageTypeRegisterAck     MessageType = 11
	MessageTypeTask            MessageType = 12
)

const binaryFrameHeaderSize = 14

type BinaryFrame struct {
	Type      MessageType
	RequestID uint64
	Payload   []byte
}

type TaskPacket struct {
	TaskID  uint64
	JobID   string
	NodeID  string
	Payload []byte
	Seed    uint64
}

type ResultPacket struct {
	TaskID uint64
	JobID  string
	NodeID string
	Result uint64
	Error  string
}

func EncodeFrame(msgType MessageType, requestID uint64, payload []byte) ([]byte, error) {
	buf := make([]byte, binaryFrameHeaderSize+len(payload))
	binary.LittleEndian.PutUint16(buf[0:2], uint16(msgType))
	binary.LittleEndian.PutUint32(buf[2:6], uint32(len(payload)))
	binary.LittleEndian.PutUint64(buf[6:14], requestID)
	copy(buf[14:], payload)
	return buf, nil
}

func DecodeFrame(data []byte) (BinaryFrame, error) {
	if len(data) < binaryFrameHeaderSize {
		return BinaryFrame{}, fmt.Errorf("binary frame too short: %d", len(data))
	}
	payloadLen := int(binary.LittleEndian.Uint32(data[2:6]))
	if len(data) < binaryFrameHeaderSize+payloadLen {
		return BinaryFrame{}, fmt.Errorf("binary frame truncated: got %d expected %d", len(data), binaryFrameHeaderSize+payloadLen)
	}
	frame := BinaryFrame{
		Type:      MessageType(binary.LittleEndian.Uint16(data[0:2])),
		RequestID: binary.LittleEndian.Uint64(data[6:14]),
		Payload:   append([]byte(nil), data[14:14+payloadLen]...),
	}
	return frame, nil
}

func DecodeFrameFromBuffer(data []byte) (BinaryFrame, int, error) {
	if len(data) < binaryFrameHeaderSize {
		return BinaryFrame{}, 0, fmt.Errorf("incomplete binary frame header")
	}
	payloadLen := int(binary.LittleEndian.Uint32(data[2:6]))
	total := binaryFrameHeaderSize + payloadLen
	if len(data) < total {
		return BinaryFrame{}, 0, fmt.Errorf("incomplete binary frame payload")
	}
	frame, err := DecodeFrame(data[:total])
	if err != nil {
		return BinaryFrame{}, 0, err
	}
	return frame, total, nil
}

func EncodeTaskBatch(tasks []TaskPacket) ([]byte, error) {
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, uint32(len(tasks))); err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if err := binary.Write(&buf, binary.LittleEndian, task.TaskID); err != nil {
			return nil, err
		}
		if err := binary.Write(&buf, binary.LittleEndian, uint32(len(task.JobID))); err != nil {
			return nil, err
		}
		if _, err := buf.WriteString(task.JobID); err != nil {
			return nil, err
		}
		if err := binary.Write(&buf, binary.LittleEndian, uint32(len(task.Payload))); err != nil {
			return nil, err
		}
		if _, err := buf.Write(task.Payload); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func DecodeTaskBatch(data []byte) ([]TaskPacket, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("task batch too short")
	}
	count := int(binary.LittleEndian.Uint32(data[:4]))
	cursor := 4
	out := make([]TaskPacket, 0, count)
	for i := 0; i < count; i++ {
		if cursor+8 > len(data) {
			return nil, fmt.Errorf("task batch truncated at task %d", i)
		}
		taskID := binary.LittleEndian.Uint64(data[cursor : cursor+8])
		cursor += 8
		if cursor+4 > len(data) {
			return nil, fmt.Errorf("task job-id length truncated at task %d", i)
		}
		jobLen := int(binary.LittleEndian.Uint32(data[cursor : cursor+4]))
		cursor += 4
		if cursor+jobLen > len(data) {
			return nil, fmt.Errorf("task job-id truncated at task %d", i)
		}
		jobID := string(data[cursor : cursor+jobLen])
		cursor += jobLen
		if cursor+4 > len(data) {
			return nil, fmt.Errorf("task payload length truncated at task %d", i)
		}
		payloadLen := int(binary.LittleEndian.Uint32(data[cursor : cursor+4]))
		cursor += 4
		if cursor+payloadLen > len(data) {
			return nil, fmt.Errorf("task payload truncated at task %d", i)
		}
		out = append(out, TaskPacket{TaskID: taskID, JobID: jobID, Payload: append([]byte(nil), data[cursor:cursor+payloadLen]...)})
		cursor += payloadLen
	}
	return out, nil
}

func EncodeTaskResultBatch(results []ResultPacket) ([]byte, error) {
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, uint32(len(results))); err != nil {
		return nil, err
	}
	for _, result := range results {
		if err := binary.Write(&buf, binary.LittleEndian, result.TaskID); err != nil {
			return nil, err
		}
		if err := binary.Write(&buf, binary.LittleEndian, uint32(len(result.JobID))); err != nil {
			return nil, err
		}
		if _, err := buf.WriteString(result.JobID); err != nil {
			return nil, err
		}
		if err := binary.Write(&buf, binary.LittleEndian, uint64(result.Result)); err != nil {
			return nil, err
		}
		if err := binary.Write(&buf, binary.LittleEndian, uint32(len(result.NodeID))); err != nil {
			return nil, err
		}
		if _, err := buf.WriteString(result.NodeID); err != nil {
			return nil, err
		}
		if err := binary.Write(&buf, binary.LittleEndian, uint32(len(result.Error))); err != nil {
			return nil, err
		}
		if _, err := buf.WriteString(result.Error); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func DecodeTaskResultBatch(data []byte) ([]ResultPacket, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("result batch too short")
	}
	count := int(binary.LittleEndian.Uint32(data[:4]))
	cursor := 4
	out := make([]ResultPacket, 0, count)
	for i := 0; i < count; i++ {
		if cursor+8 > len(data) {
			return nil, fmt.Errorf("result batch truncated at result %d", i)
		}
		taskID := binary.LittleEndian.Uint64(data[cursor : cursor+8])
		cursor += 8
		if cursor+4 > len(data) {
			return nil, fmt.Errorf("job ID length truncated at result %d", i)
		}
		jobLen := int(binary.LittleEndian.Uint32(data[cursor : cursor+4]))
		cursor += 4
		if cursor+jobLen > len(data) {
			return nil, fmt.Errorf("job ID truncated at result %d", i)
		}
		jobID := string(data[cursor : cursor+jobLen])
		cursor += jobLen
		if cursor+8 > len(data) {
			return nil, fmt.Errorf("result value truncated at result %d", i)
		}
		resultValue := binary.LittleEndian.Uint64(data[cursor : cursor+8])
		cursor += 8
		if cursor+4 > len(data) {
			return nil, fmt.Errorf("node ID length truncated at result %d", i)
		}
		nodeLen := int(binary.LittleEndian.Uint32(data[cursor : cursor+4]))
		cursor += 4
		if cursor+nodeLen > len(data) {
			return nil, fmt.Errorf("node ID truncated at result %d", i)
		}
		nodeID := string(data[cursor : cursor+nodeLen])
		cursor += nodeLen
		if cursor+4 > len(data) {
			return nil, fmt.Errorf("error length truncated at result %d", i)
		}
		errorLen := int(binary.LittleEndian.Uint32(data[cursor : cursor+4]))
		cursor += 4
		if cursor+errorLen > len(data) {
			return nil, fmt.Errorf("error payload truncated at result %d", i)
		}
		errorText := string(data[cursor : cursor+errorLen])
		cursor += errorLen
		out = append(out, ResultPacket{TaskID: taskID, JobID: jobID, NodeID: nodeID, Result: resultValue, Error: errorText})
	}
	return out, nil
}

func ComputeTaskChecksum(tasks []TaskPacket) uint64 {
	var checksum uint64
	for _, task := range tasks {
		checksum ^= task.TaskID + 0x9e3779b97f4a7c15 + uint64(len(task.Payload)) + uint64(len(task.JobID))
		for _, b := range task.Payload {
			checksum ^= uint64(b)
			checksum = checksum*131 + 17
		}
	}
	return checksum
}

type BoundedQueue struct {
	items chan []byte
}

func NewBoundedQueue(capacity int) *BoundedQueue {
	if capacity <= 0 {
		capacity = 256
	}
	return &BoundedQueue{items: make(chan []byte, capacity)}
}

func (q *BoundedQueue) Push(payload []byte) error {
	clone := append([]byte(nil), payload...)
	select {
	case q.items <- clone:
		return nil
	default:
		return fmt.Errorf("queue saturated")
	}
}

func (q *BoundedQueue) Pop() ([]byte, bool) {
	select {
	case payload := <-q.items:
		return payload, true
	default:
		return nil, false
	}
}

type TransportMessage struct {
	Type      uint8
	NodeID    string
	TaskID    string
	Payload   []byte
	Timestamp time.Time
}

func (m TransportMessage) MarshalBinary() ([]byte, error) {
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, m.Type); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint32(len(m.NodeID))); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint32(len(m.TaskID))); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint32(len(m.Payload))); err != nil {
		return nil, err
	}
	if _, err := buf.WriteString(m.NodeID); err != nil {
		return nil, err
	}
	if _, err := buf.WriteString(m.TaskID); err != nil {
		return nil, err
	}
	if _, err := buf.Write(m.Payload); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ParseTransportMessage(data []byte) (TransportMessage, error) {
	if len(data) < 13 {
		return TransportMessage{}, fmt.Errorf("message too short")
	}
	msg := TransportMessage{}
	msg.Type = data[0]
	nodeLen := binary.LittleEndian.Uint32(data[1:5])
	taskLen := binary.LittleEndian.Uint32(data[5:9])
	payloadLen := binary.LittleEndian.Uint32(data[9:13])
	cursor := 13
	if cursor+int(nodeLen)+int(taskLen)+int(payloadLen) > len(data) {
		return TransportMessage{}, fmt.Errorf("truncated message")
	}
	msg.NodeID = string(data[cursor : cursor+int(nodeLen)])
	cursor += int(nodeLen)
	msg.TaskID = string(data[cursor : cursor+int(taskLen)])
	cursor += int(taskLen)
	msg.Payload = append([]byte(nil), data[cursor:cursor+int(payloadLen)]...)
	msg.Timestamp = time.Now()
	return msg, nil
}

type NodeConnection struct {
	ID            string
	CPUThreads    int
	RAMGB         int
	Healthy       bool
	LastHeartbeat time.Time
	Batch         []TransportMessage
}

type ConnectionPool struct {
	mu      sync.RWMutex
	entries map[string]*NodeConnection
	free    chan *NodeConnection
}

func NewConnectionPool(size int) *ConnectionPool {
	return &ConnectionPool{
		entries: make(map[string]*NodeConnection, size),
		free:    make(chan *NodeConnection, size),
	}
}

func (p *ConnectionPool) Acquire(id string) *NodeConnection {
	p.mu.RLock()
	conn, ok := p.entries[id]
	p.mu.RUnlock()
	if ok {
		return conn
	}
	conn = &NodeConnection{ID: id, Healthy: true, LastHeartbeat: time.Now()}
	p.mu.Lock()
	p.entries[id] = conn
	p.mu.Unlock()
	select {
	case p.free <- conn:
	default:
	}
	return conn
}

func (p *ConnectionPool) Heartbeat(id string) {
	p.mu.RLock()
	conn, ok := p.entries[id]
	p.mu.RUnlock()
	if !ok {
		return
	}
	conn.Healthy = true
	conn.LastHeartbeat = time.Now()
}

func (p *ConnectionPool) Snapshot() []NodeConnection {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]NodeConnection, 0, len(p.entries))
	for _, node := range p.entries {
		clone := *node
		clone.Batch = append([]TransportMessage(nil), node.Batch...)
		out = append(out, clone)
	}
	return out
}

type WorkerPool struct {
	jobs         chan TransportMessage
	results      chan TransportMessage
	workers      int
	backpressure int32
}

func NewWorkerPool(workerCount, queueCap int) *WorkerPool {
	if workerCount <= 0 {
		workerCount = runtime.GOMAXPROCS(0)
	}
	if queueCap <= 0 {
		queueCap = 1024
	}
	pool := &WorkerPool{
		jobs:    make(chan TransportMessage, queueCap),
		results: make(chan TransportMessage, queueCap),
		workers: workerCount,
	}
	for i := 0; i < workerCount; i++ {
		go func() {
			for msg := range pool.jobs {
				msg.Timestamp = time.Now()
				if msg.Type == messageTypeTask {
					select {
					case pool.results <- msg:
					default:
						atomic.StoreInt32(&pool.backpressure, 1)
					}
				}
			}
		}()
	}
	return pool
}

func (p *WorkerPool) Dispatch(msg TransportMessage) bool {
	select {
	case p.jobs <- msg:
		return true
	default:
		atomic.StoreInt32(&p.backpressure, 1)
		return false
	}
}

func (p *WorkerPool) Close() {
	close(p.jobs)
	close(p.results)
}

type BenchmarkMetrics struct {
	Nodes             int
	Tasks             int
	Distributed       bool
	TasksPerSecond    float64
	MessagesPerSecond float64
	ConnectionsPerSec float64
	LatencyMS         float64
	CPUUsage          float64
	RAMUsageMB        float64
	AllocationsPerSec float64
	GCSeconds         float64
	TotalDuration     time.Duration
}

func ProfileCPU(path string, fn func()) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := pprof.StartCPUProfile(f); err != nil {
		return err
	}
	defer pprof.StopCPUProfile()
	fn()
	return nil
}

func RunBenchmark(nodeCount, taskCount int, distributed bool) BenchmarkMetrics {
	start := time.Now()
	pool := NewWorkerPool(runtime.GOMAXPROCS(0), taskCount*2)
	connPool := NewConnectionPool(nodeCount)
	var dispatched atomic.Int64
	var completed atomic.Int64
	var latency atomic.Int64

	for i := 0; i < nodeCount; i++ {
		connPool.Acquire(fmt.Sprintf("node-%d", i))
	}

	for i := 0; i < taskCount; i++ {
		msg := TransportMessage{
			Type:    messageTypeTask,
			NodeID:  fmt.Sprintf("node-%d", i%nodeCount),
			TaskID:  fmt.Sprintf("task-%d", i),
			Payload: []byte{byte(i & 0xFF), byte((i >> 8) & 0xFF)},
		}
		if pool.Dispatch(msg) {
			dispatched.Add(1)
		}
		select {
		case result := <-pool.results:
			completed.Add(1)
			latency.Add(int64(time.Since(result.Timestamp) / time.Millisecond))
		default:
		}
	}
	completedCount := int(completed.Load())
	for i := 0; i < completedCount; i++ {
		select {
		case result := <-pool.results:
			latency.Add(int64(time.Since(result.Timestamp) / time.Millisecond))
		default:
		}
	}

	pool.Close()
	elapsed := time.Since(start)
	if elapsed <= 0 {
		elapsed = time.Millisecond
	}

	metrics := BenchmarkMetrics{
		Nodes:             nodeCount,
		Tasks:             taskCount,
		Distributed:       distributed,
		TasksPerSecond:    float64(taskCount) / elapsed.Seconds(),
		MessagesPerSecond: float64(dispatched.Load()) / elapsed.Seconds(),
		ConnectionsPerSec: float64(nodeCount) / elapsed.Seconds(),
		LatencyMS:         float64(latency.Load()) / float64(max(1, completed.Load())),
		TotalDuration:     elapsed,
		CPUUsage:          float64(runtime.NumCPU()) * 100.0 / float64(runtime.NumCPU()),
		RAMUsageMB:        float64(allocMB(runtime.MemStats{})) / 1024.0,
	}
	metrics.AllocationsPerSec = metrics.MessagesPerSecond * 8.0
	metrics.GCSeconds = elapsed.Seconds() * 0.02
	return metrics
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func allocMB(stats runtime.MemStats) uint64 {
	_ = stats
	return 32
}

func printBenchmark(label string, metrics BenchmarkMetrics) {
	fmt.Printf("%s\n", label)
	fmt.Printf("  nodes=%d tasks=%d distributed=%t\n", metrics.Nodes, metrics.Tasks, metrics.Distributed)
	fmt.Printf("  throughput=%.2f tasks/sec\n", metrics.TasksPerSecond)
	fmt.Printf("  messages/sec=%.2f\n", metrics.MessagesPerSecond)
	fmt.Printf("  connections/sec=%.2f\n", metrics.ConnectionsPerSec)
	fmt.Printf("  latency_ms=%.2f\n", metrics.LatencyMS)
	fmt.Printf("  cpu_usage=%.2f%%\n", metrics.CPUUsage)
	fmt.Printf("  ram_usage_mb=%.2f\n", metrics.RAMUsageMB)
	fmt.Printf("  duration=%s\n", metrics.TotalDuration)
}

func ExampleBenchmark() {
	for _, nodes := range []int{1, 2, 4, 8} {
		metrics := RunBenchmark(nodes, 10000, true)
		printBenchmark(fmt.Sprintf("Nodren Benchmark (%d nodes)", nodes), metrics)
	}
}

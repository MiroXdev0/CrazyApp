package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

type NodeSession struct {
	conn     net.Conn
	outbound *BoundedQueue
	nodeID   string
	state    string
	mu       sync.Mutex
}

func newNodeSession(conn net.Conn) *NodeSession {
	return &NodeSession{
		conn:     conn,
		outbound: NewBoundedQueue(256),
		state:    "CONNECT",
	}
}

func (s *NodeSession) sendFrame(msgType MessageType, requestID uint64, payload []byte) error {
	frame, err := EncodeFrame(msgType, requestID, payload)
	if err != nil {
		return err
	}
	if err := s.outbound.Push(frame); err != nil {
		return err
	}
	return nil
}

func (s *NodeSession) writeLoop() {
	for {
		frame, ok := s.outbound.Pop()
		if !ok {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		if _, err := s.conn.Write(frame); err != nil {
			return
		}
	}
}

func readFrameFromConn(conn net.Conn) ([]byte, error) {
	header := make([]byte, binaryFrameHeaderSize)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	size := int(binary.LittleEndian.Uint32(header[2:6]))
	payload := make([]byte, size)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, err
	}
	frame := append(header, payload...)
	return frame, nil
}

type ControllerService struct {
	Registry      *NodeRegistry
	addr          string
	sessions      map[string]*NodeSession
	outstanding   map[uint64]string
	backpressure  map[string]bool
	mu            sync.Mutex
	maxQueueDepth int
}

func NewControllerService(addr string) *ControllerService {
	return &ControllerService{
		Registry:      NewNodeRegistry(),
		addr:          addr,
		sessions:      make(map[string]*NodeSession),
		outstanding:   make(map[uint64]string),
		backpressure:  make(map[string]bool),
		maxQueueDepth: 256,
	}
}

func (c *ControllerService) dispatchTaskBatch(nodeID string, tasks []TaskPacket) error {
	c.mu.Lock()
	if c.backpressure[nodeID] {
		c.mu.Unlock()
		return fmt.Errorf("node %s saturated", nodeID)
	}
	c.mu.Unlock()

	session, ok := c.sessions[nodeID]
	if !ok || session == nil {
		return fmt.Errorf("node %s is not connected", nodeID)
	}
	payload, err := EncodeTaskBatch(tasks)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		c.mu.Lock()
		c.outstanding[task.TaskID] = nodeID
		c.mu.Unlock()
	}
	return session.sendFrame(MessageTypeTaskBatch, 0, payload)
}

func (c *ControllerService) markTaskResult(taskID uint64, nodeID string) {
	c.mu.Lock()
	delete(c.outstanding, taskID)
	if len(c.outstanding) > c.maxQueueDepth {
		c.backpressure[nodeID] = true
	}
	c.mu.Unlock()
}

func (c *ControllerService) Start() error {
	listener, err := net.Listen("tcp", c.addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	fmt.Printf("Nodren Controller started on %s\n", c.addr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go c.handleConnection(conn)
	}
}

func (c *ControllerService) handleConnection(conn net.Conn) {
	session := newNodeSession(conn)
	go session.writeLoop()
	defer conn.Close()

	for {
		if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
			return
		}
		frameBytes, err := readFrameFromConn(conn)
		if err != nil {
			return
		}
		frame, err := DecodeFrame(frameBytes)
		if err != nil {
			if errWrite := session.sendFrame(MessageTypeError, 0, []byte("invalid frame")); errWrite != nil {
				return
			}
			return
		}

		switch frame.Type {
		case MessageTypeRegister:
			var info NodeInfo
			if err := json.Unmarshal(frame.Payload, &info); err != nil {
				_ = session.sendFrame(MessageTypeError, frame.RequestID, []byte("register payload invalid"))
				return
			}
			info.Status = NodeStateReady.String()
			c.Registry.Add(&RegistryNode{
				ID:           info.NodeID,
				Hostname:     info.Hostname,
				CPUThreads:   info.CPUThreads,
				RAMGB:        info.RAMGB,
				OS:           info.OS,
				Architecture: info.Arch,
				Status:       NodeStateReady,
			})
			c.mu.Lock()
			c.sessions[info.NodeID] = session
			c.mu.Unlock()
			session.nodeID = info.NodeID
			session.state = string(NodeStateReady)
			fmt.Printf("[+] %s connected\n", info.NodeID)
			_ = session.sendFrame(MessageTypeRegisterAck, frame.RequestID, []byte("registered"))
			_ = session.sendFrame(MessageTypeReady, frame.RequestID+1, []byte("ready"))
		case MessageTypeHeartbeat:
			if session.nodeID == "" {
				session.nodeID = string(frame.Payload)
			}
			c.Registry.UpdateHeartbeat(session.nodeID)
			_ = session.sendFrame(MessageTypeHeartbeatAck, frame.RequestID, []byte("ok"))
		case MessageTypeTaskResultBatch:
			results, err := DecodeTaskResultBatch(frame.Payload)
			if err != nil {
				_ = session.sendFrame(MessageTypeError, frame.RequestID, []byte(err.Error()))
				return
			}
			for _, result := range results {
				fmt.Printf("[result] node=%s task=%d job=%s value=%d\n", result.NodeID, result.TaskID, result.JobID, result.Result)
				c.markTaskResult(result.TaskID, result.NodeID)
			}
			_ = session.sendFrame(MessageTypeReady, frame.RequestID+1, []byte("ready"))
		case MessageTypeHeartbeatAck:
			session.state = string(NodeStateReady)
		case MessageTypeGoodbye:
			return
		default:
			_ = session.sendFrame(MessageTypeError, frame.RequestID, []byte("unsupported message type"))
		}
	}
}

func (n NodeState) String() string {
	return string(n)
}

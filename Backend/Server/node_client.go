package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

type NodeClient struct {
	ID         string
	Hostname   string
	CPUThreads int
	RAMGB      int
	OS         string
	Arch       string
	conn       net.Conn
	outbound   *BoundedQueue
}

func NewNodeClient() *NodeClient {
	hostname, _ := os.Hostname()
	return &NodeClient{
		ID:         "NODE-" + fmt.Sprintf("%03d", time.Now().UnixNano()%1000),
		Hostname:   hostname,
		CPUThreads: 8,
		RAMGB:      16,
		OS:         runtimeOS(),
		Arch:       runtimeArch(),
		outbound:   NewBoundedQueue(256),
	}
}

func runtimeOS() string {
	return "Windows"
}

func runtimeArch() string {
	return "amd64"
}

func (n *NodeClient) sendFrame(msgType MessageType, requestID uint64, payload []byte) error {
	frame, err := EncodeFrame(msgType, requestID, payload)
	if err != nil {
		return err
	}
	return n.outbound.Push(frame)
}

func (n *NodeClient) writeLoop(conn net.Conn) {
	for {
		payload, ok := n.outbound.Pop()
		if !ok {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		if _, err := conn.Write(payload); err != nil {
			return
		}
	}
}

func readFrame(reader *bufio.Reader) ([]byte, error) {
	header := make([]byte, binaryFrameHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	size := int(binary.LittleEndian.Uint32(header[2:6]))
	body := make([]byte, size)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	frame := make([]byte, 0, binaryFrameHeaderSize+size)
	frame = append(frame, header...)
	frame = append(frame, body...)
	return frame, nil
}

func (n *NodeClient) StartController(addr string) error {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}
	n.conn = conn
	defer conn.Close()

	go n.writeLoop(conn)

	payload, _ := json.Marshal(NodeInfo{
		NodeID:     n.ID,
		Hostname:   n.Hostname,
		CPUThreads: n.CPUThreads,
		RAMGB:      n.RAMGB,
		OS:         n.OS,
		Arch:       n.Arch,
		Status:     "READY",
	})
	if err := n.sendFrame(MessageTypeRegister, 1, payload); err != nil {
		return err
	}

	reader := bufio.NewReader(conn)
	for {
		frameBytes, err := readFrame(reader)
		if err != nil {
			return err
		}
		frame, err := DecodeFrame(frameBytes)
		if err != nil {
			return err
		}
		switch frame.Type {
		case MessageTypeRegisterAck:
			fmt.Println("[node] registered")
		case MessageTypeHeartbeat:
			if err := n.sendFrame(MessageTypeHeartbeatAck, frame.RequestID, []byte(n.ID)); err != nil {
				return err
			}
		case MessageTypeReady:
			fmt.Println("[node] controller ready")
		case MessageTypeTaskBatch:
			tasks, err := DecodeTaskBatch(frame.Payload)
			if err != nil {
				return err
			}
			results, err := ExecuteNativeTaskBatch(tasks)
			if err != nil {
				return err
			}
			for i := range results {
				results[i].NodeID = n.ID
				if results[i].JobID == "" {
					results[i].JobID = fmt.Sprintf("job-%d", i)
				}
			}
			batch, err := EncodeTaskResultBatch(results)
			if err != nil {
				return err
			}
			if err := n.sendFrame(MessageTypeTaskResultBatch, frame.RequestID, batch); err != nil {
				return err
			}
		case MessageTypeGoodbye:
			return nil
		case MessageTypeError:
			return fmt.Errorf("controller error: %s", string(frame.Payload))
		}
	}
}

func (n *NodeClient) heartbeatLoop() {
	for {
		time.Sleep(5 * time.Second)
		if err := n.sendFrame(MessageTypeHeartbeat, uint64(time.Now().UnixNano()), []byte(n.ID)); err != nil {
			return
		}
	}
}

func runNodeClient() {
	client := NewNodeClient()
	fmt.Printf("Nodren Node: %s\n", client.ID)
	go client.heartbeatLoop()
	if err := client.StartController("127.0.0.1:8080"); err != nil {
		fmt.Println("node connection failed:", err)
	}
}

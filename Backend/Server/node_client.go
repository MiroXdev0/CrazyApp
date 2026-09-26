package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
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
	}
}

func runtimeOS() string {
	return "Windows"
}

func runtimeArch() string {
	return "amd64"
}

func (n *NodeClient) StartController(addr string) error {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}
	n.conn = conn
	defer conn.Close()

	payload, _ := SerializeNodeInfo(NodeInfo{
		NodeID:     n.ID,
		Hostname:   n.Hostname,
		CPUThreads: n.CPUThreads,
		RAMGB:      n.RAMGB,
		OS:         n.OS,
		Arch:       n.Arch,
		Status:     "READY",
	})
	registered, _ := EncodeMessage(MsgRegister, payload)
	if _, err = conn.Write([]byte(registered + "\n")); err != nil {
		return err
	}

	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "HEARTBEAT_ACK" {
			fmt.Println("[node] heartbeat ack")
		}
		if line == "REGISTER_ACK" {
			fmt.Println("[node] registered")
		}
		if line == "DISCONNECT_ACK" {
			return nil
		}

		if strings.Contains(line, "COMMAND") {
			_ = conn.Write([]byte("COMMAND_RESULT\n"))
		}

		// Simple heartbeat loop
		if line == "" {
			_ = conn.Write([]byte("HEARTBEAT\n"))
		}
		if strings.Contains(line, "HEARTBEAT") {
			_ = conn.Write([]byte("HEARTBEAT\n"))
		}
		if strings.Contains(line, "PING") {
			_ = conn.Write([]byte("PONG\n"))
		}
		if strings.Contains(line, "COMMAND_RESULT") {
			return nil
		}
	}
}

func main() {
	client := NewNodeClient()
	fmt.Printf("CrazyApp Node: %s\n", client.ID)
	if err := client.StartController("127.0.0.1:8080"); err != nil {
		fmt.Println("node connection failed:", err)
	}
}

package main

import (
	"fmt"
	"net"
	"time"
)

type ControllerService struct {
	Registry *NodeRegistry
	addr     string
}

func NewControllerService(addr string) *ControllerService {
	return &ControllerService{
		Registry: NewNodeRegistry(),
		addr:     addr,
	}
}

func (c *ControllerService) Start() error {
	listener, err := net.Listen("tcp", c.addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	fmt.Printf("CrazyApp Controller started on %s\n", c.addr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}

		go c.handleConnection(conn)
	}
}

func (c *ControllerService) handleConnection(conn net.Conn) {
	defer conn.Close()

	buf := make([]byte, 4096)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
			return
		}
		n, err := conn.Read(buf)
		if err != nil {
			return
		}

		message := string(buf[:n])
		if len(message) == 0 {
			continue
		}

		msg, err := DecodeMessage(message)
		if err != nil {
			continue
		}

		switch msg.Type {
		case MsgRegister:
			info, err := ParseNodeInfo(string(msg.Payload))
			if err != nil {
				continue
			}
			c.Registry.Add(&RegistryNode{
				ID:           info.NodeID,
				Hostname:     info.Hostname,
				CPUThreads:   info.CPUThreads,
				RAMGB:        info.RAMGB,
				OS:           info.OS,
				Architecture: info.Arch,
				Status:       NodeStateReady,
			})
			fmt.Printf("[+] %s connected\n", info.NodeID)
			fmt.Printf("%d nodes online\n", c.Registry.Count())
			_, _ = conn.Write([]byte("REGISTER_ACK\n"))
		case MsgHeartbeat:
			if hb, err := ParseNodeInfo(string(msg.Payload)); err == nil {
				c.Registry.UpdateHeartbeat(hb.NodeID)
				_, _ = conn.Write([]byte("HEARTBEAT_ACK\n"))
			}
		case MsgDisconnect:
			_ = conn.Write([]byte("DISCONNECT_ACK\n"))
			return
		}
	}
}

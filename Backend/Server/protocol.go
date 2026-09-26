package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	MsgRegister     = "REGISTER"
	MsgRegisterAck  = "REGISTER_ACK"
	MsgHeartbeat    = "HEARTBEAT"
	MsgHeartbeatAck = "HEARTBEAT_ACK"
	MsgStatus       = "NODE_STATUS"
	MsgCommand      = "COMMAND"
	MsgCommandRes   = "COMMAND_RESULT"
	MsgDisconnect   = "DISCONNECT"
	MsgError        = "ERROR"
)

type ProtocolMessage struct {
	Type      string          `json:"type"`
	NodeID    string          `json:"node_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Timestamp int64           `json:"timestamp,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type NodeInfo struct {
	NodeID     string `json:"node_id"`
	Hostname   string `json:"hostname"`
	CPUThreads int    `json:"cpu_threads"`
	RAMGB      int    `json:"ram_gb"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	Status     string `json:"status"`
}

type Heartbeat struct {
	NodeID string `json:"node_id"`
}

func EncodeMessage(msgType string, payload any) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	frame := ProtocolMessage{
		Type:      msgType,
		Payload:   b,
		Timestamp: 0,
	}

	out, err := json.Marshal(frame)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func DecodeMessage(raw string) (ProtocolMessage, error) {
	var msg ProtocolMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		return msg, err
	}
	return msg, nil
}

func ParseNodeInfo(raw string) (NodeInfo, error) {
	var info NodeInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return NodeInfo{}, err
	}
	return info, nil
}

func SerializeNodeInfo(info NodeInfo) (string, error) {
	b, err := json.Marshal(info)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func FormatStatusLine(node NodeInfo) string {
	return fmt.Sprintf("%s | %s | %s | %d threads | %d GB | %s",
		node.NodeID,
		node.Hostname,
		node.OS,
		node.CPUThreads,
		node.RAMGB,
		node.Status,
	)
}

func IsCommandMessage(msg string) bool {
	return strings.Contains(msg, MsgCommand)
}

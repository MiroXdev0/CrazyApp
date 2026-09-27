package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	protocolMagic   uint32 = 0x4E44524E // "NDRN"
	protocolVersion uint16 = 1
	frameHeaderSize        = 20
	maxFrameSize    uint32 = 16 << 20
	maxBatchTasks   uint32 = 4096
	maxStringSize   uint32 = 1 << 20
)

type MessageType uint16

const (
	MsgHello MessageType = iota + 1
	MsgRegister
	MsgRegisterAck
	MsgHeartbeat
	MsgHeartbeatAck
	MsgTaskBatch
	MsgTaskResultBatch
	MsgError
	MsgGoodbye
	MsgReady
)

type ResourceRequirements struct {
	CPUCores    uint32
	RAMGB       uint64
	GPURequired bool
}

type GPUInfo struct {
	Vendor string
	Model  string
	VRAMGB uint64
}

type NodeInfo struct {
	ID       string
	Hostname string
	OS       string
	Arch     string
	CPUCores uint32
	RAMGB    uint64
	GPU      GPUInfo
}

type Task struct {
	ID           uint64
	JobID        string
	Command      string
	Priority     uint8
	Requirements ResourceRequirements
	Payload      []byte
}

type TaskResult struct {
	TaskID     uint64
	JobID      string
	Status     string
	Value      int64
	Error      string
	DurationUS uint64
	NodeID     string
}

type frame struct {
	Type      MessageType
	RequestID uint64
	Payload   []byte
}

func writeString(buf *bytes.Buffer, s string) error {
	if uint64(len(s)) > uint64(maxStringSize) {
		return fmt.Errorf("string too large: %d", len(s))
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(s))); err != nil {
		return err
	}
	_, err := buf.WriteString(s)
	return err
}

func readString(data []byte, cursor *int) (string, error) {
	if *cursor+4 > len(data) {
		return "", io.ErrUnexpectedEOF
	}
	n := binary.LittleEndian.Uint32(data[*cursor : *cursor+4])
	*cursor += 4
	if n > maxStringSize || int(n) > len(data)-*cursor {
		return "", fmt.Errorf("invalid string length: %d", n)
	}
	s := string(data[*cursor : *cursor+int(n)])
	*cursor += int(n)
	return s, nil
}

func writeFrame(w io.Writer, typ MessageType, requestID uint64, payload []byte) error {
	if uint64(len(payload)) > uint64(maxFrameSize) {
		return fmt.Errorf("frame payload too large: %d", len(payload))
	}

	header := make([]byte, frameHeaderSize)
	binary.LittleEndian.PutUint32(header[0:4], protocolMagic)
	binary.LittleEndian.PutUint16(header[4:6], protocolVersion)
	binary.LittleEndian.PutUint16(header[6:8], uint16(typ))
	binary.LittleEndian.PutUint64(header[8:16], requestID)
	binary.LittleEndian.PutUint32(header[16:20], uint32(len(payload)))

	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader) (frame, error) {
	header := make([]byte, frameHeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return frame{}, err
	}

	if binary.LittleEndian.Uint32(header[0:4]) != protocolMagic {
		return frame{}, errors.New("invalid nodren protocol magic")
	}
	if binary.LittleEndian.Uint16(header[4:6]) != protocolVersion {
		return frame{}, errors.New("unsupported nodren protocol version")
	}

	payloadLen := binary.LittleEndian.Uint32(header[16:20])
	if payloadLen > maxFrameSize {
		return frame{}, fmt.Errorf("frame exceeds limit: %d", payloadLen)
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return frame{}, err
	}

	return frame{
		Type:      MessageType(binary.LittleEndian.Uint16(header[6:8])),
		RequestID: binary.LittleEndian.Uint64(header[8:16]),
		Payload:   payload,
	}, nil
}

func encodeRegister(info NodeInfo) ([]byte, error) {
	var b bytes.Buffer
	for _, s := range []string{info.ID, info.Hostname, info.OS, info.Arch, info.GPU.Vendor, info.GPU.Model} {
		if err := writeString(&b, s); err != nil {
			return nil, err
		}
	}
	fields := []uint64{
		uint64(info.CPUCores),
		info.RAMGB,
		info.GPU.VRAMGB,
	}
	for _, v := range fields {
		if err := binary.Write(&b, binary.LittleEndian, v); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

func decodeRegister(data []byte) (NodeInfo, error) {
	cursor := 0
	values := make([]string, 6)
	for i := range values {
		s, err := readString(data, &cursor)
		if err != nil {
			return NodeInfo{}, err
		}
		values[i] = s
	}

	required := 8 * 3
	if cursor+required > len(data) {
		return NodeInfo{}, io.ErrUnexpectedEOF
	}
	info := NodeInfo{
		ID:       values[0],
		Hostname: values[1],
		OS:       values[2],
		Arch:     values[3],
		GPU: GPUInfo{
			Vendor: values[4],
			Model:  values[5],
		},
	}
	info.CPUCores = uint32(binary.LittleEndian.Uint64(data[cursor : cursor+8]))
	cursor += 8
	info.RAMGB = binary.LittleEndian.Uint64(data[cursor : cursor+8])
	cursor += 8
	info.GPU.VRAMGB = binary.LittleEndian.Uint64(data[cursor : cursor+8])

	if info.ID == "" {
		return NodeInfo{}, errors.New("node id is required")
	}
	return info, nil
}

func encodeTaskBatch(tasks []Task) ([]byte, error) {
	if len(tasks) == 0 || len(tasks) > int(maxBatchTasks) {
		return nil, fmt.Errorf("invalid task batch size: %d", len(tasks))
	}
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(tasks)))

	for _, t := range tasks {
		if err := binary.Write(&b, binary.LittleEndian, t.ID); err != nil {
			return nil, err
		}
		if err := writeString(&b, t.JobID); err != nil {
			return nil, err
		}
		if err := writeString(&b, t.Command); err != nil {
			return nil, err
		}
		_ = b.WriteByte(t.Priority)
		_ = binary.Write(&b, binary.LittleEndian, t.Requirements.CPUCores)
		_ = binary.Write(&b, binary.LittleEndian, t.Requirements.RAMGB)
		if t.Requirements.GPURequired {
			_ = b.WriteByte(1)
		} else {
			_ = b.WriteByte(0)
		}
		if uint64(len(t.Payload)) > uint64(maxFrameSize) {
			return nil, fmt.Errorf("task payload too large")
		}
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(t.Payload)))
		_, _ = b.Write(t.Payload)
	}
	return b.Bytes(), nil
}

func decodeTaskResultBatch(data []byte) ([]TaskResult, error) {
	if len(data) < 4 {
		return nil, io.ErrUnexpectedEOF
	}
	count := binary.LittleEndian.Uint32(data[:4])
	if count > maxBatchTasks {
		return nil, fmt.Errorf("result batch too large: %d", count)
	}
	cursor := 4
	results := make([]TaskResult, 0, count)

	for i := uint32(0); i < count; i++ {
		if cursor+8 > len(data) {
			return nil, io.ErrUnexpectedEOF
		}
		taskID := binary.LittleEndian.Uint64(data[cursor : cursor+8])
		cursor += 8

		jobID, err := readString(data, &cursor)
		if err != nil {
			return nil, err
		}
		status, err := readString(data, &cursor)
		if err != nil {
			return nil, err
		}

		if cursor+8 > len(data) {
			return nil, io.ErrUnexpectedEOF
		}
		value := int64(binary.LittleEndian.Uint64(data[cursor : cursor+8]))
		cursor += 8

		errText, err := readString(data, &cursor)
		if err != nil {
			return nil, err
		}

		if cursor+8 > len(data) {
			return nil, io.ErrUnexpectedEOF
		}
		durationUS := binary.LittleEndian.Uint64(data[cursor : cursor+8])
		cursor += 8

		nodeID, err := readString(data, &cursor)
		if err != nil {
			return nil, err
		}

		results = append(results, TaskResult{
			TaskID:     taskID,
			JobID:      jobID,
			Status:     status,
			Value:      value,
			Error:      errText,
			DurationUS: durationUS,
			NodeID:     nodeID,
		})
	}
	return results, nil
}

func encodeHeartbeat(unixMilli int64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, uint64(unixMilli))
	return b
}

func decodeHeartbeat(data []byte) (int64, error) {
	if len(data) != 8 {
		return 0, errors.New("invalid heartbeat payload")
	}
	return int64(binary.LittleEndian.Uint64(data)), nil
}

func encodeError(message string) []byte {
	var b bytes.Buffer
	_ = writeString(&b, message)
	return b.Bytes()
}

func _mathGuard(v int) bool { return v >= 0 && v <= math.MaxInt32 }

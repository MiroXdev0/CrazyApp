package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"time"
)

const (
	// Little-endian encoding of the ASCII bytes "NDRN".
	protocolMagic   uint32 = 0x4E52444E
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
	MsgTaskSubmit
	MsgTaskAck
	MsgTaskCancel
	MsgTaskState
	MsgTaskResult
	MsgArtifactBegin
	MsgArtifactChunk
	MsgArtifactEnd
	MsgCapabilities
)

type ResourceRequirements struct {
	CPUCores        uint32   `json:"cpu_cores"`
	RAMGB           uint64   `json:"ram_gb"`
	MaxRAMGB        uint64   `json:"max_ram_gb,omitempty"`
	GPURequired     bool     `json:"gpu_required"`
	GPUCount        uint32   `json:"gpu_count,omitempty"`
	VRAMGB          uint64   `json:"vram_gb,omitempty"`
	AcceleratorType string   `json:"accelerator_type,omitempty"`
	GPUCapabilities []string `json:"gpu_capabilities,omitempty"`
}

type GPUInfo struct {
	Vendor       string   `json:"vendor"`
	Model        string   `json:"model"`
	VRAMGB       uint64   `json:"vram_gb"`
	Count        uint32   `json:"count,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Driver       string   `json:"driver,omitempty"`
	Runtime      string   `json:"runtime,omitempty"`
}

type NodeInfo struct {
	ID             string   `json:"id"`
	Hostname       string   `json:"hostname"`
	OS             string   `json:"os"`
	Arch           string   `json:"arch"`
	CPUModel       string   `json:"cpu_model,omitempty"`
	CPUCores       uint32   `json:"cpu_cores"`
	RAMGB          uint64   `json:"ram_gb"`
	GPU            GPUInfo  `json:"gpu"`
	Runtimes       []string `json:"runtimes,omitempty"`
	ExecutionTypes []string `json:"execution_types,omitempty"`
	Capabilities   []string `json:"capabilities,omitempty"`
}

// WorkerTelemetry contains measurements reported by the worker process. A
// negative utilization value means the platform could not provide that
// measurement; schedulers must then fall back to advertised capacity.
type WorkerTelemetry struct {
	Timestamp                time.Time `json:"timestamp"`
	UptimeSeconds            uint64    `json:"uptime_seconds"`
	ActiveTasks              uint32    `json:"active_tasks"`
	CPUUtilizationPercent    float64   `json:"cpu_utilization_percent"`
	MemoryAvailableGB        uint64    `json:"memory_available_gb"`
	MemoryUtilizationPercent float64   `json:"memory_utilization_percent"`
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
	TaskID     uint64 `json:"task_id"`
	JobID      string `json:"job_id"`
	Status     string `json:"status"`
	Value      int64  `json:"value"`
	ErrorCode  string `json:"error_code,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationUS uint64 `json:"duration_us"`
	NodeID     string `json:"node_id"`
}

type GeneralTaskEnvelope struct {
	TaskID  uint64
	JobID   string
	Attempt uint32
	Spec    GeneralTaskSpec
}

type GeneralTaskResultEnvelope struct {
	TaskID          uint64
	JobID           string
	Attempt         uint32
	Status          string
	ExitCode        *int32
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
	DurationUS      uint64
	ErrorCode       string
	Error           string
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
	for _, s := range []string{info.ID, info.Hostname, info.OS, info.Arch, info.GPU.Vendor, info.GPU.Model, info.CPUModel} {
		if err := writeString(&b, s); err != nil {
			return nil, err
		}
	}
	for _, values := range [][]string{info.Runtimes, info.ExecutionTypes, info.Capabilities} {
		if uint64(len(values)) > uint64(maxBatchTasks) {
			return nil, errors.New("too many worker capabilities")
		}
		if err := binary.Write(&b, binary.LittleEndian, uint32(len(values))); err != nil {
			return nil, err
		}
		for _, value := range values {
			if err := writeString(&b, value); err != nil {
				return nil, err
			}
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
	if err := binary.Write(&b, binary.LittleEndian, uint64(info.GPU.Count)); err != nil {
		return nil, err
	}
	if err := writeStringArray(&b, info.GPU.Capabilities); err != nil {
		return nil, err
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
	// CPUModel was appended after the original registration fields. A legacy
	// worker has exactly 24 bytes left for the three numeric fields.
	if len(data)-cursor > required {
		model, err := readString(data, &cursor)
		if err != nil {
			return NodeInfo{}, err
		}
		info.CPUModel = model
		if len(data)-cursor > required {
			arrays := []*[]string{&info.Runtimes, &info.ExecutionTypes, &info.Capabilities}
			for _, values := range arrays {
				if cursor+4 > len(data) {
					return NodeInfo{}, io.ErrUnexpectedEOF
				}
				count := binary.LittleEndian.Uint32(data[cursor : cursor+4])
				cursor += 4
				if count > maxBatchTasks {
					return NodeInfo{}, errors.New("too many worker capabilities")
				}
				*values = make([]string, 0, count)
				for index := uint32(0); index < count; index++ {
					value, err := readString(data, &cursor)
					if err != nil {
						return NodeInfo{}, err
					}
					*values = append(*values, value)
				}
			}
		}
	}
	info.CPUCores = uint32(binary.LittleEndian.Uint64(data[cursor : cursor+8]))
	cursor += 8
	info.RAMGB = binary.LittleEndian.Uint64(data[cursor : cursor+8])
	cursor += 8
	info.GPU.VRAMGB = binary.LittleEndian.Uint64(data[cursor : cursor+8])
	cursor += 8
	if cursor+8 <= len(data) {
		info.GPU.Count = uint32(binary.LittleEndian.Uint64(data[cursor : cursor+8]))
		cursor += 8
		if cursor < len(data) {
			capabilities, err := readStringArray(data, &cursor)
			if err != nil {
				return NodeInfo{}, err
			}
			info.GPU.Capabilities = capabilities
		}
	}

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
		errorCode := ""
		if cursor < len(data) {
			errorCode, err = readString(data, &cursor)
			if err != nil {
				return nil, err
			}
		}

		results = append(results, TaskResult{
			TaskID:     taskID,
			JobID:      jobID,
			Status:     status,
			Value:      value,
			ErrorCode:  errorCode,
			Error:      errText,
			DurationUS: durationUS,
			NodeID:     nodeID,
		})
	}
	return results, nil
}

func writeStringArray(b *bytes.Buffer, values []string) error {
	if len(values) > int(maxBatchTasks) {
		return errors.New("array is too large")
	}
	if err := binary.Write(b, binary.LittleEndian, uint32(len(values))); err != nil {
		return err
	}
	for _, value := range values {
		if err := writeString(b, value); err != nil {
			return err
		}
	}
	return nil
}

func readStringArray(data []byte, cursor *int) ([]string, error) {
	if *cursor+4 > len(data) {
		return nil, io.ErrUnexpectedEOF
	}
	count := binary.LittleEndian.Uint32(data[*cursor : *cursor+4])
	*cursor += 4
	if count > maxBatchTasks {
		return nil, errors.New("array is too large")
	}
	values := make([]string, 0, count)
	for index := uint32(0); index < count; index++ {
		value, err := readString(data, cursor)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func writeBytes(b *bytes.Buffer, value []byte) error {
	if uint64(len(value)) > uint64(maxFrameSize) {
		return errors.New("byte field is too large")
	}
	if err := binary.Write(b, binary.LittleEndian, uint32(len(value))); err != nil {
		return err
	}
	_, err := b.Write(value)
	return err
}

func readBytes(data []byte, cursor *int) ([]byte, error) {
	if *cursor+4 > len(data) {
		return nil, io.ErrUnexpectedEOF
	}
	length := binary.LittleEndian.Uint32(data[*cursor : *cursor+4])
	*cursor += 4
	if length > maxFrameSize || int(length) > len(data)-*cursor {
		return nil, errors.New("invalid byte field length")
	}
	value := append([]byte(nil), data[*cursor:*cursor+int(length)]...)
	*cursor += int(length)
	return value, nil
}

func encodeGeneralTask(envelope GeneralTaskEnvelope) ([]byte, error) {
	var b bytes.Buffer
	if err := binary.Write(&b, binary.LittleEndian, envelope.TaskID); err != nil {
		return nil, err
	}
	if err := writeString(&b, envelope.JobID); err != nil {
		return nil, err
	}
	if err := binary.Write(&b, binary.LittleEndian, envelope.Attempt); err != nil {
		return nil, err
	}
	spec := defaultTaskSpec(envelope.Spec)
	if err := validateGeneralTask(spec); err != nil {
		return nil, err
	}
	for _, value := range []string{string(spec.Type), spec.Version, spec.Executable, spec.Runtime, spec.Script, spec.Workload, spec.WorkingDirectory} {
		if err := writeString(&b, value); err != nil {
			return nil, err
		}
	}
	if err := writeStringArray(&b, spec.Arguments); err != nil {
		return nil, err
	}
	environment := make([]string, 0, len(spec.Environment)*2)
	keys := make([]string, 0, len(spec.Environment))
	for key := range spec.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key, spec.Environment[key])
	}
	if err := writeStringArray(&b, environment); err != nil {
		return nil, err
	}
	if err := base64Bytes(spec.StdinB64, &b); err != nil {
		return nil, err
	}
	for _, value := range []uint64{spec.TimeoutMS, spec.StdoutLimitBytes, spec.StderrLimitBytes, spec.Requirements.RAMGB, spec.Requirements.MaxRAMGB} {
		if err := binary.Write(&b, binary.LittleEndian, value); err != nil {
			return nil, err
		}
	}
	if err := binary.Write(&b, binary.LittleEndian, spec.Requirements.CPUCores); err != nil {
		return nil, err
	}
	if spec.Requirements.GPURequired {
		_ = b.WriteByte(1)
	} else {
		_ = b.WriteByte(0)
	}
	if err := binary.Write(&b, binary.LittleEndian, spec.Requirements.GPUCount); err != nil {
		return nil, err
	}
	if err := binary.Write(&b, binary.LittleEndian, spec.Requirements.VRAMGB); err != nil {
		return nil, err
	}
	if err := writeString(&b, spec.Requirements.AcceleratorType); err != nil {
		return nil, err
	}
	if err := writeStringArray(&b, spec.Requirements.GPUCapabilities); err != nil {
		return nil, err
	}
	for _, value := range []string{spec.Target.OS, spec.Target.Arch, spec.Target.PreferredWorkerID} {
		if err := writeString(&b, value); err != nil {
			return nil, err
		}
	}
	for _, values := range [][]string{spec.Target.RequiredRuntimes, spec.Target.RequiredCapabilities, spec.Target.AllowedWorkerIDs} {
		if err := writeStringArray(&b, values); err != nil {
			return nil, err
		}
	}
	for _, artifacts := range [][]TaskArtifact{spec.InputArtifacts, spec.OutputArtifacts} {
		if len(artifacts) > int(maxBatchTasks) {
			return nil, errors.New("too many task artifacts")
		}
		if err := binary.Write(&b, binary.LittleEndian, uint32(len(artifacts))); err != nil {
			return nil, err
		}
		for _, artifact := range artifacts {
			for _, value := range []string{artifact.ID, artifact.Name, artifact.SHA256, artifact.Kind} {
				if err := writeString(&b, value); err != nil {
					return nil, err
				}
			}
			if err := binary.Write(&b, binary.LittleEndian, artifact.Size); err != nil {
				return nil, err
			}
		}
	}
	for _, value := range []string{spec.WorkloadKind, string(spec.Strategy)} {
		if err := writeString(&b, value); err != nil {
			return nil, err
		}
	}
	for _, value := range []uint32{spec.RequiredWorkers, spec.Replicas} {
		if err := binary.Write(&b, binary.LittleEndian, value); err != nil {
			return nil, err
		}
	}
	if spec.PackageManifest == nil {
		_ = b.WriteByte(0)
	} else {
		_ = b.WriteByte(1)
		manifest := spec.PackageManifest
		for _, value := range []string{manifest.EntryPoint, manifest.Runtime, manifest.OS, manifest.Arch} {
			if err := writeString(&b, value); err != nil {
				return nil, err
			}
		}
		if err := writeStringArray(&b, manifest.Arguments); err != nil {
			return nil, err
		}
		if err := writeStringArray(&b, manifest.RequiredRuntimes); err != nil {
			return nil, err
		}
		environment := make([]string, 0, len(manifest.Environment)*2)
		keys := make([]string, 0, len(manifest.Environment))
		for key := range manifest.Environment {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			environment = append(environment, key, manifest.Environment[key])
		}
		if err := writeStringArray(&b, environment); err != nil {
			return nil, err
		}
	}
	if err := binary.Write(&b, binary.LittleEndian, spec.Retry.MaxRetries); err != nil {
		return nil, err
	}
	if len(b.Bytes()) > int(maxFrameSize) {
		return nil, errors.New("general task payload is too large")
	}
	return b.Bytes(), nil
}

func base64Bytes(value string, b *bytes.Buffer) error {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return fmt.Errorf("invalid stdin base64: %w", err)
	}
	return writeBytes(b, decoded)
}

func decodeGeneralTask(data []byte) (GeneralTaskEnvelope, error) {
	var envelope GeneralTaskEnvelope
	cursor := 0
	if cursor+8 > len(data) {
		return envelope, io.ErrUnexpectedEOF
	}
	envelope.TaskID = binary.LittleEndian.Uint64(data[cursor : cursor+8])
	cursor += 8
	var err error
	if envelope.JobID, err = readString(data, &cursor); err != nil {
		return envelope, err
	}
	if cursor+4 > len(data) {
		return envelope, io.ErrUnexpectedEOF
	}
	envelope.Attempt = binary.LittleEndian.Uint32(data[cursor : cursor+4])
	cursor += 4
	values := make([]string, 7)
	for index := range values {
		values[index], err = readString(data, &cursor)
		if err != nil {
			return envelope, err
		}
	}
	envelope.Spec = GeneralTaskSpec{Type: TaskType(values[0]), Version: values[1], Executable: values[2], Runtime: values[3], Script: values[4], Workload: values[5], WorkingDirectory: values[6]}
	if envelope.Spec.Arguments, err = readStringArray(data, &cursor); err != nil {
		return envelope, err
	}
	environment, err := readStringArray(data, &cursor)
	if err != nil || len(environment)%2 != 0 {
		return envelope, errors.New("invalid environment encoding")
	}
	envelope.Spec.Environment = make(map[string]string, len(environment)/2)
	for index := 0; index < len(environment); index += 2 {
		envelope.Spec.Environment[environment[index]] = environment[index+1]
	}
	stdin, err := readBytes(data, &cursor)
	if err != nil {
		return envelope, err
	}
	envelope.Spec.StdinB64 = base64.StdEncoding.EncodeToString(stdin)
	if cursor+8*5+4+1 > len(data) {
		return envelope, io.ErrUnexpectedEOF
	}
	envelope.Spec.TimeoutMS = binary.LittleEndian.Uint64(data[cursor : cursor+8])
	cursor += 8
	envelope.Spec.StdoutLimitBytes = binary.LittleEndian.Uint64(data[cursor : cursor+8])
	cursor += 8
	envelope.Spec.StderrLimitBytes = binary.LittleEndian.Uint64(data[cursor : cursor+8])
	cursor += 8
	envelope.Spec.Requirements.RAMGB = binary.LittleEndian.Uint64(data[cursor : cursor+8])
	cursor += 8
	envelope.Spec.Requirements.MaxRAMGB = binary.LittleEndian.Uint64(data[cursor : cursor+8])
	cursor += 8
	envelope.Spec.Requirements.CPUCores = binary.LittleEndian.Uint32(data[cursor : cursor+4])
	cursor += 4
	envelope.Spec.Requirements.GPURequired = data[cursor] != 0
	cursor++
	if cursor+4+8 > len(data) {
		return envelope, io.ErrUnexpectedEOF
	}
	envelope.Spec.Requirements.GPUCount = binary.LittleEndian.Uint32(data[cursor : cursor+4])
	cursor += 4
	envelope.Spec.Requirements.VRAMGB = binary.LittleEndian.Uint64(data[cursor : cursor+8])
	cursor += 8
	if envelope.Spec.Requirements.AcceleratorType, err = readString(data, &cursor); err != nil {
		return envelope, err
	}
	if envelope.Spec.Requirements.GPUCapabilities, err = readStringArray(data, &cursor); err != nil {
		return envelope, err
	}
	for index := range []string{envelope.Spec.Target.OS, envelope.Spec.Target.Arch, envelope.Spec.Target.PreferredWorkerID} {
		value, readErr := readString(data, &cursor)
		if readErr != nil {
			return envelope, readErr
		}
		switch index {
		case 0:
			envelope.Spec.Target.OS = value
		case 1:
			envelope.Spec.Target.Arch = value
		default:
			envelope.Spec.Target.PreferredWorkerID = value
		}
	}
	arrays := []*[]string{&envelope.Spec.Target.RequiredRuntimes, &envelope.Spec.Target.RequiredCapabilities, &envelope.Spec.Target.AllowedWorkerIDs}
	for _, values := range arrays {
		*values, err = readStringArray(data, &cursor)
		if err != nil {
			return envelope, err
		}
	}
	artifactLists := []*[]TaskArtifact{&envelope.Spec.InputArtifacts, &envelope.Spec.OutputArtifacts}
	for _, artifacts := range artifactLists {
		if cursor+4 > len(data) {
			return envelope, io.ErrUnexpectedEOF
		}
		count := binary.LittleEndian.Uint32(data[cursor : cursor+4])
		cursor += 4
		if count > maxBatchTasks {
			return envelope, errors.New("too many task artifacts")
		}
		*artifacts = make([]TaskArtifact, 0, count)
		for index := uint32(0); index < count; index++ {
			values := make([]string, 4)
			for valueIndex := range values {
				values[valueIndex], err = readString(data, &cursor)
				if err != nil {
					return envelope, err
				}
			}
			if cursor+8 > len(data) {
				return envelope, io.ErrUnexpectedEOF
			}
			size := binary.LittleEndian.Uint64(data[cursor : cursor+8])
			cursor += 8
			*artifacts = append(*artifacts, TaskArtifact{ID: values[0], Name: values[1], SHA256: values[2], Kind: values[3], Size: size})
		}
	}
	if envelope.Spec.WorkloadKind, err = readString(data, &cursor); err != nil {
		return envelope, err
	}
	strategy, readErr := readString(data, &cursor)
	if readErr != nil {
		return envelope, readErr
	}
	envelope.Spec.Strategy = ExecutionStrategy(strategy)
	if cursor+4+4+1 > len(data) {
		return envelope, io.ErrUnexpectedEOF
	}
	envelope.Spec.RequiredWorkers = binary.LittleEndian.Uint32(data[cursor : cursor+4])
	cursor += 4
	envelope.Spec.Replicas = binary.LittleEndian.Uint32(data[cursor : cursor+4])
	cursor += 4
	if data[cursor] != 0 {
		cursor++
		manifest := &TaskPackageManifest{}
		values := make([]string, 4)
		for index := range values {
			values[index], err = readString(data, &cursor)
			if err != nil {
				return envelope, err
			}
		}
		manifest.EntryPoint, manifest.Runtime, manifest.OS, manifest.Arch = values[0], values[1], values[2], values[3]
		if manifest.Arguments, err = readStringArray(data, &cursor); err != nil {
			return envelope, err
		}
		if manifest.RequiredRuntimes, err = readStringArray(data, &cursor); err != nil {
			return envelope, err
		}
		environment, readErr := readStringArray(data, &cursor)
		if readErr != nil || len(environment)%2 != 0 {
			return envelope, errors.New("invalid package manifest environment")
		}
		manifest.Environment = make(map[string]string, len(environment)/2)
		for index := 0; index < len(environment); index += 2 {
			manifest.Environment[environment[index]] = environment[index+1]
		}
		envelope.Spec.PackageManifest = manifest
	} else {
		cursor++
	}
	if cursor+4 != len(data) {
		return envelope, errors.New("invalid general task trailing data")
	}
	envelope.Spec.Retry.MaxRetries = binary.LittleEndian.Uint32(data[cursor : cursor+4])
	envelope.Spec = defaultTaskSpec(envelope.Spec)
	return envelope, validateGeneralTask(envelope.Spec)
}

func encodeGeneralTaskResult(result GeneralTaskResultEnvelope) ([]byte, error) {
	var b bytes.Buffer
	if err := binary.Write(&b, binary.LittleEndian, result.TaskID); err != nil {
		return nil, err
	}
	if err := writeString(&b, result.JobID); err != nil {
		return nil, err
	}
	if err := binary.Write(&b, binary.LittleEndian, result.Attempt); err != nil {
		return nil, err
	}
	for _, value := range []string{result.Status, result.ErrorCode, result.Error} {
		if err := writeString(&b, value); err != nil {
			return nil, err
		}
	}
	if result.ExitCode == nil {
		_ = b.WriteByte(0)
	} else {
		_ = b.WriteByte(1)
		_ = binary.Write(&b, binary.LittleEndian, *result.ExitCode)
	}
	if err := writeBytes(&b, result.Stdout); err != nil {
		return nil, err
	}
	if err := writeBytes(&b, result.Stderr); err != nil {
		return nil, err
	}
	_ = b.WriteByte(boolByte(result.StdoutTruncated))
	_ = b.WriteByte(boolByte(result.StderrTruncated))
	if err := binary.Write(&b, binary.LittleEndian, result.DurationUS); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func boolByte(value bool) byte {
	if value {
		return 1
	}
	return 0
}

func decodeGeneralTaskResult(data []byte) (GeneralTaskResultEnvelope, error) {
	var result GeneralTaskResultEnvelope
	cursor := 0
	if len(data) < 8 {
		return result, io.ErrUnexpectedEOF
	}
	result.TaskID = binary.LittleEndian.Uint64(data[:8])
	cursor = 8
	var err error
	if result.JobID, err = readString(data, &cursor); err != nil {
		return result, err
	}
	if cursor+4 > len(data) {
		return result, io.ErrUnexpectedEOF
	}
	result.Attempt = binary.LittleEndian.Uint32(data[cursor : cursor+4])
	cursor += 4
	if result.Status, err = readString(data, &cursor); err != nil {
		return result, err
	}
	if result.ErrorCode, err = readString(data, &cursor); err != nil {
		return result, err
	}
	if result.Error, err = readString(data, &cursor); err != nil {
		return result, err
	}
	if cursor >= len(data) {
		return result, io.ErrUnexpectedEOF
	}
	hasExit := data[cursor] != 0
	cursor++
	if hasExit {
		if cursor+4 > len(data) {
			return result, io.ErrUnexpectedEOF
		}
		value := int32(binary.LittleEndian.Uint32(data[cursor : cursor+4]))
		result.ExitCode = &value
		cursor += 4
	}
	if result.Stdout, err = readBytes(data, &cursor); err != nil {
		return result, err
	}
	if result.Stderr, err = readBytes(data, &cursor); err != nil {
		return result, err
	}
	if cursor+1+1+8 != len(data) {
		return result, errors.New("invalid general task result trailing data")
	}
	result.StdoutTruncated = data[cursor] != 0
	result.StderrTruncated = data[cursor+1] != 0
	cursor += 2
	result.DurationUS = binary.LittleEndian.Uint64(data[cursor : cursor+8])
	return result, nil
}

func encodeHeartbeat(unixMilli int64) []byte {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint64(b, uint64(unixMilli))
	// Controller replies do not carry measurements, but retain the same frame
	// shape so the worker can decode either direction consistently.
	binary.LittleEndian.PutUint64(b[8:16], 0)
	binary.LittleEndian.PutUint32(b[16:20], 0)
	binary.LittleEndian.PutUint32(b[20:24], math.MaxUint32)
	binary.LittleEndian.PutUint64(b[24:32], math.MaxUint64)
	return b
}

func encodeHeartbeatTelemetry(unixMilli int64, uptimeSeconds uint64, activeTasks uint32, cpuPercent, memoryAvailableGB float64) []byte {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint64(b, uint64(unixMilli))
	binary.LittleEndian.PutUint64(b[8:16], uptimeSeconds)
	binary.LittleEndian.PutUint32(b[16:20], activeTasks)
	if cpuPercent < 0 {
		binary.LittleEndian.PutUint32(b[20:24], math.MaxUint32)
	} else {
		binary.LittleEndian.PutUint32(b[20:24], uint32(math.Round(math.Max(0, math.Min(100, cpuPercent))*1000)))
	}
	if memoryAvailableGB < 0 {
		binary.LittleEndian.PutUint64(b[24:32], math.MaxUint64)
	} else {
		binary.LittleEndian.PutUint64(b[24:32], uint64(math.Max(0, memoryAvailableGB)*1024))
	}
	return b
}

func decodeHeartbeat(data []byte) (int64, WorkerTelemetry, error) {
	if len(data) != 8 && len(data) != 32 {
		return 0, WorkerTelemetry{}, errors.New("invalid heartbeat payload")
	}
	timestamp := int64(binary.LittleEndian.Uint64(data))
	telemetry := WorkerTelemetry{
		Timestamp:                time.UnixMilli(timestamp).UTC(),
		CPUUtilizationPercent:    -1,
		MemoryUtilizationPercent: -1,
	}
	if len(data) == 8 {
		return timestamp, telemetry, nil
	}
	telemetry.UptimeSeconds = binary.LittleEndian.Uint64(data[8:16])
	telemetry.ActiveTasks = binary.LittleEndian.Uint32(data[16:20])
	cpuMilli := binary.LittleEndian.Uint32(data[20:24])
	if cpuMilli != math.MaxUint32 {
		telemetry.CPUUtilizationPercent = float64(cpuMilli) / 1000
	}
	availableMB := binary.LittleEndian.Uint64(data[24:32])
	if availableMB != math.MaxUint64 {
		telemetry.MemoryAvailableGB = availableMB / 1024
	}
	return timestamp, telemetry, nil
}

func encodeError(message string) []byte {
	var b bytes.Buffer
	_ = writeString(&b, message)
	return b.Bytes()
}

func encodeTaskCancel(taskID uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, taskID)
	return b
}

func encodeArtifactBegin(taskID uint64, artifact TaskArtifact) ([]byte, error) {
	var b bytes.Buffer
	if err := binary.Write(&b, binary.LittleEndian, taskID); err != nil {
		return nil, err
	}
	for _, value := range []string{artifact.ID, artifact.Name, artifact.SHA256, artifact.Kind} {
		if err := writeString(&b, value); err != nil {
			return nil, err
		}
	}
	if err := binary.Write(&b, binary.LittleEndian, artifact.Size); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func encodeArtifactChunk(taskID uint64, artifactID string, offset uint64, data []byte) ([]byte, error) {
	var b bytes.Buffer
	if err := binary.Write(&b, binary.LittleEndian, taskID); err != nil {
		return nil, err
	}
	if err := writeString(&b, artifactID); err != nil {
		return nil, err
	}
	if err := binary.Write(&b, binary.LittleEndian, offset); err != nil {
		return nil, err
	}
	if err := writeBytes(&b, data); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func encodeArtifactEnd(taskID uint64, artifactID string) ([]byte, error) {
	var b bytes.Buffer
	if err := binary.Write(&b, binary.LittleEndian, taskID); err != nil {
		return nil, err
	}
	if err := writeString(&b, artifactID); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func _mathGuard(v int) bool { return v >= 0 && v <= math.MaxInt32 }

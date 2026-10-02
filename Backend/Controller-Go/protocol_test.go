package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	want := []byte("hello nodren")
	go func() {
		_ = writeFrame(left, MsgReady, 42, want)
	}()

	got, err := readFrame(right)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != MsgReady || got.RequestID != 42 || !bytes.Equal(got.Payload, want) {
		t.Fatalf("unexpected frame: %+v", got)
	}
}

func TestFrameUsesNDRNWireMagic(t *testing.T) {
	var encoded bytes.Buffer
	if err := writeFrame(&encoded, MsgReady, 1, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded.Bytes()[:4], []byte("NDRN")) {
		t.Fatalf("unexpected wire magic: %x", encoded.Bytes()[:4])
	}
}

func TestJobRequestUsesDocumentedJSONFields(t *testing.T) {
	var request jobRequest
	if err := json.Unmarshal([]byte(`{"command":"sum","requirements":{"cpu_cores":2,"ram_gb":4,"gpu_required":false}}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.Requirements.CPUCores != 2 || request.Requirements.RAMGB != 4 {
		t.Fatalf("requirements were not decoded: %#v", request.Requirements)
	}
}

func TestTaskResultDecodesMachineReadableErrorCode(t *testing.T) {
	var payload bytes.Buffer
	_ = binary.Write(&payload, binary.LittleEndian, uint32(1))
	_ = binary.Write(&payload, binary.LittleEndian, uint64(7))
	for _, value := range []string{"JOB-7", "FAILED"} {
		if err := writeString(&payload, value); err != nil {
			t.Fatal(err)
		}
	}
	_ = binary.Write(&payload, binary.LittleEndian, int64(0))
	if err := writeString(&payload, "unsupported workload: nope"); err != nil {
		t.Fatal(err)
	}
	_ = binary.Write(&payload, binary.LittleEndian, uint64(3))
	for _, value := range []string{"NODE-1", "unsupported_workload"} {
		if err := writeString(&payload, value); err != nil {
			t.Fatal(err)
		}
	}

	results, err := decodeTaskResultBatch(payload.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ErrorCode != "unsupported_workload" {
		t.Fatalf("unexpected result error code: %#v", results)
	}
}

func TestRegisterRoundTrip(t *testing.T) {
	in := NodeInfo{
		ID: "node-1", Hostname: "host", OS: "Linux", Arch: "x86_64",
		CPUCores: 8, LogicalCPUCores: 8, PhysicalCPUCores: 4, RAMGB: 32,
		GPU: GPUInfo{Vendor: "NVIDIA", Model: "RTX", VRAMGB: 12},
	}
	payload, err := encodeRegister(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeRegister(payload)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != in.ID || out.CPUCores != in.CPUCores || out.LogicalCPUCores != in.LogicalCPUCores || out.PhysicalCPUCores != in.PhysicalCPUCores || out.GPU.Model != in.GPU.Model {
		t.Fatalf("register mismatch: %#v %#v", in, out)
	}
}

func TestHeartbeatRejectsTrailingData(t *testing.T) {
	payload := make([]byte, 32)
	payload = append(payload, 0)
	if _, _, err := decodeHeartbeat(payload); err == nil {
		t.Fatal("heartbeat decoder accepted trailing bytes")
	}
}

func TestHeartbeatCarriesOptionalGPUTelemetry(t *testing.T) {
	payload := encodeHeartbeatTelemetryWithCountersAndGPU(1, 2, 3, 11, 7, 12.5, 6, 10, 42.5)
	_, telemetry, err := decodeHeartbeat(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !telemetry.GPUAvailableVRAMKnown || telemetry.GPUAvailableVRAMGB != 10 || telemetry.GPUUtilizationPercent != 42.5 {
		t.Fatalf("unexpected GPU telemetry: %#v", telemetry)
	}
}

func TestGeneralTaskWireRoundTrip(t *testing.T) {
	in := GeneralTaskEnvelope{
		TaskID:  42,
		JobID:   "TASK-42",
		Attempt: 2,
		Spec: GeneralTaskSpec{
			Type: TaskTypeScript, Version: "1", Runtime: "python", Script: "main.py",
			Arguments: []string{"--name", "Nodren"}, Environment: map[string]string{"MODE": "test"},
			WorkingDirectory: ".", StdinB64: "aGVsbG8=", TimeoutMS: 1000,
			StdoutLimitBytes: 2048, StderrLimitBytes: 2048,
			Requirements:    ResourceRequirements{CPUCores: 2, RAMGB: 4, MaxRAMGB: 8, GPURequired: true, GPUCount: 1, VRAMGB: 8, AcceleratorType: "CUDA", GPUCapabilities: []string{"tensor"}},
			Target:          TaskTarget{OS: "windows", Arch: "x86_64", RequiredRuntimes: []string{"python"}, AllowedWorkerIDs: []string{"worker-a"}},
			InputArtifacts:  []TaskArtifact{{ID: "ART-1", Name: "input.bin", Size: 3, SHA256: strings.Repeat("a", 64), Kind: "input"}},
			Strategy:        ExecutionSingle,
			PackageManifest: &TaskPackageManifest{EntryPoint: "main.py", Runtime: "python", Arguments: []string{"--ready"}, Environment: map[string]string{"MODEL": "demo"}},
			Retry:           RetryPolicy{MaxRetries: 2},
		},
	}
	payload, err := encodeGeneralTask(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeGeneralTask(payload)
	if err != nil {
		t.Fatal(err)
	}
	if out.TaskID != in.TaskID || out.Spec.Runtime != "python" || out.Spec.Environment["MODE"] != "test" || len(out.Spec.InputArtifacts) != 1 || out.Spec.Retry.MaxRetries != 2 || out.Spec.Requirements.GPUCount != 1 || out.Spec.PackageManifest == nil || out.Spec.PackageManifest.Environment["MODEL"] != "demo" {
		t.Fatalf("general task mismatch: %#v", out)
	}
}

func TestTaskOutputChunkDecodesStreamPayload(t *testing.T) {
	var payload bytes.Buffer
	_ = binary.Write(&payload, binary.LittleEndian, uint64(99))
	if err := writeString(&payload, "stdout"); err != nil {
		t.Fatal(err)
	}
	payload.WriteByte(1)
	if err := writeBytes(&payload, []byte("token")); err != nil {
		t.Fatal(err)
	}
	chunk, err := decodeTaskOutput(payload.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if chunk.TaskID != 99 || chunk.Stream != "stdout" || !chunk.Final || string(chunk.Data) != "token" {
		t.Fatalf("unexpected task output chunk: %#v", chunk)
	}
}

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net"
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
		CPUCores: 8, RAMGB: 32,
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
	if out.ID != in.ID || out.CPUCores != in.CPUCores || out.GPU.Model != in.GPU.Model {
		t.Fatalf("register mismatch: %#v %#v", in, out)
	}
}

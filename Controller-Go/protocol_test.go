package main

import (
	"bytes"
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

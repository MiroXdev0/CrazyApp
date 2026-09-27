package main

import (
	"bytes"
	"testing"
)

func TestBinaryFrameRoundTrip(t *testing.T) {
	payload := []byte{10, 20, 30, 40, 50}
	encoded, err := EncodeFrame(MessageTypeTaskBatch, 42, payload)
	if err != nil {
		t.Fatalf("EncodeFrame returned error: %v", err)
	}
	decoded, err := DecodeFrame(encoded)
	if err != nil {
		t.Fatalf("DecodeFrame returned error: %v", err)
	}
	if decoded.Type != MessageTypeTaskBatch {
		t.Fatalf("type mismatch: got %d want %d", decoded.Type, MessageTypeTaskBatch)
	}
	if decoded.RequestID != 42 {
		t.Fatalf("request mismatch: got %d want 42", decoded.RequestID)
	}
	if !bytes.Equal(decoded.Payload, payload) {
		t.Fatalf("payload mismatch: got %v want %v", decoded.Payload, payload)
	}
}

func TestTaskBatchChecksumConsistency(t *testing.T) {
	tasks := []TaskPacket{
		{TaskID: 1, Payload: []byte{1, 2, 3}},
		{TaskID: 2, Payload: []byte{4, 5, 6}},
		{TaskID: 3, Payload: []byte{7, 8, 9}},
	}
	first := ComputeTaskChecksum(tasks)
	second := ComputeTaskChecksum(tasks)
	if first != second {
		t.Fatalf("checksum not stable: %d != %d", first, second)
	}
	if first == 0 {
		t.Fatalf("checksum unexpectedly zero")
	}
}

func TestNativeExecutionBatchReturnsDeterministicSum(t *testing.T) {
	tasks := []TaskPacket{
		{TaskID: 1, Payload: []byte{1, 2, 3, 4}},
		{TaskID: 2, Payload: []byte{5, 6, 7, 8}},
	}
	results, err := ExecuteNativeTaskBatch(tasks)
	if err != nil {
		t.Fatalf("ExecuteNativeTaskBatch returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Result != 10 || results[1].Result != 26 {
		t.Fatalf("unexpected native sums: %+v", results)
	}
}

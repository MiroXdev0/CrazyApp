package main

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestWorkloadPayloads(t *testing.T) {
	sum, err := workloadPayload("sum", []string{"1", "2", "255"})
	if err != nil {
		t.Fatal(err)
	}
	if string(sum) != string([]byte{1, 2, 255}) {
		t.Fatalf("unexpected sum payload: %v", sum)
	}

	dot, err := workloadPayload("dot_product", []string{"1,2,3", "4,5,6"})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{3, 0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3, 0, 0, 0, 4, 0, 0, 0, 5, 0, 0, 0, 6, 0, 0, 0}
	if string(dot) != string(want) {
		t.Fatalf("unexpected dot_product payload: %v", dot)
	}
}

func TestWorkloadPayloadRejectsMalformedInput(t *testing.T) {
	if _, err := workloadPayload("sum", nil); err == nil {
		t.Fatal("expected empty sum arguments to fail")
	}
	if _, err := workloadPayload("dot_product", []string{"1,2", "3"}); err == nil {
		t.Fatal("expected mismatched dot_product vectors to fail")
	}
}

func TestJobRequestShape(t *testing.T) {
	payload := []byte{1, 2, 3}
	request := jobRequest{
		Command:      "sum",
		Priority:     50,
		Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
		PayloadB64:   base64.StdEncoding.EncodeToString(payload),
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	if value["command"] != "sum" || value["payload_base64"] != "AQID" {
		t.Fatalf("unexpected job request: %s", encoded)
	}
}

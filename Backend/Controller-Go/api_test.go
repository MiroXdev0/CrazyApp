package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIHealthAndNodesEndpoints(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 8, 32)
	controller.nodes["worker-a"].AllocatedCPUCores = 2
	controller.nodes["worker-a"].AllocatedRAMGB = 8
	updateNodeCapacity(controller.nodes["worker-a"])

	server := httptest.NewServer(controller.routes())
	defer server.Close()

	// Test GET /health
	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	var health map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health["status"] != "ok" || health["service"] != "nodren-controller" {
		t.Fatalf("unexpected health response: %#v", health)
	}

	// Test GET /v1/nodes
	resp, err = http.Get(server.URL + "/v1/nodes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var nodes []NodeRecord
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Info.ID != "worker-a" {
		t.Fatalf("unexpected nodes: %#v", nodes)
	}
	if nodes[0].AllocatedCPUCores != 2 || nodes[0].AvailableCPUCores != 6 {
		t.Fatalf("unexpected node capacity accounting: %#v", nodes[0])
	}
	if nodes[0].CapacityScore <= 0 || nodes[0].EffectiveCapacity <= 0 {
		t.Fatalf("expected positive capacity metrics: %#v", nodes[0])
	}
}

func TestAPIJobsLifecycleAndInspection(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 4, 16)
	controller.sessions["worker-a"] = newSession(nil)

	server := httptest.NewServer(controller.routes())
	defer server.Close()

	// Submit a job via POST /v1/jobs
	reqBody, _ := json.Marshal(jobRequest{
		Command:      "sum",
		Priority:     50,
		Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
		PayloadB64:   base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4, 5}),
	})
	resp, err := http.Post(server.URL+"/v1/jobs", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", resp.StatusCode)
	}
	var submitted Job
	if err := json.NewDecoder(resp.Body).Decode(&submitted); err != nil {
		t.Fatal(err)
	}
	if submitted.ID == "" || submitted.Status != JobQueued {
		t.Fatalf("unexpected submitted job: %#v", submitted)
	}

	// Schedule the job
	controller.scheduleOnce()

	// Inspect via GET /v1/jobs/{id}
	resp, err = http.Get(server.URL + "/v1/jobs/" + submitted.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var inspected Job
	if err := json.NewDecoder(resp.Body).Decode(&inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.Status != JobRunning || len(inspected.Partitions) != 1 {
		t.Fatalf("unexpected running job state: %#v", inspected)
	}

	// Complete the partition
	taskID := inspected.Partitions[0].TaskID
	controller.applyResults([]TaskResult{{
		TaskID: taskID, JobID: inspected.ID, Status: "COMPLETED", Value: 15, NodeID: "worker-a",
	}})

	// Inspect again after completion
	resp, err = http.Get(server.URL + "/v1/jobs/" + submitted.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var completed Job
	if err := json.NewDecoder(resp.Body).Decode(&completed); err != nil {
		t.Fatal(err)
	}
	if completed.Status != JobCompleted || completed.Result == nil || completed.Result.Value != 15 {
		t.Fatalf("unexpected completed job state: %#v", completed)
	}
	if completed.Distribution.CompletedPartitions != 1 || completed.Distribution.ProgressPercent != 100 {
		t.Fatalf("unexpected distribution progress: %#v", completed.Distribution)
	}
}

func TestAPIManualDistributionValidation(t *testing.T) {
	controller := NewController("", "")
	server := httptest.NewServer(controller.routes())
	defer server.Close()

	// Invalid total percentage (90 != 100)
	reqBody, _ := json.Marshal(jobRequest{
		Command:           "sum",
		DistributionMode:  DistributionManual,
		ManualAllocations: map[string]uint8{"worker-a": 50, "worker-b": 40},
	})
	resp, err := http.Post(server.URL+"/v1/jobs", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for invalid allocation total, got %d", resp.StatusCode)
	}
}

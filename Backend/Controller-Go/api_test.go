package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestAPIAIPlanEndpoint(t *testing.T) {
	controller := NewController("", "")
	for _, id := range []string{"worker-a", "worker-b"} {
		controller.nodes[id] = aiTestWorker(id, 1)
		controller.sessions[id] = newSession(nil)
	}
	server := httptest.NewServer(controller.routes())
	defer server.Close()

	body, err := json.Marshal(AIWorkloadSpec{
		Runtime:     "python",
		EntryPoint:  "run.py",
		WorkerCount: 2,
		Strategy:    AIStrategyDistributedProcess,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(server.URL+"/v1/ai/plans", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("expected AI plan creation to return 201, got %d", response.StatusCode)
	}
	var plan AIExecutionPlan
	if err := json.NewDecoder(response.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	if plan.ExecutionID == "" || plan.WorldSize != 2 || len(plan.Workers) != 2 {
		t.Fatalf("unexpected AI plan response: %#v", plan)
	}
	inspect, err := http.Get(server.URL + "/v1/ai/plans/" + plan.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	defer inspect.Body.Close()
	if inspect.StatusCode != http.StatusOK {
		t.Fatalf("expected AI plan inspection to return 200, got %d", inspect.StatusCode)
	}
}

func TestAPIArtifactUploadAndDownload(t *testing.T) {
	controller := NewController("", "")
	controller.artifactDir = t.TempDir()
	server := httptest.NewServer(controller.routes())
	defer server.Close()
	payload := []byte{0, 1, 2, 3, 255}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/artifacts?name=input.bin", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Nodren-Artifact-Name", "input.bin")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		response.Body.Close()
		t.Fatalf("expected artifact upload to succeed, got %d", response.StatusCode)
	}
	var artifact ArtifactRecord
	if err := json.NewDecoder(response.Body).Decode(&artifact); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if artifact.ID == "" || artifact.Size != uint64(len(payload)) || len(artifact.SHA256) != 64 {
		t.Fatalf("unexpected artifact metadata: %#v", artifact)
	}
	lookup, err := http.Get(server.URL + "/v1/artifacts?sha256=" + artifact.SHA256 + "&size=5")
	if err != nil {
		t.Fatal(err)
	}
	if lookup.StatusCode != http.StatusOK {
		lookup.Body.Close()
		t.Fatalf("expected content-addressed lookup to succeed, got %d", lookup.StatusCode)
	}
	lookup.Body.Close()
	response, err = http.Get(server.URL + "/v1/artifacts/" + artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected artifact download to succeed, got %d", response.StatusCode)
	}
	downloaded, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded, payload) {
		t.Fatalf("artifact contents changed: %#v", downloaded)
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

func TestAPIControlOperations(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 4, 16)
	controller.sessions["worker-a"] = newSession(nil)
	server := httptest.NewServer(controller.routes())
	defer server.Close()

	post := func(path string, body any) *http.Response {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.Post(server.URL+path, "application/json", bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	response := post("/v1/nodes/worker-a/pause", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected worker pause to succeed, got %d", response.StatusCode)
	}
	response.Body.Close()
	if controller.nodes["worker-a"].State != NodePaused {
		t.Fatalf("expected worker to be paused, got %s", controller.nodes["worker-a"].State)
	}
	response = post("/v1/nodes/worker-a/resume", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || controller.nodes["worker-a"].State != NodeReady {
		t.Fatalf("expected worker resume to succeed, got status=%d state=%s", response.StatusCode, controller.nodes["worker-a"].State)
	}

	job, err := controller.createJob(jobRequest{Command: "sum", DistributionMode: DistributionAutomatic})
	if err != nil {
		t.Fatal(err)
	}
	response = post("/v1/jobs/"+job.ID+"/pause", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || controller.jobs[job.ID].Status != JobPaused {
		t.Fatalf("expected queued job pause to succeed, got status=%d job=%s", response.StatusCode, controller.jobs[job.ID].Status)
	}
	response = post("/v1/jobs/"+job.ID+"/resume", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || controller.jobs[job.ID].Status != JobQueued {
		t.Fatalf("expected queued job resume to succeed, got status=%d job=%s", response.StatusCode, controller.jobs[job.ID].Status)
	}

	requestBody, _ := json.Marshal(distributionRequest{Mode: DistributionManual, ManualAllocations: map[string]uint8{"worker-a": 100}})
	request, _ := http.NewRequest(http.MethodPut, server.URL+"/v1/jobs/"+job.ID+"/distribution", bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || controller.jobs[job.ID].Distribution.Mode != DistributionManual {
		t.Fatalf("expected distribution update to succeed, got status=%d mode=%s", response.StatusCode, controller.jobs[job.ID].Distribution.Mode)
	}

	response = post("/v1/jobs/"+job.ID+"/cancel", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || controller.jobs[job.ID].Status != JobCancelled {
		t.Fatalf("expected job cancel to succeed, got status=%d job=%s", response.StatusCode, controller.jobs[job.ID].Status)
	}
}

func TestAPIObservabilityEndpoints(t *testing.T) {
	controller := NewController("", "")
	node := readyNode("worker-a", 4, 16)
	node.Telemetry = WorkerTelemetry{
		Timestamp: time.Now().UTC(), CPUUtilizationPercent: 25,
		MemoryAvailableGB: 12, MemoryUtilizationPercent: 25,
	}
	controller.nodes[node.Info.ID] = node
	controller.sessions[node.Info.ID] = newSession(nil)
	server := httptest.NewServer(controller.routes())
	defer server.Close()

	response, err := http.Get(server.URL + "/v1/nodes/worker-a/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected worker stats endpoint to succeed, got %d", response.StatusCode)
	}
	var workerStats workerStatsResponse
	if err := json.NewDecoder(response.Body).Decode(&workerStats); err != nil {
		t.Fatal(err)
	}
	if workerStats.Node.Telemetry.CPUUtilizationPercent != 25 || workerStats.SchedulerWeight <= 0 {
		t.Fatalf("unexpected worker stats: %#v", workerStats)
	}

	job, err := controller.createJob(jobRequest{
		Command: "sum", Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
		PayloadB64: base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
	})
	if err != nil {
		t.Fatal(err)
	}
	controller.scheduleOnce()
	response, err = http.Get(server.URL + "/v1/jobs/" + job.ID + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var stats jobStatsResponse
	if err := json.NewDecoder(response.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if len(stats.Job.Partitions) != 1 || len(stats.SchedulerReasons) != 1 || stats.Job.Partitions[0].AssignmentReason == "" {
		t.Fatalf("expected explainable partition stats: %#v", stats)
	}

	response, err = http.Get(server.URL + "/v1/jobs/" + job.ID + "/partitions")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var partitions []Partition
	if err := json.NewDecoder(response.Body).Decode(&partitions); err != nil {
		t.Fatal(err)
	}
	if len(partitions) != 1 || partitions[0].NodeID != "worker-a" {
		t.Fatalf("unexpected partition response: %#v", partitions)
	}
}

func TestAPIClusterStatusAndStaleWorkerDetection(t *testing.T) {
	controller := NewController("127.0.0.1:9000", "127.0.0.1:8080")
	node := readyNode("worker-a", 8, 32)
	node.Telemetry = WorkerTelemetry{
		Timestamp:                time.Now().UTC(),
		ActiveTasks:              2,
		CompletedTasks:           7,
		FailedTasks:              1,
		CPUUtilizationPercent:    40,
		MemoryAvailableGB:        20,
		MemoryAvailableKnown:     true,
		MemoryUtilizationPercent: 37.5,
	}
	node.CompletedTasks = 7
	node.FailedTasks = 1
	controller.nodes[node.Info.ID] = node

	server := httptest.NewServer(controller.routes())
	defer server.Close()
	response, err := http.Get(server.URL + "/v1/cluster/status")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected cluster status endpoint to succeed, got %d", response.StatusCode)
	}
	var status clusterStatusResponse
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Workers != 1 || status.OnlineWorkers != 1 || status.TotalCPUCores != 8 || status.TotalRAMGB != 32 {
		t.Fatalf("unexpected cluster capacity/status: %#v", status)
	}
	if status.ActiveTasks != 2 || status.TotalCompletedTasks != 7 || status.TotalFailedTasks != 1 || status.CPUUtilizationPercent != 40 {
		t.Fatalf("unexpected cluster telemetry: %#v", status)
	}
	job, err := controller.createJob(jobRequest{Command: "sum"})
	if err != nil {
		t.Fatal(err)
	}
	activeResponse, err := http.Get(server.URL + "/v1/jobs/active")
	if err != nil {
		t.Fatal(err)
	}
	defer activeResponse.Body.Close()
	var activeJobs []Job
	if err := json.NewDecoder(activeResponse.Body).Decode(&activeJobs); err != nil {
		t.Fatal(err)
	}
	if len(activeJobs) != 1 || activeJobs[0].ID != job.ID {
		t.Fatalf("unexpected active jobs response: %#v", activeJobs)
	}

	controller.nodes[node.Info.ID].LastHeartbeat = time.Now().Add(-16 * time.Second)
	lost := controller.markStaleWorkers(time.Now())
	if len(lost) != 1 || controller.nodes[node.Info.ID].State != NodeLost {
		t.Fatalf("stale worker was not marked lost: lost=%v node=%#v", lost, controller.nodes[node.Info.ID])
	}
}

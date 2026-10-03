package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerOutputArtifactIsVerifiedAndStored(t *testing.T) {
	controller := NewController("", "")
	controller.artifactDir = t.TempDir()
	controller.jobs["JOB-output"] = &Job{
		ID:     "JOB-output",
		Status: JobRunning,
		Task:   &GeneralTaskSpec{OutputArtifacts: []TaskArtifact{{ID: "OUT-1", Name: "result.json", Kind: "output"}}},
	}
	controller.taskToJob[77] = "JOB-output"
	controller.taskToNode[77] = "worker-output"
	payload := []byte(`{"value":42}`)
	hash := sha256.Sum256(payload)
	begin, err := encodeArtifactBegin(77, TaskArtifact{ID: "OUT-1", Name: "result.json", Size: uint64(len(payload)), SHA256: hex.EncodeToString(hash[:]), Kind: "output"})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.beginWorkerArtifact("worker-output", begin); err != nil {
		t.Fatal(err)
	}
	chunk, err := encodeArtifactChunk(77, "OUT-1", 0, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.appendWorkerArtifact("worker-output", chunk); err != nil {
		t.Fatal(err)
	}
	end, err := encodeArtifactEnd(77, "OUT-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.finishWorkerArtifact("worker-output", end); err != nil {
		t.Fatal(err)
	}
	record := controller.artifacts["OUT-1"]
	if record == nil || record.Size != uint64(len(payload)) || len(controller.taskArtifacts[77]) != 1 {
		t.Fatalf("output artifact was not stored: record=%#v task=%#v", record, controller.taskArtifacts[77])
	}
}

func TestArtifactPathRejectsTraversalAndUnsafeIdentifiers(t *testing.T) {
	controller := NewController("", "")
	controller.artifactDir = t.TempDir()
	for _, id := range []string{"../escape", "..", "sub/path", `sub\path`, "C:escape", "", strings.Repeat("a", maxArtifactIDLength+1)} {
		if path, err := controller.artifactPath(id); err == nil || path != "" {
			t.Errorf("artifactPath(%q) = %q, %v; want rejection", id, path, err)
		}
	}
	path, err := controller.artifactPath("AI-OUT-123456")
	if err != nil {
		t.Fatalf("valid artifact ID rejected: %v", err)
	}
	if filepath.Dir(path) != controller.artifactStoreDir() {
		t.Fatalf("artifact path escaped store: %q", path)
	}
}

func TestBeginWorkerArtifactRejectsTraversalID(t *testing.T) {
	controller := NewController("", "")
	controller.artifactDir = t.TempDir()
	controller.jobs["JOB-output"] = &Job{
		ID:     "JOB-output",
		Status: JobRunning,
		Task:   &GeneralTaskSpec{OutputArtifacts: []TaskArtifact{{ID: "../escape", Name: "result.json"}}},
	}
	controller.taskToJob[77] = "JOB-output"
	controller.taskToNode[77] = "worker-output"
	begin, err := encodeArtifactBegin(77, TaskArtifact{ID: "../escape", Name: "result.json", Size: 0, SHA256: hex.EncodeToString(make([]byte, sha256.Size))})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.beginWorkerArtifact("worker-output", begin); err == nil {
		t.Fatal("worker output artifact with traversal ID was accepted")
	}
}

func TestSafeTensorsInspectionEstimatesMemory(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "model.safetensors")
	header, err := json.Marshal(map[string]any{
		"__metadata__": map[string]string{"model_type": "demo-transformer"},
		"weight":       map[string]any{"dtype": "F32", "shape": []uint64{2, 3}, "data_offsets": []uint64{0, 24}},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(file, binary.LittleEndian, uint64(len(header))); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(header); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(make([]byte, 24)); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	inspection, err := inspectAIPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Format != "safetensors" || inspection.Architecture != "demo-transformer" || inspection.ParameterCount != 6 || inspection.EstimatedRAMGB == 0 || inspection.EstimatedVRAMGB == 0 {
		t.Fatalf("unexpected inspection: %#v", inspection)
	}
}

func TestGGUFInspectionReadsArchitectureQuantizationAndContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tiny.gguf")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writeString := func(value string) {
		if err := binary.Write(file, binary.LittleEndian, uint64(len(value))); err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString(value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := file.Write([]byte("GGUF")); err != nil {
		t.Fatal(err)
	}
	for _, value := range []uint32{3} {
		if err := binary.Write(file, binary.LittleEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []uint64{1, 2} {
		if err := binary.Write(file, binary.LittleEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	writeString("general.architecture")
	if err := binary.Write(file, binary.LittleEndian, uint32(8)); err != nil {
		t.Fatal(err)
	}
	writeString("llama")
	writeString("llama.context_length")
	if err := binary.Write(file, binary.LittleEndian, uint32(4)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(file, binary.LittleEndian, uint32(4096)); err != nil {
		t.Fatal(err)
	}
	writeString("blk.0.weight")
	if err := binary.Write(file, binary.LittleEndian, uint32(2)); err != nil {
		t.Fatal(err)
	}
	for _, value := range []uint64{2, 2} {
		if err := binary.Write(file, binary.LittleEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := binary.Write(file, binary.LittleEndian, uint32(0)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(file, binary.LittleEndian, uint64(0)); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	inspection, err := inspectAIPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Status != "VALIDATED" || inspection.Architecture != "llama" || inspection.ContextLength != 4096 || inspection.TensorCount != 1 || inspection.ParameterCount != 4 {
		t.Fatalf("unexpected GGUF inspection: %#v", inspection)
	}
}

func TestSchedulerUsesLiveMemoryAndVRAMAvailability(t *testing.T) {
	controller := NewController("", "")
	node := aiTestWorker("resource-worker", 1)
	node.Telemetry.MemoryAvailableKnown = true
	node.Telemetry.MemoryAvailableGB = 8
	node.Telemetry.GPUAvailableVRAMKnown = true
	node.Telemetry.GPUAvailableVRAMGB = 6
	controller.nodes[node.Info.ID] = node
	controller.sessions[node.Info.ID] = newSession(nil)
	if controller.canFit(node, ResourceRequirements{CPUCores: 1, RAMGB: 12, VRAMGB: 1}) {
		t.Fatal("scheduler accepted a task larger than live RAM availability")
	}
	if controller.canFit(node, ResourceRequirements{CPUCores: 1, RAMGB: 1, VRAMGB: 8}) {
		t.Fatal("scheduler accepted a task larger than live VRAM availability")
	}
	if !controller.canFit(node, ResourceRequirements{CPUCores: 1, RAMGB: 4, VRAMGB: 4}) {
		t.Fatal("scheduler rejected resources that fit live availability")
	}
}

func aiTestWorker(id string, capacity float64) *NodeRecord {
	node := readyNode(id, 8, 32)
	node.Info.Runtimes = []string{"python"}
	node.Info.ExecutionTypes = []string{"PROCESS", "SCRIPT"}
	node.Info.GPU = GPUInfo{
		Vendor:       "NVIDIA",
		Model:        "A10",
		VRAMGB:       24,
		Count:        1,
		Capabilities: []string{"cuda"},
		Runtime:      "CUDA",
	}
	node.PerformanceFactor = capacity
	updateNodeCapacity(node)
	return node
}

func TestAIPlanFormsCapabilityAwareWorkerGroup(t *testing.T) {
	controller := NewController("", "")
	for _, node := range []*NodeRecord{
		aiTestWorker("worker-a", 1.0),
		aiTestWorker("worker-b", 1.3),
		aiTestWorker("worker-c", 0.8),
	} {
		controller.nodes[node.Info.ID] = node
		controller.sessions[node.Info.ID] = newSession(nil)
	}

	plan, err := controller.createAIPlan(AIWorkloadSpec{
		Type:        "AI",
		Runtime:     "python",
		EntryPoint:  "run.py",
		WorkerCount: 2,
		Strategy:    AIStrategyDistributedProcess,
		Requirements: ResourceRequirements{
			CPUCores:    2,
			RAMGB:       4,
			GPURequired: true,
			GPUCount:    1,
			VRAMGB:      16,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.WorldSize != 2 || len(plan.Workers) != 2 || len(plan.Launches) != 2 {
		t.Fatalf("unexpected AI plan shape: %#v", plan)
	}
	if plan.Workers[0].WorkerID != "worker-b" {
		t.Fatalf("planner did not prefer the higher-capacity worker: %#v", plan.Workers)
	}
	if plan.Workers[0].WorkerID == plan.Workers[1].WorkerID || plan.Workers[1].Rank != 1 {
		t.Fatalf("worker group ranks are not distinct: %#v", plan.Workers)
	}
	launch := plan.Launches[0].Task
	if launch.Type != TaskTypeScript || launch.Target.AllowedWorkerIDs[0] != plan.Workers[0].WorkerID || launch.Environment["NODREN_AI_WORLD_SIZE"] != "2" {
		t.Fatalf("launch configuration is incomplete: %#v", launch)
	}
}

func TestAIPlanUsesAllSelectedWorkerCPUsByDefaultWhenRequested(t *testing.T) {
	controller := NewController("", "")
	node := aiTestWorker("full-cpu-worker", 1)
	controller.nodes[node.Info.ID] = node
	controller.sessions[node.Info.ID] = newSession(nil)
	plan, err := controller.createAIPlan(AIWorkloadSpec{
		Runtime:    "python",
		EntryPoint: "run.py",
		CPUAuto:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Workers[0].CPUCores != node.Info.CPUCores || plan.Launches[0].Task.Requirements.CPUCores != node.Info.CPUCores {
		t.Fatalf("AI plan did not resolve automatic CPU capacity: worker=%d launch=%d node=%d", plan.Workers[0].CPUCores, plan.Launches[0].Task.Requirements.CPUCores, node.Info.CPUCores)
	}
}

func TestAIExecutionCreatesExistingGeneralizedTasks(t *testing.T) {
	controller := NewController("", "")
	for _, id := range []string{"worker-a", "worker-b"} {
		node := aiTestWorker(id, 1)
		controller.nodes[id] = node
		controller.sessions[id] = newSession(nil)
	}
	execution, err := controller.createAIExecution(AIWorkloadSpec{
		Runtime:     "python",
		EntryPoint:  "run.py",
		WorkerCount: 2,
		Strategy:    AIStrategyDistributedProcess,
	})
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != "QUEUED" || len(execution.TaskIDs) != 2 {
		t.Fatalf("unexpected AI execution: %#v", execution)
	}
	for _, taskID := range execution.TaskIDs {
		job := controller.jobs[taskID]
		if job == nil || job.Task == nil || job.Task.WorkloadKind != "ai-distributed-process" {
			t.Fatalf("AI launch was not represented by a generalized task: %#v", job)
		}
	}
}

func TestAIPlannerRejectsUnimplementedStrategies(t *testing.T) {
	controller := NewController("", "")
	for _, id := range []string{"worker-a", "worker-b"} {
		node := aiTestWorker(id, 1)
		controller.nodes[id] = node
		controller.sessions[id] = newSession(nil)
	}
	_, err := controller.createAIPlan(AIWorkloadSpec{
		Runtime:     "python",
		EntryPoint:  "run.py",
		WorkerCount: 2,
		Strategy:    AIStrategyTensorParallel,
	})
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("expected clear unsupported-strategy error, got %v", err)
	}
}

func TestAIModelShardMetadataUsesArtifactVerification(t *testing.T) {
	controller := NewController("", "")
	node := aiTestWorker("worker-a", 1)
	controller.nodes[node.Info.ID] = node
	controller.sessions[node.Info.ID] = newSession(nil)
	digest := strings.Repeat("a", 64)
	controller.artifacts["ART-model"] = &ArtifactRecord{
		ID:     "ART-model",
		Name:   "weights-0001.bin",
		Size:   128,
		SHA256: digest,
		Kind:   "model",
	}
	spec := AIWorkloadSpec{
		Runtime:    "python",
		EntryPoint: "run.py",
		Model:      AIModel{Artifacts: []TaskArtifact{{ID: "ART-model", Name: "weights-0001.bin", Size: 128, SHA256: digest}}, Shards: []AIModelShard{{ID: "shard-0", ArtifactID: "ART-model", Size: 128, SHA256: digest, Runtime: "python", Precision: "bf16"}}},
	}
	if _, err := controller.createAIPlan(spec); err != nil {
		t.Fatal(err)
	}
	spec.Model.Shards[0].Size = 64
	if _, err := controller.createAIPlan(spec); err == nil || !strings.Contains(err.Error(), "size does not match") {
		t.Fatalf("expected shard size validation error, got %v", err)
	}
	spec.Model.Shards[0].Size = 128
	spec.Model.Shards[0].ComputationShard = true
	if _, err := controller.createAIPlan(spec); err == nil || !strings.Contains(err.Error(), "computation/model sharding is not implemented") {
		t.Fatalf("expected computation-shard capability error, got %v", err)
	}
}

func TestAIPlanPersistsWithoutEmbeddingArtifactBytes(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "controller-state.json")
	controller := NewController("", "")
	controller.statePath = statePath
	node := aiTestWorker("worker-a", 1)
	controller.nodes[node.Info.ID] = node
	controller.sessions[node.Info.ID] = newSession(nil)
	plan, err := controller.createAIPlan(AIWorkloadSpec{
		Runtime:    "python",
		EntryPoint: "run.py",
		Framework:  "pytorch",
		Precision:  "bf16",
		Model:      AIModel{Name: "demo-model", Framework: "pytorch", Precision: "bf16"},
	})
	if err != nil {
		t.Fatal(err)
	}

	recovered := NewController("", "")
	recovered.statePath = statePath
	if err := recovered.loadState(); err != nil {
		t.Fatal(err)
	}
	recoveredPlan := recovered.aiPlans[plan.ExecutionID]
	if recoveredPlan == nil || len(recoveredPlan.Launches) != 1 {
		t.Fatalf("AI plan was not recovered: %#v", recovered.aiPlans)
	}
	if recoveredPlan.Workload.Framework != "pytorch" || recoveredPlan.Workload.Precision != "bf16" || recoveredPlan.Workload.Model.Name != "demo-model" {
		t.Fatalf("AI workload metadata was not recovered: %#v", recoveredPlan.Workload)
	}
}

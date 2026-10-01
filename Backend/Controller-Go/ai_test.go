package main

import (
	"path/filepath"
	"strings"
	"testing"
)

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

package main

import "testing"

func readyNode(id string, cpu uint32, ram uint64) *NodeRecord {
	return &NodeRecord{
		Info: NodeInfo{
			ID: id, CPUCores: cpu, RAMGB: ram,
		},
		State: NodeReady,
	}
}

func TestChooseNodeUsesBestFitAndDeterministicTieBreak(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 2, 4)
	controller.nodes["worker-b"] = readyNode("worker-b", 8, 16)
	controller.sessions["worker-a"] = newSession(nil)
	controller.sessions["worker-b"] = newSession(nil)

	if got := controller.chooseNode(ResourceRequirements{CPUCores: 1, RAMGB: 1}); got != "worker-a" {
		t.Fatalf("small job selected %q, want worker-a", got)
	}
	if got := controller.chooseNode(ResourceRequirements{CPUCores: 6, RAMGB: 8}); got != "worker-b" {
		t.Fatalf("large job selected %q, want worker-b", got)
	}

	controller.nodes["worker-c"] = readyNode("worker-c", 2, 4)
	controller.sessions["worker-c"] = newSession(nil)
	if got := controller.chooseNode(ResourceRequirements{CPUCores: 1, RAMGB: 1}); got != "worker-a" {
		t.Fatalf("tie selected %q, want lexicographically first worker", got)
	}
}

func TestMeasuredTelemetryAndPerformanceAffectScheduling(t *testing.T) {
	controller := NewController("", "")
	fast := readyNode("fast", 4, 16)
	slow := readyNode("slow", 4, 16)
	fast.Telemetry.CPUUtilizationPercent = 10
	slow.Telemetry.CPUUtilizationPercent = 90
	fast.PerformanceFactor = 1.4
	slow.PerformanceFactor = 0.6
	controller.nodes[fast.Info.ID] = fast
	controller.nodes[slow.Info.ID] = slow
	controller.sessions[fast.Info.ID] = newSession(nil)
	controller.sessions[slow.Info.ID] = newSession(nil)
	updateNodeCapacity(fast)
	updateNodeCapacity(slow)

	if fast.EffectiveCapacity <= slow.EffectiveCapacity {
		t.Fatalf("expected measured load and performance to favor fast worker: fast=%.2f slow=%.2f", fast.EffectiveCapacity, slow.EffectiveCapacity)
	}

	job := &Job{ID: "job", Distribution: DistributionInfo{Mode: DistributionAutomatic, TotalPartitions: 2}, Partitions: []Partition{
		{ID: "p1", Units: 10, State: PartitionQueued},
		{ID: "p2", Units: 10, State: PartitionQueued},
	}}
	if got := controller.choosePartitionNodeLocked(job); got != "fast" {
		t.Fatalf("expected fast worker to be selected, got %q", got)
	}
}

func TestChooseNodeExcludesUnavailableAndIncompatibleWorkers(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["busy"] = readyNode("busy", 8, 16)
	controller.nodes["lost"] = readyNode("lost", 8, 16)
	controller.nodes["lost"].State = NodeLost
	controller.sessions["busy"] = newSession(nil)
	controller.sessions["lost"] = newSession(nil)

	if got := controller.chooseNode(ResourceRequirements{CPUCores: 32, RAMGB: 1}); got != "" {
		t.Fatalf("incompatible worker selected %q", got)
	}
	controller.nodes["busy"].State = NodeBusy
	controller.nodes["busy"].AllocatedCPUCores = 8
	controller.nodes["busy"].AllocatedRAMGB = 16
	if got := controller.chooseNode(ResourceRequirements{CPUCores: 1, RAMGB: 1}); got != "" {
		t.Fatalf("busy worker selected %q", got)
	}
}

func TestGPURequirementsUseCountMemoryAndCapabilities(t *testing.T) {
	controller := NewController("", "")
	worker := readyNode("gpu-worker", 16, 64)
	worker.Info.GPU = GPUInfo{
		Vendor:       "NVIDIA",
		Model:        "A10",
		VRAMGB:       24,
		Count:        2,
		Capabilities: []string{"cuda", "tensor"},
		Runtime:      "CUDA",
	}
	controller.nodes[worker.Info.ID] = worker
	controller.sessions[worker.Info.ID] = newSession(nil)

	requirements := ResourceRequirements{
		CPUCores:        4,
		RAMGB:           8,
		GPURequired:     true,
		GPUCount:        2,
		VRAMGB:          16,
		AcceleratorType: "CUDA",
		GPUCapabilities: []string{"tensor"},
	}
	if got := controller.chooseNode(requirements); got != worker.Info.ID {
		t.Fatalf("compatible GPU worker was not selected: %q", got)
	}
	if got := controller.chooseNode(ResourceRequirements{GPUCount: 3}); got != "" {
		t.Fatalf("worker with insufficient GPU count was selected: %q", got)
	}
	if got := controller.chooseNode(ResourceRequirements{VRAMGB: 32}); got != "" {
		t.Fatalf("worker with insufficient VRAM was selected: %q", got)
	}
}

func TestReserveAndRollbackTracksAssignedJobs(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 2, 4)
	controller.sessions["worker-a"] = newSession(nil)
	job, err := controller.createJob(jobRequest{
		ID: "job-1", Command: "sum",
		Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
	})
	if err != nil {
		t.Fatal(err)
	}

	sess, ok := controller.reserveTask(job.ID, 1, "worker-a")
	if !ok || sess == nil {
		t.Fatal("task was not reserved")
	}
	if job.Status != JobRunning || job.NodeID != "worker-a" || len(controller.nodes["worker-a"].AssignedJobs) != 1 {
		t.Fatalf("assignment was not recorded: %#v %#v", job, controller.nodes["worker-a"])
	}

	controller.rollbackTask(1)
	if job.Status != JobQueued || job.NodeID != "" || len(controller.nodes["worker-a"].AssignedJobs) != 0 {
		t.Fatalf("rollback was not recorded: %#v %#v", job, controller.nodes["worker-a"])
	}
}

func TestReserveUsesRemainingWorkerCapacityForConcurrentJobs(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 2, 4)
	controller.sessions["worker-a"] = newSession(nil)
	for _, id := range []string{"job-1", "job-2"} {
		if _, err := controller.createJob(jobRequest{
			ID: id, Command: "sum",
			Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, ok := controller.reserveTask("job-1", 1, "worker-a"); !ok {
		t.Fatal("first task was not reserved")
	}
	if got := controller.chooseNode(ResourceRequirements{CPUCores: 1, RAMGB: 1}); got != "worker-a" {
		t.Fatalf("worker was not reusable while capacity remained: %q", got)
	}
	if _, ok := controller.reserveTask("job-2", 2, "worker-a"); !ok {
		t.Fatal("second task was not reserved")
	}
	if got := controller.chooseNode(ResourceRequirements{CPUCores: 1, RAMGB: 1}); got != "" {
		t.Fatalf("full worker was selected for a third task: %q", got)
	}
	if len(controller.nodes["worker-a"].AssignedJobs) != 2 {
		t.Fatalf("concurrent assignments were not tracked: %#v", controller.nodes["worker-a"])
	}
}

func TestImpossibleJobFailsOnlyAfterWorkersAreKnown(t *testing.T) {
	controller := NewController("", "")
	job, err := controller.createJob(jobRequest{
		ID: "job-1", Command: "sum",
		Requirements: ResourceRequirements{CPUCores: 128},
	})
	if err != nil {
		t.Fatal(err)
	}
	controller.scheduleOnce()
	if job.Status != JobQueued {
		t.Fatalf("job failed before any worker was known: %#v", job)
	}

	controller.nodes["worker-a"] = readyNode("worker-a", 2, 4)
	controller.scheduleOnce()
	if job.Status != JobFailed || job.Result == nil || job.Result.ErrorCode != "resource_requirements" {
		t.Fatalf("impossible job was not rejected clearly: %#v", job)
	}
}

package main

import (
	"encoding/base64"
	"testing"
)

func TestWorkloadPartitionersPreserveReductionSemantics(t *testing.T) {
	values := make([]byte, 256)
	for index := range values {
		values[index] = byte(index % 17)
	}

	for _, test := range []struct {
		name string
		def  workloadDefinition
		want int64
	}{
		{name: "sum", def: byteReductionWorkload{}, want: 2040},
		{name: "xor", def: byteReductionWorkload{xor: true}, want: 16},
	} {
		t.Run(test.name, func(t *testing.T) {
			parts, err := test.def.partition(values, 31)
			if err != nil {
				t.Fatal(err)
			}
			results := make([]int64, len(parts))
			for index, part := range parts {
				for _, value := range part.Payload {
					if test.name == "xor" {
						results[index] ^= int64(value)
					} else {
						results[index] += int64(value)
					}
				}
			}
			got, err := test.def.merge(results)
			if err != nil || got != test.want {
				t.Fatalf("merged result = %d, error = %v, want %d", got, err, test.want)
			}
		})
	}
}

func TestDotProductPartitionerPreservesVectorRanges(t *testing.T) {
	payload := []byte{
		4, 0, 0, 0,
		1, 0, 0, 0, 2, 0, 0, 0, 3, 0, 0, 0, 4, 0, 0, 0,
		5, 0, 0, 0, 6, 0, 0, 0, 7, 0, 0, 0, 8, 0, 0, 0,
	}
	definition := dotProductWorkload{}
	parts, err := definition.partition(payload, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 || parts[0].Units != 2 || parts[1].Units != 2 {
		t.Fatalf("unexpected partitions: %#v", parts)
	}
	got, err := definition.merge([]int64{17, 53})
	if err != nil || got != 70 {
		t.Fatalf("merged dot product = %d, error = %v", got, err)
	}
}

func TestAdaptiveSchedulerAssignsMoreUnitsToHigherCapacityWorker(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 8, 32)
	controller.nodes["worker-b"] = readyNode("worker-b", 2, 8)
	controller.sessions["worker-a"] = newSession(nil)
	controller.sessions["worker-b"] = newSession(nil)

	payload := make([]byte, 640)
	for index := range payload {
		payload[index] = 1
	}
	job, err := controller.createJob(jobRequest{
		ID: "adaptive-job", Command: "sum", Priority: 50,
		Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
		PayloadB64:   base64.StdEncoding.EncodeToString(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	controller.scheduleOnce()

	if job.Distribution.TotalPartitions < 2 {
		t.Fatalf("workload was not partitioned: %#v", job.Distribution)
	}
	units := map[string]uint64{}
	for _, partition := range job.Partitions {
		if partition.NodeID == "" {
			continue
		}
		units[partition.NodeID] += partition.Units
	}
	if units["worker-a"] <= units["worker-b"] {
		t.Fatalf("capacity-aware distribution was not proportional: %#v", units)
	}
}

func TestManualDistributionRequiresExactlyOneHundredPercent(t *testing.T) {
	controller := NewController("", "")
	_, err := controller.createJob(jobRequest{
		ID: "manual-invalid", Command: "sum", DistributionMode: DistributionManual,
		ManualAllocations: map[string]uint8{"worker-a": 60, "worker-b": 30},
	})
	if err == nil {
		t.Fatal("invalid manual allocation was accepted")
	}
}

func TestDisconnectedWorkerRequeuesOnlyIncompletePartitions(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 4, 8)
	controller.nodes["worker-b"] = readyNode("worker-b", 4, 8)
	controller.sessions["worker-a"] = newSession(nil)
	controller.sessions["worker-b"] = newSession(nil)

	payload := make([]byte, 512)
	for index := range payload {
		payload[index] = 1
	}
	job, err := controller.createJob(jobRequest{
		ID: "requeue-job", Command: "sum", Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
		PayloadB64: base64.StdEncoding.EncodeToString(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	controller.scheduleOnce()

	var completedFromB *Partition
	for index := range job.Partitions {
		if job.Partitions[index].NodeID == "worker-b" {
			completedFromB = &job.Partitions[index]
			break
		}
	}
	if completedFromB == nil {
		t.Fatal("test workload was not assigned to worker-b")
	}
	controller.applyResults([]TaskResult{{
		TaskID: completedFromB.TaskID, JobID: job.ID, Status: "COMPLETED", Value: int64(completedFromB.Units), NodeID: "worker-b",
	}})

	controller.mu.Lock()
	controller.requeueNodeJobsLocked("worker-a")
	controller.mu.Unlock()

	foundRequeued := false
	for _, partition := range job.Partitions {
		if partition.NodeID == "" && partition.State == PartitionRequeued {
			foundRequeued = true
			if partition.TaskID != 0 {
				t.Fatalf("requeued partition retained task id: %#v", partition)
			}
		}
	}
	if !foundRequeued || job.Distribution.CompletedPartitions == 0 || job.Status != JobQueued {
		t.Fatalf("disconnect did not preserve completed work and requeue incomplete work: %#v", job)
	}
}

func TestTinyWorkloadAvoidsPartitioning(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 8, 32)
	controller.nodes["worker-b"] = readyNode("worker-b", 4, 16)
	controller.sessions["worker-a"] = newSession(nil)
	controller.sessions["worker-b"] = newSession(nil)

	payload := []byte{1, 2, 3, 4, 5}
	job, err := controller.createJob(jobRequest{
		ID: "tiny-job", Command: "sum", Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
		PayloadB64: base64.StdEncoding.EncodeToString(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	controller.scheduleOnce()

	if job.Distribution.TotalPartitions != 1 {
		t.Fatalf("tiny workload was fragmented into %d partitions", job.Distribution.TotalPartitions)
	}
	if len(job.Partitions) != 1 {
		t.Fatalf("expected exactly 1 partition, got %d", len(job.Partitions))
	}
}

func TestEqualCapacityWorkersReceiveBalancedPartitions(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 4, 16)
	controller.nodes["worker-b"] = readyNode("worker-b", 4, 16)
	controller.sessions["worker-a"] = newSession(nil)
	controller.sessions["worker-b"] = newSession(nil)

	payload := make([]byte, 512)
	for index := range payload {
		payload[index] = 1
	}
	job, err := controller.createJob(jobRequest{
		ID: "equal-job", Command: "sum", Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
		PayloadB64: base64.StdEncoding.EncodeToString(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	controller.scheduleOnce()

	units := map[string]uint64{}
	for _, partition := range job.Partitions {
		if partition.NodeID != "" {
			units[partition.NodeID] += partition.Units
		}
	}
	if units["worker-a"] != units["worker-b"] {
		t.Fatalf("equal capacity workers received unequal work: %#v", units)
	}
}

func TestSchedulerAdaptsToExistingResourceAllocations(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 8, 32)
	controller.nodes["worker-b"] = readyNode("worker-b", 8, 32)
	controller.sessions["worker-a"] = newSession(nil)
	controller.sessions["worker-b"] = newSession(nil)

	// Pre-occupy worker-a with 7 cores out of 8
	controller.nodes["worker-a"].AllocatedCPUCores = 7
	updateNodeCapacity(controller.nodes["worker-a"])

	payload := make([]byte, 512)
	for index := range payload {
		payload[index] = 1
	}
	job, err := controller.createJob(jobRequest{
		ID: "adaptive-load-job", Command: "sum", Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
		PayloadB64: base64.StdEncoding.EncodeToString(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	controller.scheduleOnce()

	units := map[string]uint64{}
	for _, partition := range job.Partitions {
		if partition.NodeID != "" {
			units[partition.NodeID] += partition.Units
		}
	}
	if units["worker-b"] <= units["worker-a"] {
		t.Fatalf("heavily occupied worker received more/equal work than free worker: %#v", units)
	}
}

func TestDynamicSchedulingAssignsRemainingPartitionsWhenWorkerFinishesEarly(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 1, 4)
	controller.nodes["worker-b"] = readyNode("worker-b", 1, 4)
	controller.sessions["worker-a"] = newSession(nil)
	controller.sessions["worker-b"] = newSession(nil)

	payload := make([]byte, 512)
	for index := range payload {
		payload[index] = 1
	}
	job, err := controller.createJob(jobRequest{
		ID: "dynamic-job", Command: "sum", Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
		PayloadB64: base64.StdEncoding.EncodeToString(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	controller.scheduleOnce()

	// With 1 core each, 2 partitions should be ASSIGNED, and the rest QUEUED
	assignedCount := 0
	queuedCount := 0
	for _, p := range job.Partitions {
		if p.State == PartitionAssigned {
			assignedCount++
		} else if p.State == PartitionQueued {
			queuedCount++
		}
	}
	if assignedCount != 2 || queuedCount == 0 {
		t.Fatalf("expected 2 assigned and some queued, got assigned=%d queued=%d", assignedCount, queuedCount)
	}

	// Worker A completes its partition
	var partA *Partition
	for index := range job.Partitions {
		if job.Partitions[index].NodeID == "worker-a" && job.Partitions[index].State == PartitionAssigned {
			partA = &job.Partitions[index]
			break
		}
	}
	if partA == nil {
		t.Fatal("no partition on worker-a")
	}
	controller.applyResults([]TaskResult{{
		TaskID: partA.TaskID, JobID: job.ID, Status: "COMPLETED", Value: int64(partA.Units), NodeID: "worker-a",
	}})

	// After applyResults, triggerSchedule/scheduleOnce assigns the next partition to worker-a
	controller.scheduleOnce()

	newAssignedA := 0
	for _, p := range job.Partitions {
		if p.NodeID == "worker-a" && p.State == PartitionAssigned {
			newAssignedA++
		}
	}
	if newAssignedA != 1 {
		t.Fatalf("worker-a did not receive the next queued partition after finishing earlier: %#v", job.Partitions)
	}
}

func TestStaleResultRejectionAfterRequeue(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 2, 4)
	controller.sessions["worker-a"] = newSession(nil)

	payload := make([]byte, 256)
	for index := range payload {
		payload[index] = 1
	}
	job, err := controller.createJob(jobRequest{
		ID: "stale-test-job", Command: "sum", Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
		PayloadB64: base64.StdEncoding.EncodeToString(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	controller.scheduleOnce()

	oldTaskID := job.Partitions[0].TaskID
	if oldTaskID == 0 {
		t.Fatal("partition was not assigned a task ID")
	}

	// Requeue worker-a
	controller.mu.Lock()
	controller.requeueNodeJobsLocked("worker-a")
	controller.mu.Unlock()

	// An unsolicited / stale result with oldTaskID arrives
	controller.applyResults([]TaskResult{{
		TaskID: oldTaskID, JobID: job.ID, Status: "COMPLETED", Value: 12345, NodeID: "worker-a",
	}})

	// Job should still be QUEUED and partition REQUEUED, not completed by the stale result
	if job.Status == JobCompleted || job.Partitions[0].State == PartitionCompleted {
		t.Fatalf("stale result was accepted after requeue: %#v", job)
	}
}

func TestNonPartitionableWorkloadExecutionAndCompletion(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 2, 4)
	controller.sessions["worker-a"] = newSession(nil)

	isPartitionable := false
	job, err := controller.createJob(jobRequest{
		ID: "single-task-job", Command: "sum", Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
		PayloadB64:    base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
		Partitionable: &isPartitionable,
	})
	if err != nil {
		t.Fatal(err)
	}
	controller.scheduleOnce()

	if job.Distribution.Partitionable {
		t.Fatal("job was marked partitionable despite explicit override")
	}
	if len(job.Partitions) != 1 {
		t.Fatalf("expected 1 partition, got %d", len(job.Partitions))
	}

	taskID := job.Partitions[0].TaskID
	controller.applyResults([]TaskResult{{
		TaskID: taskID, JobID: job.ID, Status: "COMPLETED", Value: 6, NodeID: "worker-a", DurationUS: 100,
	}})

	if job.Status != JobCompleted || job.Result == nil || job.Result.Value != 6 {
		t.Fatalf("non-partitionable job did not complete with result: %#v", job)
	}
}

func TestControllerResourceAccountingReturnsToZeroAfterJobCompletion(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 4, 16)
	controller.sessions["worker-a"] = newSession(nil)

	payload := make([]byte, 256)
	job, err := controller.createJob(jobRequest{
		ID: "accounting-job", Command: "sum", Requirements: ResourceRequirements{CPUCores: 2, RAMGB: 4},
		PayloadB64: base64.StdEncoding.EncodeToString(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	controller.scheduleOnce()

	if controller.nodes["worker-a"].AllocatedCPUCores == 0 {
		t.Fatal("resources were not allocated during execution")
	}

	for _, p := range job.Partitions {
		controller.applyResults([]TaskResult{{
			TaskID: p.TaskID, JobID: job.ID, Status: "COMPLETED", Value: 0, NodeID: "worker-a",
		}})
	}

	if job.Status != JobCompleted {
		t.Fatalf("job did not complete: %#v", job)
	}
	if controller.nodes["worker-a"].AllocatedCPUCores != 0 || controller.nodes["worker-a"].AllocatedRAMGB != 0 {
		t.Fatalf("resources were not completely released: CPU=%d RAM=%d",
			controller.nodes["worker-a"].AllocatedCPUCores, controller.nodes["worker-a"].AllocatedRAMGB)
	}
}

func TestMalformedPartitionPayloads(t *testing.T) {
	dot := dotProductWorkload{}
	if _, err := dot.unitCount([]byte{}); err == nil {
		t.Fatal("empty dot_product payload was accepted")
	}
	if _, err := dot.unitCount([]byte{1, 0, 0, 0}); err == nil {
		t.Fatal("truncated dot_product payload was accepted")
	}
	byteRed := byteReductionWorkload{}
	if _, err := byteRed.unitCount([]byte{}); err == nil {
		t.Fatal("empty byte reduction payload was accepted")
	}
	if _, err := byteRed.merge([]int64{}); err == nil {
		t.Fatal("empty reduction merge was accepted")
	}
}

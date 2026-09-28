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

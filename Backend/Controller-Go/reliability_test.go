package main

import (
	"encoding/base64"
	"os"
	"testing"
)

func TestJobAndPartitionTransitions(t *testing.T) {
	if !validJobTransition(JobQueued, JobRunning) || !validJobTransition(JobRunning, JobCompleted) || !validJobTransition(JobPaused, JobQueued) {
		t.Fatal("expected supported job transitions to be valid")
	}
	if validJobTransition(JobCompleted, JobRunning) || validJobTransition(JobCancelled, JobQueued) {
		t.Fatal("terminal job states must not transition back into execution")
	}
	if !validPartitionTransition(PartitionRequeued, PartitionAssigned) || !validPartitionTransition(PartitionRunning, PartitionCompleted) {
		t.Fatal("expected supported partition transitions to be valid")
	}
	if validPartitionTransition(PartitionCompleted, PartitionRunning) || validPartitionTransition(PartitionCancelled, PartitionAssigned) {
		t.Fatal("terminal partition states must not be reassigned")
	}
}

func TestDuplicatePartitionResultIsIgnored(t *testing.T) {
	controller := NewController("", "")
	controller.nodes["worker-a"] = readyNode("worker-a", 2, 4)
	controller.sessions["worker-a"] = newSession(nil)
	job, err := controller.createJob(jobRequest{
		ID: "duplicate-result-job", Command: "sum",
		Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
		PayloadB64:   base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
	})
	if err != nil {
		t.Fatal(err)
	}
	controller.scheduleOnce()
	taskID := job.Partitions[0].TaskID
	result := TaskResult{TaskID: taskID, JobID: job.ID, Status: "COMPLETED", Value: 6, NodeID: "worker-a"}
	controller.applyResults([]TaskResult{result})
	controller.applyResults([]TaskResult{result})
	if job.Status != JobCompleted || job.Partitions[0].State != PartitionCompleted {
		t.Fatalf("duplicate result changed completed state: %#v", job)
	}
	if job.Distribution.CompletedPartitions != 1 || job.Distribution.CompletedUnits != 3 {
		t.Fatalf("duplicate result was counted twice: %#v", job.Distribution)
	}
}

func TestControllerStateSnapshotRecoversAndRequeuesActiveWork(t *testing.T) {
	statePath := t.TempDir() + string(os.PathSeparator) + "controller-state.json"
	controller := NewController("", "")
	controller.statePath = statePath
	controller.nodes["worker-a"] = readyNode("worker-a", 2, 4)
	controller.sessions["worker-a"] = newSession(nil)
	payload := make([]byte, 128)
	job, err := controller.createJob(jobRequest{
		ID: "recovery-job", Command: "sum",
		Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
		PayloadB64:   base64.StdEncoding.EncodeToString(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	controller.scheduleOnce()
	if job.Status != JobRunning {
		t.Fatalf("expected job to be running before snapshot, got %s", job.Status)
	}

	recovered := NewController("", "")
	recovered.statePath = statePath
	if err := recovered.loadState(); err != nil {
		t.Fatal(err)
	}
	recoveredJob := recovered.jobs[job.ID]
	if recoveredJob == nil || recoveredJob.Status != JobQueued {
		t.Fatalf("active job was not recovered as queued: %#v", recoveredJob)
	}
	for _, partition := range recoveredJob.Partitions {
		if partition.State == PartitionAssigned || partition.State == PartitionRunning || partition.TaskID != 0 || partition.NodeID != "" {
			t.Fatalf("active partition was not requeued: %#v", partition)
		}
	}
	if recovered.nodes["worker-a"].State != NodeOffline {
		t.Fatalf("persisted worker was incorrectly treated as connected: %s", recovered.nodes["worker-a"].State)
	}
}

func TestControllerPublishesStateEvents(t *testing.T) {
	controller := NewController("", "")
	_, events, unsubscribe := controller.subscribeEvents()
	defer unsubscribe()
	if _, err := controller.createJob(jobRequest{ID: "event-job", Command: "sum"}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Type != "job.created" || event.ID != "event-job" {
			t.Fatalf("unexpected event: %#v", event)
		}
	default:
		t.Fatal("expected job.created event")
	}
}

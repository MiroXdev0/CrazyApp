package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The repository's SQL files are schema/data fixtures, not a runtime Go
// database adapter. Until that adapter exists, the Controller uses this
// versioned atomic snapshot so restart recovery is durable and dependency-free.
type persistedState struct {
	Version      uint32            `json:"version"`
	Nodes        []NodeRecord      `json:"nodes"`
	Jobs         []persistedJob    `json:"jobs"`
	Artifacts    []ArtifactRecord  `json:"artifacts,omitempty"`
	AIPlans      []AIExecutionPlan `json:"ai_plans,omitempty"`
	AIExecutions []AIExecution     `json:"ai_executions,omitempty"`
}

type persistedJob struct {
	Job                   Job      `json:"job"`
	PartitionPayloadsB64  []string `json:"partition_payloads_base64,omitempty"`
	PartitionableOverride *bool    `json:"partitionable_override,omitempty"`
}

func (c *Controller) loadState() error {
	if strings.TrimSpace(c.statePath) == "" {
		return nil
	}
	data, err := os.ReadFile(c.statePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read controller state: %w", err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode controller state: %w", err)
	}
	if state.Version != 1 {
		return fmt.Errorf("unsupported controller state version %d", state.Version)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, storedNode := range state.Nodes {
		node := storedNode
		node.State = NodeOffline
		node.AssignedJobs = nil
		node.AllocatedCPUCores = 0
		node.AllocatedRAMGB = 0
		updateNodeCapacity(&node)
		c.nodes[node.Info.ID] = &node
	}
	for _, storedArtifact := range state.Artifacts {
		artifact := storedArtifact
		c.artifacts[artifact.ID] = &artifact
		if value, parseErr := strconv.ParseUint(strings.TrimPrefix(artifact.ID, "ART-"), 10, 64); parseErr == nil && value > c.nextArtifact {
			c.nextArtifact = value
		}
	}
	for _, storedPlan := range state.AIPlans {
		plan := cloneAIPlan(storedPlan)
		c.aiPlans[plan.ExecutionID] = &plan
		if value, parseErr := strconv.ParseUint(strings.TrimPrefix(plan.ExecutionID, "AI-"), 10, 64); parseErr == nil && value > c.nextAIExecution {
			c.nextAIExecution = value
		}
	}
	for _, storedExecution := range state.AIExecutions {
		execution := cloneAIExecution(storedExecution)
		c.aiExecutions[execution.ID] = &execution
		c.aiPlans[execution.Plan.ExecutionID] = &execution.Plan
		if value, parseErr := strconv.ParseUint(strings.TrimPrefix(execution.ID, "AI-"), 10, 64); parseErr == nil && value > c.nextAIExecution {
			c.nextAIExecution = value
		}
	}
	for _, stored := range state.Jobs {
		job := stored.Job
		job.partitionableOverride = stored.PartitionableOverride
		for index := range job.Partitions {
			if index >= len(stored.PartitionPayloadsB64) {
				return fmt.Errorf("job %s is missing partition payload %d", job.ID, index)
			}
			payload, decodeErr := base64.StdEncoding.DecodeString(stored.PartitionPayloadsB64[index])
			if decodeErr != nil {
				return fmt.Errorf("job %s has invalid partition payload: %w", job.ID, decodeErr)
			}
			job.Partitions[index].payload = payload
			if job.Partitions[index].State == PartitionAssigned || job.Partitions[index].State == PartitionRunning {
				job.Partitions[index].State = PartitionRequeued
				job.Partitions[index].TaskID = 0
				job.Partitions[index].NodeID = ""
				job.Partitions[index].Attempt++
				job.Partitions[index].UpdatedAt = time.Now()
			}
		}
		if job.Status == JobRunning {
			job.Status = JobQueued
			job.NodeID = ""
			job.NodeIDs = nil
		}
		c.jobs[job.ID] = &job
		if value, parseErr := strconv.ParseUint(strings.TrimPrefix(job.ID, "JOB-"), 10, 64); parseErr == nil && value > c.nextJob {
			c.nextJob = value
		}
		for _, partition := range job.Partitions {
			if partition.TaskID > c.nextTask {
				c.nextTask = partition.TaskID
			}
		}
		c.updateJobProgressLocked(&job)
	}
	return nil
}

func (c *Controller) persistLocked() {
	if strings.TrimSpace(c.statePath) == "" {
		return
	}
	state := persistedState{
		Version:      1,
		Nodes:        make([]NodeRecord, 0, len(c.nodes)),
		Jobs:         make([]persistedJob, 0, len(c.jobs)),
		Artifacts:    make([]ArtifactRecord, 0, len(c.artifacts)),
		AIPlans:      make([]AIExecutionPlan, 0, len(c.aiPlans)),
		AIExecutions: make([]AIExecution, 0, len(c.aiExecutions)),
	}
	for _, node := range c.nodes {
		copy := *node
		copy.AssignedJobs = append([]string(nil), node.AssignedJobs...)
		state.Nodes = append(state.Nodes, copy)
	}
	for _, job := range c.jobs {
		copy := cloneJob(*job)
		stored := persistedJob{Job: copy, PartitionableOverride: job.partitionableOverride, PartitionPayloadsB64: make([]string, len(job.Partitions))}
		for index, partition := range job.Partitions {
			stored.PartitionPayloadsB64[index] = base64.StdEncoding.EncodeToString(partition.payload)
		}
		state.Jobs = append(state.Jobs, stored)
	}
	for _, artifact := range c.artifacts {
		state.Artifacts = append(state.Artifacts, *artifact)
	}
	for _, plan := range c.aiPlans {
		state.AIPlans = append(state.AIPlans, cloneAIPlan(*plan))
	}
	for _, execution := range c.aiExecutions {
		state.AIExecutions = append(state.AIExecutions, cloneAIExecution(*execution))
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		logStateError(err)
		return
	}
	directory := filepath.Dir(c.statePath)
	if directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			logStateError(err)
			return
		}
	}
	temporary := c.statePath + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		logStateError(err)
		return
	}
	if err := os.Rename(temporary, c.statePath); err != nil {
		_ = os.Remove(temporary)
		logStateError(err)
	}
}

func logStateError(err error) {
	// Persistence failures must be visible but must not bring down worker
	// sessions or corrupt in-memory scheduling state.
	println("controller state persistence error:", err.Error())
}

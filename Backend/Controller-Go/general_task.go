package main

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

type TaskType string

const (
	TaskTypeProcess        TaskType = "PROCESS"
	TaskTypeScript         TaskType = "SCRIPT"
	TaskTypeNativeWorkload TaskType = "NATIVE_WORKLOAD"
	TaskTypeCommand        TaskType = "COMMAND"
)

type TaskArtifact struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   uint64 `json:"size"`
	SHA256 string `json:"sha256"`
	Kind   string `json:"kind,omitempty"`
}

type TaskTarget struct {
	OS                   string   `json:"os,omitempty"`
	Arch                 string   `json:"arch,omitempty"`
	RequiredRuntimes     []string `json:"required_runtimes,omitempty"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
	AllowedWorkerIDs     []string `json:"allowed_worker_ids,omitempty"`
	PreferredWorkerID    string   `json:"preferred_worker_id,omitempty"`
}

type RetryPolicy struct {
	MaxRetries uint32 `json:"max_retries"`
}

type ExecutionStrategy string

const (
	ExecutionSingle      ExecutionStrategy = "SINGLE"
	ExecutionBatch       ExecutionStrategy = "BATCH"
	ExecutionReplicated  ExecutionStrategy = "REPLICATED"
	ExecutionPartitioned ExecutionStrategy = "PARTITIONED"
	ExecutionDistributed ExecutionStrategy = "DISTRIBUTED"
)

type TaskPackageManifest struct {
	EntryPoint       string            `json:"entry_point,omitempty"`
	Runtime          string            `json:"runtime,omitempty"`
	Arguments        []string          `json:"arguments,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	OS               string            `json:"os,omitempty"`
	Arch             string            `json:"arch,omitempty"`
	RequiredRuntimes []string          `json:"required_runtimes,omitempty"`
}

// GeneralTaskSpec is the controller-owned, strongly typed description of a
// worker execution. Native workloads use Workload and continue through the
// existing C ABI path; other task types use the worker process executor.
type GeneralTaskSpec struct {
	Type             TaskType             `json:"type"`
	Version          string               `json:"version"`
	Executable       string               `json:"executable,omitempty"`
	Runtime          string               `json:"runtime,omitempty"`
	Script           string               `json:"script,omitempty"`
	Workload         string               `json:"workload,omitempty"`
	Arguments        []string             `json:"arguments,omitempty"`
	Environment      map[string]string    `json:"environment,omitempty"`
	WorkingDirectory string               `json:"working_directory,omitempty"`
	StdinB64         string               `json:"stdin_base64,omitempty"`
	InputArtifacts   []TaskArtifact       `json:"input_artifacts,omitempty"`
	OutputArtifacts  []TaskArtifact       `json:"output_artifacts,omitempty"`
	TimeoutMS        uint64               `json:"timeout_ms,omitempty"`
	StdoutLimitBytes uint64               `json:"stdout_limit_bytes,omitempty"`
	StderrLimitBytes uint64               `json:"stderr_limit_bytes,omitempty"`
	Requirements     ResourceRequirements `json:"requirements"`
	Target           TaskTarget           `json:"target,omitempty"`
	Retry            RetryPolicy          `json:"retry"`
	WorkloadKind     string               `json:"workload_kind,omitempty"`
	Strategy         ExecutionStrategy    `json:"strategy,omitempty"`
	RequiredWorkers  uint32               `json:"required_workers,omitempty"`
	Replicas         uint32               `json:"replicas,omitempty"`
	PackageManifest  *TaskPackageManifest `json:"package_manifest,omitempty"`
}

type GeneralTaskResult struct {
	Status          string         `json:"status"`
	ExitCode        *int32         `json:"exit_code,omitempty"`
	StdoutB64       string         `json:"stdout_base64,omitempty"`
	StderrB64       string         `json:"stderr_base64,omitempty"`
	StdoutTruncated bool           `json:"stdout_truncated"`
	StderrTruncated bool           `json:"stderr_truncated"`
	DurationUS      uint64         `json:"duration_us"`
	ErrorCode       string         `json:"error_code,omitempty"`
	Error           string         `json:"error,omitempty"`
	Attempt         uint32         `json:"attempt"`
	OutputArtifacts []TaskArtifact `json:"output_artifacts,omitempty"`
}

type taskRequest struct {
	ID      string          `json:"id,omitempty"`
	BatchID string          `json:"batch_id,omitempty"`
	Task    GeneralTaskSpec `json:"task"`
}

type taskBatchRequest struct {
	BatchID string        `json:"batch_id,omitempty"`
	Tasks   []taskRequest `json:"tasks"`
}

func (c *Controller) createGeneralTask(req taskRequest) (*Job, error) {
	spec := defaultTaskSpec(req.Task)
	if err := validateGeneralTask(spec); err != nil {
		return nil, err
	}
	id := req.ID
	if id == "" {
		id = fmt.Sprintf("TASK-%06d", atomic.AddUint64(&c.nextJob, 1))
	}
	now := time.Now()
	job := &Job{
		ID:           id,
		Command:      spec.Workload,
		Requirements: spec.Requirements,
		Status:       JobQueued,
		CreatedAt:    now,
		UpdatedAt:    now,
		Task:         &spec,
		BatchID:      req.BatchID,
	}
	if spec.Type == TaskTypeNativeWorkload {
		job.Command = spec.Workload
		job.PayloadB64 = spec.StdinB64
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, artifact := range spec.InputArtifacts {
		stored := c.artifacts[artifact.ID]
		if stored == nil {
			return nil, fmt.Errorf("input artifact %s was not found", artifact.ID)
		}
		if artifact.Size != 0 && artifact.Size != stored.Size {
			return nil, fmt.Errorf("input artifact %s size does not match metadata", artifact.ID)
		}
		if artifact.SHA256 != "" && !strings.EqualFold(artifact.SHA256, stored.SHA256) {
			return nil, fmt.Errorf("input artifact %s checksum does not match metadata", artifact.ID)
		}
	}
	if _, exists := c.jobs[id]; exists {
		return nil, errors.New("task already exists")
	}
	c.jobs[id] = job
	c.persistLocked()
	c.triggerSchedule()
	c.publishEvent(controllerEvent{Type: "task.created", Resource: "task", ID: job.ID, Status: string(job.Status)})
	return job, nil
}

func defaultTaskSpec(spec GeneralTaskSpec) GeneralTaskSpec {
	if spec.Version == "" {
		spec.Version = "1"
	}
	if spec.StdoutLimitBytes == 0 {
		spec.StdoutLimitBytes = 1 << 20
	}
	if spec.StderrLimitBytes == 0 {
		spec.StderrLimitBytes = 1 << 20
	}
	if spec.Strategy == "" {
		spec.Strategy = ExecutionSingle
	}
	return spec
}

func validateGeneralTask(spec GeneralTaskSpec) error {
	spec = defaultTaskSpec(spec)
	if spec.Type != TaskTypeProcess && spec.Type != TaskTypeScript && spec.Type != TaskTypeNativeWorkload && spec.Type != TaskTypeCommand {
		return fmt.Errorf("unsupported task type %q", spec.Type)
	}
	if spec.Version == "" {
		return errors.New("task version is required")
	}
	switch spec.Strategy {
	case ExecutionSingle, ExecutionBatch:
	default:
		return fmt.Errorf("execution strategy %q is reserved for a future workload adapter", spec.Strategy)
	}
	if spec.RequiredWorkers > 1 && spec.Strategy == ExecutionSingle {
		return errors.New("single execution cannot require multiple workers")
	}
	if spec.RequiredWorkers > 1 || spec.Replicas > 1 {
		return errors.New("multi-worker execution requires a workload adapter")
	}
	for _, value := range append(append([]string{}, spec.Arguments...), spec.WorkingDirectory, spec.Executable, spec.Runtime, spec.Script, spec.Workload) {
		if strings.IndexByte(value, 0) >= 0 {
			return errors.New("task fields cannot contain NUL bytes")
		}
	}
	for key, value := range spec.Environment {
		if key == "" || strings.IndexByte(key, 0) >= 0 || strings.IndexByte(value, 0) >= 0 {
			return errors.New("environment names and values must be non-empty and NUL-free")
		}
	}
	for _, artifact := range append(append([]TaskArtifact{}, spec.InputArtifacts...), spec.OutputArtifacts...) {
		if strings.TrimSpace(artifact.ID) == "" || strings.TrimSpace(artifact.Name) == "" {
			return errors.New("task artifacts require an id and name")
		}
		if strings.IndexByte(artifact.Name, 0) >= 0 || artifact.Name == "." || artifact.Name == ".." || strings.ContainsAny(artifact.Name, `/\\`) {
			return fmt.Errorf("artifact name %q must be a relative file name", artifact.Name)
		}
		if len(artifact.SHA256) > 0 && len(artifact.SHA256) != 64 {
			return fmt.Errorf("artifact %s has an invalid SHA-256", artifact.ID)
		}
	}
	switch spec.Type {
	case TaskTypeProcess, TaskTypeCommand:
		if strings.TrimSpace(spec.Executable) == "" {
			return errors.New("executable is required for process and command tasks")
		}
	case TaskTypeScript:
		if strings.TrimSpace(spec.Runtime) == "" || strings.TrimSpace(spec.Script) == "" {
			return errors.New("runtime and script are required for script tasks")
		}
	case TaskTypeNativeWorkload:
		if _, known := workloadDefinitionFor(spec.Workload); !known {
			return unsupportedWorkloadDefinition(spec.Workload)
		}
	}
	if spec.TimeoutMS > 24*60*60*1000 {
		return errors.New("task timeout cannot exceed 24 hours")
	}
	if spec.StdoutLimitBytes > 64<<20 || spec.StderrLimitBytes > 64<<20 {
		return errors.New("stdout/stderr capture limits cannot exceed 64 MiB")
	}
	return nil
}

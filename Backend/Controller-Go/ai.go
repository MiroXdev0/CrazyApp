package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

type AIExecutionStrategy string

const (
	AIStrategySingle               AIExecutionStrategy = "SINGLE"
	AIStrategyReplicated           AIExecutionStrategy = "REPLICATED"
	AIStrategyDataParallel         AIExecutionStrategy = "DATA_PARALLEL"
	AIStrategyTensorParallel       AIExecutionStrategy = "TENSOR_PARALLEL"
	AIStrategyPipelineParallel     AIExecutionStrategy = "PIPELINE_PARALLEL"
	AIStrategyDistributedInference AIExecutionStrategy = "DISTRIBUTED_INFERENCE"
	AIStrategyDistributedTraining  AIExecutionStrategy = "DISTRIBUTED_TRAINING"
	AIStrategyBatchInference       AIExecutionStrategy = "BATCH_INFERENCE"
	AIStrategyDistributedProcess   AIExecutionStrategy = "DISTRIBUTED_PROCESS"
)

type AIModel struct {
	Name      string         `json:"name,omitempty"`
	Runtime   string         `json:"runtime,omitempty"`
	Framework string         `json:"framework,omitempty"`
	Precision string         `json:"precision,omitempty"`
	Artifacts []TaskArtifact `json:"artifacts,omitempty"`
	Shards    []AIModelShard `json:"shards,omitempty"`
}

type AIModelShard struct {
	ID               string  `json:"id"`
	ArtifactID       string  `json:"artifact_id"`
	Kind             string  `json:"kind,omitempty"`
	Size             uint64  `json:"size,omitempty"`
	SHA256           string  `json:"sha256,omitempty"`
	Runtime          string  `json:"runtime,omitempty"`
	LayerStart       *uint32 `json:"layer_start,omitempty"`
	LayerEnd         *uint32 `json:"layer_end,omitempty"`
	Precision        string  `json:"precision,omitempty"`
	ComputationShard bool    `json:"computation_shard"`
}

type AICheckpointSpec struct {
	Enabled         bool           `json:"enabled"`
	IntervalSeconds uint64         `json:"interval_seconds,omitempty"`
	OutputArtifacts []TaskArtifact `json:"output_artifacts,omitempty"`
}

type AIWorkloadSpec struct {
	Type             string               `json:"type"`
	Adapter          string               `json:"adapter,omitempty"`
	Runtime          string               `json:"runtime,omitempty"`
	Framework        string               `json:"framework,omitempty"`
	EntryPoint       string               `json:"entry_point"`
	Arguments        []string             `json:"arguments,omitempty"`
	Environment      map[string]string    `json:"environment,omitempty"`
	Model            AIModel              `json:"model"`
	DatasetArtifacts []TaskArtifact       `json:"dataset_artifacts,omitempty"`
	Requirements     ResourceRequirements `json:"requirements"`
	Target           TaskTarget           `json:"target,omitempty"`
	WorkerCount      uint32               `json:"worker_count,omitempty"`
	Strategy         AIExecutionStrategy  `json:"strategy"`
	Precision        string               `json:"precision,omitempty"`
	BatchSize        uint32               `json:"batch_size,omitempty"`
	Checkpoint       *AICheckpointSpec    `json:"checkpoint,omitempty"`
	OutputArtifacts  []TaskArtifact       `json:"output_artifacts,omitempty"`
}

type AIWorkerAssignment struct {
	WorkerID     string   `json:"worker_id"`
	Rank         uint32   `json:"rank"`
	WorldSize    uint32   `json:"world_size"`
	GPUIndex     int      `json:"gpu_index"`
	GPUCount     uint32   `json:"gpu_count"`
	VRAMGB       uint64   `json:"vram_gb"`
	Accelerator  string   `json:"accelerator,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	OS           string   `json:"os,omitempty"`
	Arch         string   `json:"arch,omitempty"`
}

type AICommunicationPlan struct {
	Mode               string            `json:"mode"`
	LeaderWorkerID     string            `json:"leader_worker_id,omitempty"`
	WorldSize          uint32            `json:"world_size"`
	PeerEndpoints      map[string]string `json:"peer_endpoints,omitempty"`
	ControllerMediated bool              `json:"controller_mediated"`
}

type AITaskLaunch struct {
	TaskID      string            `json:"task_id,omitempty"`
	WorkerID    string            `json:"worker_id"`
	Rank        uint32            `json:"rank"`
	Environment map[string]string `json:"environment"`
	Task        GeneralTaskSpec   `json:"task"`
}

type AIExecutionPlan struct {
	ExecutionID             string               `json:"execution_id"`
	GroupID                 string               `json:"group_id"`
	Adapter                 string               `json:"adapter"`
	Strategy                AIExecutionStrategy  `json:"strategy"`
	Workload                AIWorkloadSpec       `json:"workload"`
	Status                  string               `json:"status"`
	WorldSize               uint32               `json:"world_size"`
	LeaderWorkerID          string               `json:"leader_worker_id,omitempty"`
	ModelArtifactIDs        []string             `json:"model_artifact_ids,omitempty"`
	DatasetArtifactIDs      []string             `json:"dataset_artifact_ids,omitempty"`
	InputDistribution       string               `json:"input_distribution"`
	ComputationPartitioning string               `json:"computation_partitioning"`
	Communication           AICommunicationPlan  `json:"communication"`
	Workers                 []AIWorkerAssignment `json:"workers"`
	Launches                []AITaskLaunch       `json:"launches"`
	CreatedAt               time.Time            `json:"created_at"`
	Error                   string               `json:"error,omitempty"`
}

type AIExecution struct {
	ID        string          `json:"id"`
	Status    string          `json:"status"`
	Plan      AIExecutionPlan `json:"plan"`
	TaskIDs   []string        `json:"task_ids"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type AIAdapter interface {
	Name() string
	Validate(AIWorkloadSpec) error
	BuildLaunch(AIWorkloadSpec, AIExecutionPlan, AIWorkerAssignment) GeneralTaskSpec
}

type distributedProcessAIAdapter struct{}

func (distributedProcessAIAdapter) Name() string { return "distributed-process" }

func (distributedProcessAIAdapter) Validate(spec AIWorkloadSpec) error {
	if strings.TrimSpace(spec.EntryPoint) == "" {
		return errors.New("AI workload entry_point is required")
	}
	switch spec.Strategy {
	case AIStrategySingle:
		if spec.WorkerCount != 1 {
			return errors.New("SINGLE AI execution requires worker_count=1")
		}
	case AIStrategyDistributedProcess:
		if spec.WorkerCount < 2 {
			return errors.New("DISTRIBUTED_PROCESS requires at least two workers")
		}
	default:
		return fmt.Errorf("AI strategy %q is not implemented by the distributed-process adapter", spec.Strategy)
	}
	if len(spec.OutputArtifacts) > 0 || (spec.Checkpoint != nil && len(spec.Checkpoint.OutputArtifacts) > 0) {
		return errors.New("AI output artifact collection is not implemented by the distributed-process adapter")
	}
	if spec.Checkpoint != nil && spec.Checkpoint.Enabled {
		return errors.New("AI checkpointing is not implemented by the distributed-process adapter")
	}
	for _, shard := range spec.Model.Shards {
		if shard.ComputationShard {
			return errors.New("AI computation/model sharding is not implemented by the distributed-process adapter")
		}
	}
	return nil
}

func (adapter distributedProcessAIAdapter) BuildLaunch(spec AIWorkloadSpec, plan AIExecutionPlan, worker AIWorkerAssignment) GeneralTaskSpec {
	environment := cloneStringMap(spec.Environment)
	if environment == nil {
		environment = make(map[string]string)
	}
	reserved := map[string]string{
		"NODREN_AI_ADAPTER":       adapter.Name(),
		"NODREN_AI_EXECUTION_ID":  plan.ExecutionID,
		"NODREN_AI_GROUP_ID":      plan.GroupID,
		"NODREN_AI_RANK":          fmt.Sprintf("%d", worker.Rank),
		"NODREN_AI_WORLD_SIZE":    fmt.Sprintf("%d", worker.WorldSize),
		"NODREN_AI_LEADER_WORKER": plan.LeaderWorkerID,
		"NODREN_AI_STRATEGY":      string(plan.Strategy),
	}
	for key, value := range reserved {
		environment[key] = value
	}
	inputArtifacts := append([]TaskArtifact(nil), spec.Model.Artifacts...)
	inputArtifacts = append(inputArtifacts, spec.DatasetArtifacts...)
	target := spec.Target
	target.AllowedWorkerIDs = []string{worker.WorkerID}
	target.PreferredWorkerID = worker.WorkerID
	launch := GeneralTaskSpec{
		Version:         "1",
		Arguments:       append([]string(nil), spec.Arguments...),
		Environment:     environment,
		InputArtifacts:  inputArtifacts,
		Requirements:    spec.Requirements,
		Target:          target,
		WorkloadKind:    "ai-distributed-process",
		Strategy:        ExecutionSingle,
		RequiredWorkers: 1,
		Replicas:        1,
	}
	if spec.Runtime != "" {
		launch.Type = TaskTypeScript
		launch.Runtime = spec.Runtime
		launch.Script = spec.EntryPoint
	} else {
		launch.Type = TaskTypeProcess
		launch.Executable = spec.EntryPoint
		if !strings.ContainsAny(launch.Executable, `/\\`) {
			launch.Executable = "./" + launch.Executable
		}
	}
	return launch
}

var aiAdapters = map[string]AIAdapter{
	"distributed-process": distributedProcessAIAdapter{},
}

func defaultAIWorkload(spec AIWorkloadSpec) AIWorkloadSpec {
	if spec.Type == "" {
		spec.Type = "AI"
	}
	if spec.Adapter == "" {
		spec.Adapter = "distributed-process"
	}
	if spec.Runtime == "" {
		spec.Runtime = spec.Model.Runtime
	}
	if spec.Framework == "" {
		spec.Framework = spec.Model.Framework
	}
	if spec.Strategy == "" {
		spec.Strategy = AIStrategySingle
	}
	spec.Strategy = AIExecutionStrategy(strings.ReplaceAll(strings.ToUpper(string(spec.Strategy)), "-", "_"))
	if spec.WorkerCount == 0 {
		spec.WorkerCount = 1
	}
	if spec.Requirements.CPUCores == 0 {
		spec.Requirements.CPUCores = 1
	}
	if spec.Requirements.RAMGB == 0 {
		spec.Requirements.RAMGB = 1
	}
	if spec.Requirements.GPUCount > 0 {
		spec.Requirements.GPURequired = true
	}
	if spec.Precision == "" {
		spec.Precision = spec.Model.Precision
	}
	return spec
}

func validateAIWorkload(spec AIWorkloadSpec) (AIWorkloadSpec, AIAdapter, error) {
	spec = defaultAIWorkload(spec)
	if !strings.EqualFold(spec.Type, "AI") {
		return spec, nil, fmt.Errorf("unsupported AI workload type %q", spec.Type)
	}
	adapter := aiAdapters[spec.Adapter]
	if adapter == nil {
		return spec, nil, fmt.Errorf("AI adapter %q is not registered", spec.Adapter)
	}
	if err := adapter.Validate(spec); err != nil {
		return spec, nil, err
	}
	if spec.Strategy == AIStrategySingle && spec.WorkerCount != 1 {
		return spec, nil, errors.New("SINGLE AI execution cannot use multiple workers")
	}
	return spec, adapter, nil
}

func (c *Controller) validateAIArtifactsLocked(spec AIWorkloadSpec) error {
	check := func(artifact TaskArtifact) error {
		stored := c.artifacts[artifact.ID]
		if stored == nil {
			return fmt.Errorf("AI artifact %s was not found", artifact.ID)
		}
		if artifact.Size != 0 && artifact.Size != stored.Size {
			return fmt.Errorf("AI artifact %s size does not match metadata", artifact.ID)
		}
		if artifact.SHA256 != "" && !strings.EqualFold(artifact.SHA256, stored.SHA256) {
			return fmt.Errorf("AI artifact %s checksum does not match metadata", artifact.ID)
		}
		return nil
	}
	for _, artifact := range spec.Model.Artifacts {
		if err := check(artifact); err != nil {
			return err
		}
	}
	for _, artifact := range spec.DatasetArtifacts {
		if err := check(artifact); err != nil {
			return err
		}
	}
	for _, shard := range spec.Model.Shards {
		if shard.ArtifactID == "" {
			return fmt.Errorf("model shard %s requires artifact_id", shard.ID)
		}
		if err := check(TaskArtifact{ID: shard.ArtifactID, Size: shard.Size, SHA256: shard.SHA256}); err != nil {
			return err
		}
		found := false
		for _, artifact := range spec.Model.Artifacts {
			if artifact.ID == shard.ArtifactID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("model shard %s must reference an artifact listed in model.artifacts", shard.ID)
		}
	}
	return nil
}

func aiWorkerEligible(node *NodeRecord, spec AIWorkloadSpec) bool {
	if node == nil || (node.State != NodeReady && node.State != NodeBusy) {
		return false
	}
	if !containsFold(spec.Target.AllowedWorkerIDs, node.Info.ID) && len(spec.Target.AllowedWorkerIDs) > 0 {
		return false
	}
	if spec.Target.OS != "" && !strings.EqualFold(spec.Target.OS, node.Info.OS) {
		return false
	}
	if spec.Target.Arch != "" && !strings.EqualFold(spec.Target.Arch, node.Info.Arch) {
		return false
	}
	for _, runtime := range spec.Target.RequiredRuntimes {
		if !containsFold(node.Info.Runtimes, runtime) {
			return false
		}
	}
	for _, capability := range spec.Target.RequiredCapabilities {
		if !containsFold(node.Info.Capabilities, capability) {
			return false
		}
	}
	if spec.Runtime != "" && !strings.ContainsAny(spec.Runtime, `/\\`) && !containsFold(node.Info.Runtimes, spec.Runtime) {
		return false
	}
	return true
}

func (c *Controller) selectAIWorkersLocked(spec AIWorkloadSpec) ([]AIWorkerAssignment, error) {
	workers := make(map[string]*NodeRecord, len(c.nodes))
	for id, node := range c.nodes {
		copy := *node
		workers[id] = &copy
	}
	selected := make([]AIWorkerAssignment, 0, spec.WorkerCount)
	used := make(map[string]bool)
	for rank := uint32(0); rank < spec.WorkerCount; rank++ {
		var best *NodeRecord
		bestID := ""
		bestCapacity := -1.0
		for id, node := range workers {
			if used[id] || c.sessions[id] == nil || !aiWorkerEligible(node, spec) || !c.canFit(node, spec.Requirements) {
				continue
			}
			capacity := effectiveCapacity(node)
			if best == nil || capacity > bestCapacity || (capacity == bestCapacity && id < bestID) {
				best = node
				bestID = id
				bestCapacity = capacity
			}
		}
		if best == nil {
			return nil, fmt.Errorf("AI worker group needs %d compatible workers; only %d could be selected", spec.WorkerCount, len(selected))
		}
		used[bestID] = true
		gpuCount := availableGPUCount(best.Info)
		gpuIndex := 0
		if gpuCount > 0 {
			gpuIndex = int(rank % gpuCount)
		}
		selected = append(selected, AIWorkerAssignment{
			WorkerID:     bestID,
			Rank:         rank,
			WorldSize:    spec.WorkerCount,
			GPUIndex:     gpuIndex,
			GPUCount:     gpuCount,
			VRAMGB:       best.Info.GPU.VRAMGB,
			Accelerator:  best.Info.GPU.Vendor,
			Capabilities: append([]string(nil), best.Info.GPU.Capabilities...),
			OS:           best.Info.OS,
			Arch:         best.Info.Arch,
		})
		best.AllocatedCPUCores += requiredCPUCores(spec.Requirements)
		best.AllocatedRAMGB += requiredRAMGB(spec.Requirements)
		best.AllocatedGPUCount += requiredGPUCount(spec.Requirements)
		best.AllocatedVRAMGB += spec.Requirements.VRAMGB
		updateNodeCapacity(best)
	}
	return selected, nil
}

func (c *Controller) buildAIPlan(spec AIWorkloadSpec) (AIWorkloadSpec, AIExecutionPlan, error) {
	spec, adapter, err := validateAIWorkload(spec)
	if err != nil {
		return spec, AIExecutionPlan{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateAIArtifactsLocked(spec); err != nil {
		return spec, AIExecutionPlan{}, err
	}
	workers, err := c.selectAIWorkersLocked(spec)
	if err != nil {
		return spec, AIExecutionPlan{}, err
	}
	sequence := atomic.AddUint64(&c.nextAIExecution, 1)
	executionID := fmt.Sprintf("AI-%06d", sequence)
	groupID := fmt.Sprintf("AIG-%06d", sequence)
	plan := AIExecutionPlan{
		ExecutionID:             executionID,
		GroupID:                 groupID,
		Adapter:                 adapter.Name(),
		Strategy:                spec.Strategy,
		Workload:                cloneAIWorkloadSpec(spec),
		Status:                  "PLANNED",
		WorldSize:               spec.WorkerCount,
		LeaderWorkerID:          workers[0].WorkerID,
		InputDistribution:       "replicated_artifacts",
		ComputationPartitioning: "runtime_defined_rank_processes",
		Communication: AICommunicationPlan{
			Mode:               "environment_only",
			LeaderWorkerID:     workers[0].WorkerID,
			WorldSize:          spec.WorkerCount,
			PeerEndpoints:      map[string]string{},
			ControllerMediated: true,
		},
		Workers:   workers,
		CreatedAt: time.Now().UTC(),
	}
	for _, artifact := range spec.Model.Artifacts {
		plan.ModelArtifactIDs = append(plan.ModelArtifactIDs, artifact.ID)
	}
	for _, artifact := range spec.DatasetArtifacts {
		plan.DatasetArtifactIDs = append(plan.DatasetArtifactIDs, artifact.ID)
	}
	for _, worker := range workers {
		plan.Launches = append(plan.Launches, AITaskLaunch{
			WorkerID:    worker.WorkerID,
			Rank:        worker.Rank,
			Environment: cloneStringMap(spec.Environment),
			Task:        adapter.BuildLaunch(spec, plan, worker),
		})
	}
	return spec, plan, nil
}

func (c *Controller) createAIPlan(spec AIWorkloadSpec) (*AIExecutionPlan, error) {
	_, plan, err := c.buildAIPlan(spec)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.aiPlans[plan.ExecutionID] = &plan
	c.persistLocked()
	copy := cloneAIPlan(plan)
	c.mu.Unlock()
	return &copy, nil
}

func (c *Controller) createAIExecution(spec AIWorkloadSpec) (*AIExecution, error) {
	_, plan, err := c.buildAIPlan(spec)
	if err != nil {
		return nil, err
	}
	taskIDs := make([]string, 0, len(plan.Launches))
	for index := range plan.Launches {
		launch := &plan.Launches[index]
		taskID := fmt.Sprintf("%s-RANK-%03d", plan.ExecutionID, launch.Rank)
		launch.TaskID = taskID
		if _, err := c.createGeneralTask(taskRequest{ID: taskID, BatchID: plan.GroupID, Task: launch.Task}); err != nil {
			c.mu.Lock()
			for _, createdID := range taskIDs {
				if job := c.jobs[createdID]; job != nil && !isTerminalJob(job.Status) {
					_ = c.cancelJobLocked(job, "AI execution group creation failed")
				}
			}
			c.mu.Unlock()
			return nil, err
		}
		taskIDs = append(taskIDs, taskID)
	}
	now := time.Now().UTC()
	execution := &AIExecution{ID: plan.ExecutionID, Status: "QUEUED", Plan: plan, TaskIDs: taskIDs, CreatedAt: now, UpdatedAt: now}
	execution.Plan.Status = execution.Status
	c.mu.Lock()
	c.aiPlans[plan.ExecutionID] = &execution.Plan
	c.aiExecutions[execution.ID] = execution
	c.persistLocked()
	copy := cloneAIExecution(*execution)
	c.mu.Unlock()
	return &copy, nil
}

func (c *Controller) refreshAIExecutionLocked(execution *AIExecution) {
	if execution == nil {
		return
	}
	completed := 0
	running := false
	failed := false
	cancelled := 0
	for _, taskID := range execution.TaskIDs {
		job := c.jobs[taskID]
		if job == nil {
			failed = true
			continue
		}
		switch job.Status {
		case JobCompleted:
			completed++
		case JobRunning:
			running = true
		case JobFailed, JobTimedOut:
			failed = true
		case JobCancelled:
			cancelled++
		}
	}
	switch {
	case failed:
		execution.Status = "FAILED"
	case completed == len(execution.TaskIDs) && len(execution.TaskIDs) > 0:
		execution.Status = "COMPLETED"
	case cancelled == len(execution.TaskIDs) && len(execution.TaskIDs) > 0:
		execution.Status = "CANCELLED"
	case running:
		execution.Status = "RUNNING"
	default:
		execution.Status = "QUEUED"
	}
	execution.Plan.Status = execution.Status
	execution.UpdatedAt = time.Now().UTC()
}

func cloneAIPlan(plan AIExecutionPlan) AIExecutionPlan {
	copy := plan
	copy.Workload = cloneAIWorkloadSpec(plan.Workload)
	copy.ModelArtifactIDs = append([]string(nil), plan.ModelArtifactIDs...)
	copy.DatasetArtifactIDs = append([]string(nil), plan.DatasetArtifactIDs...)
	copy.Workers = append([]AIWorkerAssignment(nil), plan.Workers...)
	for index := range copy.Workers {
		copy.Workers[index].Capabilities = append([]string(nil), plan.Workers[index].Capabilities...)
	}
	copy.Communication.PeerEndpoints = map[string]string{}
	for key, value := range plan.Communication.PeerEndpoints {
		copy.Communication.PeerEndpoints[key] = value
	}
	copy.Launches = make([]AITaskLaunch, len(plan.Launches))
	for index, launch := range plan.Launches {
		copy.Launches[index] = launch
		copy.Launches[index].Environment = cloneStringMap(launch.Environment)
		copy.Launches[index].Task = cloneGeneralTaskSpec(launch.Task)
	}
	return copy
}

func cloneAIWorkloadSpec(spec AIWorkloadSpec) AIWorkloadSpec {
	copy := spec
	copy.Arguments = append([]string(nil), spec.Arguments...)
	copy.Environment = cloneStringMap(spec.Environment)
	copy.Model.Artifacts = append([]TaskArtifact(nil), spec.Model.Artifacts...)
	copy.Model.Shards = append([]AIModelShard(nil), spec.Model.Shards...)
	copy.DatasetArtifacts = append([]TaskArtifact(nil), spec.DatasetArtifacts...)
	copy.Requirements.GPUCapabilities = append([]string(nil), spec.Requirements.GPUCapabilities...)
	copy.Target.RequiredRuntimes = append([]string(nil), spec.Target.RequiredRuntimes...)
	copy.Target.RequiredCapabilities = append([]string(nil), spec.Target.RequiredCapabilities...)
	copy.Target.AllowedWorkerIDs = append([]string(nil), spec.Target.AllowedWorkerIDs...)
	copy.OutputArtifacts = append([]TaskArtifact(nil), spec.OutputArtifacts...)
	if spec.Checkpoint != nil {
		checkpoint := *spec.Checkpoint
		checkpoint.OutputArtifacts = append([]TaskArtifact(nil), spec.Checkpoint.OutputArtifacts...)
		copy.Checkpoint = &checkpoint
	}
	return copy
}

func cloneAIExecution(execution AIExecution) AIExecution {
	copy := execution
	copy.Plan = cloneAIPlan(execution.Plan)
	copy.TaskIDs = append([]string(nil), execution.TaskIDs...)
	return copy
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func cloneGeneralTaskSpec(spec GeneralTaskSpec) GeneralTaskSpec {
	copy := spec
	copy.Arguments = append([]string(nil), spec.Arguments...)
	copy.Environment = cloneStringMap(spec.Environment)
	copy.InputArtifacts = append([]TaskArtifact(nil), spec.InputArtifacts...)
	copy.OutputArtifacts = append([]TaskArtifact(nil), spec.OutputArtifacts...)
	copy.Target.RequiredRuntimes = append([]string(nil), spec.Target.RequiredRuntimes...)
	copy.Target.RequiredCapabilities = append([]string(nil), spec.Target.RequiredCapabilities...)
	copy.Target.AllowedWorkerIDs = append([]string(nil), spec.Target.AllowedWorkerIDs...)
	if spec.PackageManifest != nil {
		manifestCopy := *spec.PackageManifest
		manifestCopy.Arguments = append([]string(nil), spec.PackageManifest.Arguments...)
		manifestCopy.RequiredRuntimes = append([]string(nil), spec.PackageManifest.RequiredRuntimes...)
		manifestCopy.Environment = cloneStringMap(spec.PackageManifest.Environment)
		copy.PackageManifest = &manifestCopy
	}
	return copy
}

func (c *Controller) listAIWorkers() []NodeRecord {
	c.mu.RLock()
	defer c.mu.RUnlock()
	workers := make([]NodeRecord, 0, len(c.nodes))
	for _, node := range c.nodes {
		copy := *node
		copy.AssignedJobs = append([]string(nil), node.AssignedJobs...)
		updateNodeCapacity(&copy)
		workers = append(workers, copy)
	}
	sort.Slice(workers, func(i, j int) bool { return workers[i].Info.ID < workers[j].Info.ID })
	return workers
}

func (c *Controller) handleAIWorkers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, c.listAIWorkers())
}

func (c *Controller) handleAIPlans(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		c.mu.RLock()
		plans := make([]AIExecutionPlan, 0, len(c.aiPlans))
		for _, plan := range c.aiPlans {
			plans = append(plans, cloneAIPlan(*plan))
		}
		c.mu.RUnlock()
		sort.Slice(plans, func(i, j int) bool { return plans[i].ExecutionID < plans[j].ExecutionID })
		writeJSON(w, http.StatusOK, plans)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var spec AIWorkloadSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	plan, err := c.createAIPlan(spec)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, plan)
}

func (c *Controller) handleAIPlan(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/ai/plans/")
	if r.Method != http.MethodGet || path == "" || strings.Contains(path, "/") {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	c.mu.RLock()
	plan := c.aiPlans[path]
	if plan == nil {
		if execution := c.aiExecutions[path]; execution != nil {
			copy := cloneAIPlan(execution.Plan)
			c.mu.RUnlock()
			writeJSON(w, http.StatusOK, copy)
			return
		}
		c.mu.RUnlock()
		http.NotFound(w, r)
		return
	}
	copy := cloneAIPlan(*plan)
	c.mu.RUnlock()
	writeJSON(w, http.StatusOK, copy)
}

func (c *Controller) handleAIExecutions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		c.mu.Lock()
		executions := make([]AIExecution, 0, len(c.aiExecutions))
		for _, execution := range c.aiExecutions {
			c.refreshAIExecutionLocked(execution)
			executions = append(executions, cloneAIExecution(*execution))
		}
		c.mu.Unlock()
		sort.Slice(executions, func(i, j int) bool { return executions[i].ID < executions[j].ID })
		writeJSON(w, http.StatusOK, executions)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var spec AIWorkloadSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	execution, err := c.createAIExecution(spec)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, execution)
}

func (c *Controller) handleAIExecution(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/ai/executions/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		c.mu.Lock()
		execution := c.aiExecutions[id]
		if execution == nil {
			c.mu.Unlock()
			http.NotFound(w, r)
			return
		}
		c.refreshAIExecutionLocked(execution)
		copy := cloneAIExecution(*execution)
		c.mu.Unlock()
		writeJSON(w, http.StatusOK, copy)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost || parts[1] != "cancel" {
		http.Error(w, "unsupported AI execution operation", http.StatusNotImplemented)
		return
	}
	c.mu.Lock()
	execution := c.aiExecutions[id]
	if execution == nil {
		c.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	for _, taskID := range execution.TaskIDs {
		if job := c.jobs[taskID]; job != nil && !isTerminalJob(job.Status) {
			_ = c.cancelJobLocked(job, "AI execution cancelled by user")
		}
	}
	c.refreshAIExecutionLocked(execution)
	c.persistLocked()
	copy := cloneAIExecution(*execution)
	c.mu.Unlock()
	c.triggerSchedule()
	writeJSON(w, http.StatusOK, copy)
}

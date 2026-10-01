package main

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func runCLI(args []string) error {
	if len(args) == 0 {
		printHelp()
		return nil
	}
	switch strings.ToLower(args[0]) {
	case "help", "--help", "-h":
		printHelp()
		return nil
	case "version", "--version", "-v":
		fmt.Printf("Nodren %s\n", nodrenVersion)
		return nil
	case "status", "state":
		return cliStatus()
	case "controller":
		return cliController(args[1:])
	case "workers":
		return cliWorkers(args[1:])
	case "devices", "nodes":
		return cliDevices()
	case "info":
		return cliInfo(args[1:])
	case "ping":
		return cliPing(args[1:])
	case "jobs", "job":
		return cliJobsCommand(args[1:])
	case "tasks", "task":
		return cliTasksCommand(args[1:])
	case "artifacts", "artifact":
		return cliArtifactsCommand(args[1:])
	case "ai":
		return cliAI(args[1:])
	case "run":
		return cliRun(args[1:])
	case "distribution":
		return cliDistribution(args[1:])
	case "check":
		return cliCheck(args[1:])
	case "doctor":
		return cliDoctor()
	case "config":
		return cliConfig(args[1:])
	case "events", "monitor":
		return newCLIClient().events()
	case "start":
		return fmt.Errorf("start is the Controller process entrypoint; run 'nodren start' without a running Controller")
	case "stop", "restart", "reload":
		return fmt.Errorf("Controller process lifecycle is local-process managed; stop/restart is not available through the HTTP control API")
	case "logs", "log":
		return fmt.Errorf("centralized log retrieval is not available through the Controller API")
	default:
		return fmt.Errorf("unknown command %q; run 'nodren help'", args[0])
	}
}

func cliController(args []string) error {
	if len(args) == 0 || strings.EqualFold(args[0], "info") || strings.EqualFold(args[0], "status") {
		health, err := newCLIClient().health()
		if err != nil {
			return err
		}
		api := newCLIClient()
		fmt.Printf("Controller  ONLINE\nService     %s\nVersion     %s\nEndpoint    %s\nWorkers     %d\n", health.Service, health.Version, api.baseURL, health.Nodes)
		return nil
	}
	return fmt.Errorf("unsupported controller command %q; available: info", args[0])
}

func cliWorkers(args []string) error {
	if len(args) == 0 || strings.EqualFold(args[0], "list") {
		return cliDevices()
	}
	if strings.EqualFold(args[0], "stats") && len(args) == 1 {
		return cliWorkerStatsList()
	}
	if len(args) < 2 && (strings.EqualFold(args[0], "info") || strings.EqualFold(args[0], "ping") || strings.EqualFold(args[0], "pause") || strings.EqualFold(args[0], "resume") || strings.EqualFold(args[0], "remove") || strings.EqualFold(args[0], "stats")) {
		return fmt.Errorf("usage: nodren workers %s <worker-id>", args[0])
	}
	action, id := strings.ToLower(args[0]), args[1]
	api := newCLIClient()
	switch action {
	case "stats":
		stats, err := api.workerStats(id)
		if err != nil {
			return err
		}
		printWorkerStats(stats)
		return nil
	case "info":
		node, err := api.node(id)
		if err != nil {
			return err
		}
		printWorker(node)
		return nil
	case "ping":
		started := time.Now()
		node, err := api.workerAction(id, "ping")
		if err != nil {
			return err
		}
		fmt.Printf("Target       %s\nReachability %s\nState        %s\nAPI latency  %.2f ms\n", id, map[bool]string{true: "REACHABLE", false: "NOT READY"}[node.State == NodeReady || node.State == NodeBusy || node.State == NodePaused], node.State, float64(time.Since(started).Microseconds())/1000)
		return nil
	case "pause", "resume", "remove":
		node, err := api.workerAction(id, action)
		if err != nil {
			return err
		}
		fmt.Printf("Worker %s: %s\n", node.Info.ID, node.State)
		return nil
	default:
		return fmt.Errorf("unknown workers command %q; available: list, stats, info, ping, pause, resume, remove", action)
	}
}

func cliWorkerStatsList() error {
	nodes, err := newCLIClient().nodes()
	if err != nil {
		return err
	}
	for _, node := range nodes {
		stats, statsErr := newCLIClient().workerStats(node.Info.ID)
		if statsErr != nil {
			return statsErr
		}
		printWorkerStats(stats)
	}
	return nil
}

func printWorkerStats(stats workerStatsResponse) {
	node := stats.Node
	fmt.Printf("Worker %s state=%s capacity=%.2f telemetry_age=%dms CPU=%.1f%% RAM=%.1f%% available_ram=%dGB active_tasks=%d completed=%d failed=%d throughput=%.2f units/s factor=%.2f\n", node.Info.ID, node.State, stats.SchedulerWeight, stats.TelemetryAgeMS, node.Telemetry.CPUUtilizationPercent, node.Telemetry.MemoryUtilizationPercent, node.Telemetry.MemoryAvailableGB, node.Telemetry.ActiveTasks, node.Telemetry.CompletedTasks, node.Telemetry.FailedTasks, node.ObservedThroughput, node.PerformanceFactor)
	if len(stats.CurrentPartitions) > 0 {
		fmt.Printf("  partitions=%s\n", strings.Join(stats.CurrentPartitions, ", "))
	}
}

func printWorker(node NodeRecord) {
	fmt.Printf("Worker Information: %s\nID           %s\nState        %s\nHostname     %s\nOS           %s\nArchitecture %s\nCPU model    %s\nCPU capacity %d cores (%d allocated, %d available)\nRAM capacity %d GB (%d allocated, %d available)\nCPU usage    %.1f%%\nRAM usage    %.1f%% (%d GB available)\nTasks        %d active, %d completed, %d failed\nAssigned     %s\nGPU          %s\nHeartbeat    %s\nConnected    %s\n", node.Info.ID, node.Info.ID, node.State, node.Info.Hostname, node.Info.OS, node.Info.Arch, node.Info.CPUModel, node.Info.CPUCores, node.AllocatedCPUCores, node.AvailableCPUCores, node.Info.RAMGB, node.AllocatedRAMGB, node.AvailableRAMGB, node.Telemetry.CPUUtilizationPercent, node.Telemetry.MemoryUtilizationPercent, node.Telemetry.MemoryAvailableGB, node.Telemetry.ActiveTasks, node.Telemetry.CompletedTasks, node.Telemetry.FailedTasks, assignedJobs(node.AssignedJobs), gpuDescription(node.Info.GPU), node.LastHeartbeat, node.ConnectedAt)
}

func cliJobsCommand(args []string) error {
	if len(args) == 0 || strings.EqualFold(args[0], "list") {
		return cliJobs()
	}
	if strings.EqualFold(args[0], "run") {
		return cliRun(args[1:])
	}
	if len(args) < 2 {
		return fmt.Errorf("usage: nodren jobs %s <job-id>", args[0])
	}
	action, id := strings.ToLower(args[0]), args[1]
	api := newCLIClient()
	if action == "info" {
		job, err := api.job(id)
		if err != nil {
			return err
		}
		printJob(job)
		return nil
	}
	if action == "stats" {
		stats, err := api.jobStats(id)
		if err != nil {
			return err
		}
		printJob(stats.Job)
		fmt.Printf("Queue time   %d ms\nElapsed      %d ms\nActive nodes %s\n", stats.QueueTimeMS, stats.ElapsedMS, assignedJobs(stats.ActiveWorkers))
		for _, reason := range stats.SchedulerReasons {
			fmt.Printf("  %s\n", reason)
		}
		return nil
	}
	if action == "partitions" {
		partitions, err := api.jobPartitions(id)
		if err != nil {
			return err
		}
		for _, partition := range partitions {
			fmt.Printf("%s index=%d units=%d state=%s node=%s attempt=%d duration=%dus\n  reason=%s\n", partition.ID, partition.Index, partition.Units, partition.State, partition.NodeID, partition.Attempt, partition.ExecutionDurationUS, partition.AssignmentReason)
		}
		return nil
	}
	if action != "cancel" && action != "pause" && action != "resume" {
		return fmt.Errorf("unknown jobs command %q; available: list, info, stats, partitions, run, cancel, pause, resume", action)
	}
	job, err := api.jobAction(id, action)
	if err != nil {
		return err
	}
	fmt.Printf("Job %s: %s\n", job.ID, job.Status)
	return nil
}

func cliTasksCommand(args []string) error {
	if len(args) == 0 || strings.EqualFold(args[0], "list") {
		tasks, err := newCLIClient().tasks()
		if err != nil {
			return err
		}
		fmt.Println("Nodren Tasks")
		fmt.Println("------------")
		if len(tasks) == 0 {
			fmt.Println("No tasks found.")
			return nil
		}
		for _, task := range tasks {
			taskType := "-"
			if task.Task != nil {
				taskType = string(task.Task.Type)
			}
			fmt.Printf("%s  type=%s  state=%s  worker=%s\n", task.ID, taskType, task.Status, taskWorker(task))
		}
		return nil
	}
	if len(args) < 2 {
		return fmt.Errorf("usage: nodren tasks <info|cancel|retry|logs|result> <task-id>")
	}
	action, id := strings.ToLower(args[0]), args[1]
	api := newCLIClient()
	switch action {
	case "info":
		task, err := api.task(id)
		if err != nil {
			return err
		}
		printTask(task)
		return nil
	case "cancel", "retry":
		task, err := api.taskAction(id, action)
		if err != nil {
			return err
		}
		fmt.Printf("Task %s: %s\n", task.ID, task.Status)
		return nil
	case "logs", "result":
		result, err := api.taskResult(id)
		if err != nil {
			return err
		}
		printTaskResult(result)
		return nil
	case "artifacts":
		task, err := api.task(id)
		if err != nil {
			return err
		}
		if task.Task == nil {
			return &cliError{message: "Task has no generalized task specification"}
		}
		fmt.Printf("Input artifacts:\n")
		for _, artifact := range task.Task.InputArtifacts {
			fmt.Printf("  %s  %s  %d bytes  sha256=%s\n", artifact.ID, artifact.Name, artifact.Size, artifact.SHA256)
		}
		fmt.Printf("Output artifacts:\n")
		for _, artifact := range task.Task.OutputArtifacts {
			fmt.Printf("  %s  %s  %d bytes  sha256=%s\n", artifact.ID, artifact.Name, artifact.Size, artifact.SHA256)
		}
		return nil
	default:
		return fmt.Errorf("unknown tasks command %q; available: list, info, cancel, retry, logs, result, artifacts", action)
	}
}

func cliArtifactsCommand(args []string) error {
	if len(args) == 0 || strings.EqualFold(args[0], "upload") && len(args) != 2 || strings.EqualFold(args[0], "download") && len(args) != 3 {
		return fmt.Errorf("usage: nodren artifacts upload <file> | download <artifact-id> <file>")
	}
	switch strings.ToLower(args[0]) {
	case "upload":
		artifact, err := newCLIClient().uploadArtifact(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Artifact     %s\nName         %s\nSize         %d bytes\nSHA-256      %s\n", artifact.ID, artifact.Name, artifact.Size, artifact.SHA256)
		return nil
	case "download":
		if err := newCLIClient().downloadArtifact(args[1], args[2]); err != nil {
			return err
		}
		fmt.Printf("Downloaded   %s\n", args[2])
		return nil
	default:
		return fmt.Errorf("unknown artifacts command %q; available: upload, download", args[0])
	}
}

func taskWorker(task Job) string {
	if task.NodeID != "" {
		return task.NodeID
	}
	if len(task.NodeIDs) > 0 {
		return strings.Join(task.NodeIDs, ", ")
	}
	return "-"
}

func printTask(task Job) {
	taskType := "-"
	if task.Task != nil {
		taskType = string(task.Task.Type)
	}
	fmt.Printf("Task ID      %s\nType         %s\nStatus       %s\nWorker       %s\nCreated      %s\nUpdated      %s\n", task.ID, taskType, task.Status, taskWorker(task), task.CreatedAt, task.UpdatedAt)
	if task.Task != nil {
		if task.Task.Executable != "" {
			fmt.Printf("Executable    %s\n", task.Task.Executable)
		}
		if task.Task.Script != "" {
			fmt.Printf("Script        %s\n", task.Task.Script)
		}
		if task.Task.Runtime != "" {
			fmt.Printf("Runtime       %s\n", task.Task.Runtime)
		}
		if len(task.Task.Arguments) > 0 {
			fmt.Printf("Arguments     %s\n", strings.Join(task.Task.Arguments, " "))
		}
	}
	if task.Execution != nil {
		printTaskResult(*task.Execution)
	}
}

func printTaskResult(result GeneralTaskResult) {
	stdout, stdoutErr := base64.StdEncoding.DecodeString(result.StdoutB64)
	stderr, stderrErr := base64.StdEncoding.DecodeString(result.StderrB64)
	fmt.Printf("Status       %s\nDuration     %d us\n", result.Status, result.DurationUS)
	if result.ExitCode != nil {
		fmt.Printf("Exit code    %d\n", *result.ExitCode)
	}
	if result.Error != "" {
		fmt.Printf("Error        %s %s\n", result.ErrorCode, result.Error)
	}
	if stdoutErr == nil && len(stdout) > 0 {
		fmt.Printf("Stdout:\n%s\n", string(stdout))
	}
	if stderrErr == nil && len(stderr) > 0 {
		fmt.Printf("Stderr:\n%s\n", string(stderr))
	}
	if result.StdoutTruncated || result.StderrTruncated {
		fmt.Printf("Output       truncated (stdout=%t stderr=%t)\n", result.StdoutTruncated, result.StderrTruncated)
	}
}

func printJob(job Job) {
	worker := job.NodeID
	if worker == "" && len(job.NodeIDs) > 0 {
		worker = strings.Join(job.NodeIDs, ", ")
	}
	if worker == "" {
		worker = "-"
	}
	fmt.Printf("Job ID       %s\nWorkload     %s\nStatus       %s\nProgress     %.2f%%\nWorkers      %s\nPartitions   %d total, %d completed, %d running, %d pending\nCreated      %s\nUpdated      %s\n", job.ID, job.Command, job.Status, job.Distribution.ProgressPercent, worker, job.Distribution.TotalPartitions, job.Distribution.CompletedPartitions, job.Distribution.RunningPartitions, job.Distribution.PendingPartitions, job.CreatedAt, job.UpdatedAt)
	if job.Result != nil {
		fmt.Printf("Result       %d\nError        %s %s\n", job.Result.Value, job.Result.ErrorCode, job.Result.Error)
	}
}

func cliDistribution(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nodren distribution show [job-id] | auto <job-id> | set <job-id> worker=percent...")
	}
	api := newCLIClient()
	switch strings.ToLower(args[0]) {
	case "show":
		if len(args) == 1 {
			jobs, err := api.jobs()
			if err != nil {
				return err
			}
			for _, job := range jobs {
				fmt.Printf("%s  mode=%s  progress=%.2f%%  partitions=%d/%d\n", job.ID, job.Distribution.Mode, job.Distribution.ProgressPercent, job.Distribution.CompletedPartitions, job.Distribution.TotalPartitions)
			}
			return nil
		}
		job, err := api.job(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Job %s\nMode: %s\n", job.ID, strings.ToUpper(string(job.Distribution.Mode)))
		for worker, percentage := range job.Distribution.ManualAllocations {
			fmt.Printf("%s\t%d%%\n", worker, percentage)
		}
		return nil
	case "auto":
		if len(args) != 2 {
			return fmt.Errorf("usage: nodren distribution auto <job-id>")
		}
		job, err := api.updateDistribution(args[1], distributionRequest{Mode: DistributionAutomatic})
		if err != nil {
			return err
		}
		fmt.Printf("Job %s distribution: AUTOMATIC\n", job.ID)
		return nil
	case "set":
		if len(args) < 3 {
			return fmt.Errorf("usage: nodren distribution set <job-id> worker=percent [worker=percent ...]")
		}
		allocations, err := parseAllocations(args[2:])
		if err != nil {
			return err
		}
		job, err := api.updateDistribution(args[1], distributionRequest{Mode: DistributionManual, ManualAllocations: allocations})
		if err != nil {
			return err
		}
		fmt.Printf("Job %s distribution: MANUAL\n", job.ID)
		return nil
	default:
		return fmt.Errorf("unknown distribution command %q; available: show, auto, set", args[0])
	}
}

func parseAllocations(values []string) (map[string]uint8, error) {
	allocations := make(map[string]uint8)
	for _, value := range values {
		for _, entry := range strings.Split(value, ",") {
			parts := strings.SplitN(strings.TrimSpace(entry), "=", 2)
			if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
				return nil, fmt.Errorf("invalid allocation %q; expected worker=percent", entry)
			}
			percent, err := strconv.ParseUint(parts[1], 10, 8)
			if err != nil || percent > 100 {
				return nil, fmt.Errorf("invalid allocation percentage %q; expected 0..100", parts[1])
			}
			allocations[parts[0]] = uint8(percent)
		}
	}
	return allocations, nil
}

func cliStatus() error {
	api := newCLIClient()
	health, err := api.health()
	if err != nil {
		return err
	}
	nodes, err := api.nodes()
	if err != nil {
		return err
	}
	jobs, err := api.jobs()
	if err != nil {
		return err
	}
	ready := 0
	for _, node := range nodes {
		if node.State == NodeReady {
			ready++
		}
	}
	counts := map[JobStatus]int{}
	for _, job := range jobs {
		counts[job.Status]++
	}
	cluster, clusterErr := api.clusterStatus()
	fmt.Println("Nodren Status")
	fmt.Println("-------------")
	fmt.Printf("Controller     ONLINE (%s)\n", api.baseURL)
	if clusterErr == nil {
		fmt.Printf("Uptime         %ds\n", cluster.UptimeSeconds)
		fmt.Printf("Workers        %d total, %d online, %d stale, %d offline\n", cluster.Workers, cluster.OnlineWorkers, cluster.StaleWorkers, cluster.OfflineWorkers)
		fmt.Printf("CPU usage      %.1f%%\n", cluster.CPUUtilizationPercent)
		fmt.Printf("RAM usage      %.1f%% (%d GB dynamic available)\n", cluster.MemoryUtilizationPercent, cluster.MemoryAvailableGB)
		fmt.Printf("Active tasks   %d\n", cluster.ActiveTasks)
		fmt.Printf("Running Jobs   %d\n", cluster.ActiveJobs)
		fmt.Printf("Queued Jobs    %d\n", cluster.QueuedJobs)
		fmt.Printf("Completed      %d jobs / %d tasks\n", cluster.CompletedJobs, cluster.TotalCompletedTasks)
		fmt.Printf("Failed         %d jobs / %d tasks\n", cluster.FailedJobs, cluster.TotalFailedTasks)
	} else {
		// Keep status useful when talking to a pre-observability Controller.
		fmt.Printf("Workers        %d\n", health.Nodes)
		fmt.Printf("Ready          %d\n", ready)
		fmt.Printf("Running Jobs   %d\n", counts[JobRunning])
		fmt.Printf("Queued Jobs    %d\n", counts[JobQueued])
		fmt.Printf("Completed      %d\n", counts[JobCompleted])
		fmt.Printf("Failed         %d\n", counts[JobFailed])
	}
	return nil
}

func cliDevices() error {
	nodes, err := newCLIClient().nodes()
	if err != nil {
		return err
	}
	fmt.Println("Nodren Workers")
	fmt.Println("--------------")
	if len(nodes) == 0 {
		fmt.Println("No workers registered.")
		return nil
	}
	for _, node := range nodes {
		fmt.Printf("%s  state=%s  CPU=%d/%d  RAM=%d/%dGB  jobs=%d\n", node.Info.ID, node.State, node.AllocatedCPUCores, node.Info.CPUCores, node.AllocatedRAMGB, node.Info.RAMGB, len(node.AssignedJobs))
		fmt.Printf("  OS=%s arch=%s GPU=%s\n", node.Info.OS, node.Info.Arch, gpuDescription(node.Info.GPU))
	}
	return nil
}

func cliInfo(args []string) error {
	if len(args) == 0 {
		api := newCLIClient()
		fmt.Printf("Nodren %s\nController: %s\n", nodrenVersion, api.baseURL)
		return nil
	}
	nodes, err := newCLIClient().nodes()
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if node.Info.ID == args[0] {
			fmt.Printf("Worker Information: %s\n", args[0])
			fmt.Printf("ID           %s\nState        %s\nHostname     %s\nOS           %s\nArchitecture %s\n", node.Info.ID, node.State, node.Info.Hostname, node.Info.OS, node.Info.Arch)
			fmt.Printf("CPU          %d / %d allocated\nRAM          %d / %d GB allocated\n", node.AllocatedCPUCores, node.Info.CPUCores, node.AllocatedRAMGB, node.Info.RAMGB)
			fmt.Printf("Assigned     %s\nGPU          %s\nHeartbeat    %s\n", assignedJobs(node.AssignedJobs), gpuDescription(node.Info.GPU), node.LastHeartbeat)
			return nil
		}
	}
	return &cliError{message: "Unknown worker: " + args[0]}
}

func cliPing(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nodren ping <worker>")
	}
	started := time.Now()
	nodes, err := newCLIClient().nodes()
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if node.Info.ID == args[0] {
			reachable := node.State == NodeReady || node.State == NodeBusy
			fmt.Printf("Target       %s\nReachability %s\nState        %s\nAPI latency  %.2f ms\n", node.Info.ID, map[bool]string{true: "REACHABLE", false: "NOT READY"}[reachable], node.State, float64(time.Since(started).Microseconds())/1000)
			fmt.Println("[INFO] Latency measures the Controller API request, not a direct worker probe.")
			return nil
		}
	}
	return &cliError{message: "Unknown worker: " + args[0]}
}

func cliJobs() error {
	jobs, err := newCLIClient().jobs()
	if err != nil {
		return err
	}
	fmt.Println("Nodren Jobs")
	fmt.Println("-----------")
	if len(jobs) == 0 {
		fmt.Println("No jobs found.")
		return nil
	}
	for _, job := range jobs {
		worker := job.NodeID
		if worker == "" && len(job.NodeIDs) > 0 {
			worker = strings.Join(job.NodeIDs, ", ")
		}
		if worker == "" {
			worker = "-"
		}
		fmt.Printf("%s  %s  state=%s  worker=%s  cpu=%d ram=%dGB gpu=%t\n", job.ID, job.Command, job.Status, worker, job.Requirements.CPUCores, job.Requirements.RAMGB, job.Requirements.GPURequired)
		if job.Distribution.Partitionable && job.Distribution.TotalPartitions > 1 {
			fmt.Printf("  distribution=%s partitions=%d/%d units=%d/%d\n", job.Distribution.Mode, job.Distribution.CompletedPartitions, job.Distribution.TotalPartitions, job.Distribution.CompletedUnits, job.Distribution.TotalUnits)
		}
		if job.Result != nil && job.Result.Error != "" {
			fmt.Printf("  error=%s %s\n", job.Result.ErrorCode, job.Result.Error)
		}
	}
	return nil
}

func cliRun(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nodren run <sum|xor|dot_product|process|command|script> [arguments...]")
	}
	if _, err := os.Stat(args[0]); err == nil {
		return cliRunPath(args)
	}
	command := strings.ToLower(args[0])
	if command == "process" || command == "command" || command == "script" {
		return cliRunGeneralTask(command, args[1:])
	}
	payload, err := workloadPayload(command, args[1:])
	if err != nil {
		return err
	}
	request := jobRequest{Command: command, Priority: 50, Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1}, PayloadB64: base64.StdEncoding.EncodeToString(payload)}
	api := newCLIClient()
	submitted, err := api.submit(request)
	if err != nil {
		return err
	}
	fmt.Printf("Workload     %s\nJob ID       %s\nState        %s\n", submitted.Command, submitted.ID, submitted.Status)
	worker := submitted.NodeID
	if worker == "" && len(submitted.NodeIDs) > 0 {
		worker = strings.Join(submitted.NodeIDs, ", ")
	}
	if worker != "" {
		fmt.Printf("Worker       %s\n", worker)
	}
	fmt.Println("Waiting for result...")
	seconds := 30
	if value, parseErr := strconv.Atoi(getenv("NODREN_JOB_TIMEOUT_SECS", "30")); parseErr == nil && value > 0 {
		seconds = value
	}
	completed, err := api.waitForJob(submitted.ID, time.Duration(seconds)*time.Second)
	if err != nil {
		return err
	}
	if completed.Result == nil {
		return &cliError{message: "Invalid Controller response: completed job has no result"}
	}
	worker = completed.NodeID
	if worker == "" && len(completed.NodeIDs) > 0 {
		worker = strings.Join(completed.NodeIDs, ", ")
	}
	fmt.Printf("State        %s\nWorker       %s\nResult       %d\nDuration     %d us\n", completed.Status, worker, completed.Result.Value, completed.Result.DurationUS)
	if completed.Distribution.Partitionable && completed.Distribution.TotalPartitions > 1 {
		fmt.Printf("Distribution %s (%d partitions, %d units)\n", completed.Distribution.Mode, completed.Distribution.TotalPartitions, completed.Distribution.TotalUnits)
	}
	return nil
}

func cliRunPath(args []string) error {
	path := args[0]
	info, err := os.Stat(path)
	if err != nil {
		return &cliError{message: "Cannot inspect workload: " + err.Error()}
	}
	separator := len(args)
	for index, value := range args[1:] {
		if value == "--" {
			separator = index + 1
			break
		}
	}
	arguments := []string{}
	if separator < len(args) {
		arguments = append(arguments, args[separator+1:]...)
	}
	var artifact TaskArtifact
	spec := GeneralTaskSpec{
		Version:          "1",
		WorkloadKind:     "file",
		Strategy:         ExecutionSingle,
		Arguments:        arguments,
		Requirements:     ResourceRequirements{CPUCores: 1, RAMGB: 1},
		StdoutLimitBytes: 1 << 20,
		StderrLimitBytes: 1 << 20,
	}
	cleanup := ""
	if info.IsDir() {
		packagePath, manifest, packageErr := buildTaskPackage(path)
		if packageErr != nil {
			return packageErr
		}
		cleanup = packagePath
		defer os.Remove(packagePath)
		artifact, err = newCLIClient().uploadArtifactWithKind(packagePath, "package")
		if err != nil {
			return err
		}
		spec.InputArtifacts = []TaskArtifact{{ID: artifact.ID, Name: "package.tar", Size: artifact.Size, SHA256: artifact.SHA256, Kind: "package"}}
		spec.WorkloadKind = "project"
		spec.PackageManifest = &manifest.TaskPackageManifest
		if manifest.Runtime != "" {
			spec.Type = TaskTypeScript
			spec.Runtime = manifest.Runtime
			spec.Script = manifest.EntryPoint
		} else {
			spec.Type = TaskTypeProcess
			spec.Executable = manifest.Executable
		}
		if len(manifest.Arguments) > 0 && len(spec.Arguments) == 0 {
			spec.Arguments = append([]string(nil), manifest.Arguments...)
		}
		if len(manifest.Environment) > 0 {
			spec.Environment = manifest.Environment
		}
	} else {
		artifact, err = newCLIClient().uploadArtifactWithKind(path, "file")
		if err != nil {
			return err
		}
		name := filepath.Base(path)
		spec.InputArtifacts = []TaskArtifact{{ID: artifact.ID, Name: name, Size: artifact.Size, SHA256: artifact.SHA256, Kind: artifact.Kind}}
		spec.WorkloadKind = "executable"
		extension := strings.ToLower(filepath.Ext(name))
		switch extension {
		case ".py":
			spec.Type, spec.Runtime, spec.Script = TaskTypeScript, "python", name
		case ".js", ".mjs", ".cjs":
			spec.Type, spec.Runtime, spec.Script = TaskTypeScript, "node", name
		case ".sh":
			spec.Type, spec.Runtime, spec.Script = TaskTypeScript, "sh", name
		case ".ps1":
			spec.Type, spec.Runtime, spec.Script = TaskTypeScript, "powershell", name
		default:
			spec.Type = TaskTypeProcess
			spec.Executable = "./" + filepath.ToSlash(name)
		}
	}
	_ = cleanup
	submitted, err := newCLIClient().submitTask(taskRequest{Task: spec})
	if err != nil {
		return err
	}
	fmt.Printf("Task ID       %s\nType          %s\nArtifact      %s (%d bytes)\nState         %s\nWaiting for result...\n", submitted.ID, spec.Type, artifact.ID, artifact.Size, submitted.Status)
	seconds := 30
	if value, parseErr := strconv.Atoi(getenv("NODREN_JOB_TIMEOUT_SECS", "30")); parseErr == nil && value > 0 {
		seconds = value
	}
	completed, err := newCLIClient().waitForTask(submitted.ID, time.Duration(seconds)*time.Second)
	if err != nil {
		return err
	}
	if completed.Execution == nil {
		return &cliError{message: "Invalid Controller response: completed task has no execution result"}
	}
	printTaskResult(*completed.Execution)
	return nil
}

func cliRunGeneralTask(command string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nodren run %s ...", command)
	}
	separator := len(args)
	for index, value := range args {
		if value == "--" {
			separator = index
			break
		}
	}
	program := args[:separator]
	if command == "script" && len(program) < 2 {
		return fmt.Errorf("usage: nodren run script <runtime> <script> [-- arguments...]")
	}
	if (command == "process" || command == "command") && len(program) < 1 {
		return fmt.Errorf("usage: nodren run %s <executable> [-- arguments...]", command)
	}
	arguments := []string{}
	if separator < len(args) {
		arguments = append(arguments, args[separator+1:]...)
	}
	spec := GeneralTaskSpec{
		Type:         TaskType(strings.ToUpper(command)),
		Version:      "1",
		Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1},
		Arguments:    arguments,
	}
	if command == "script" {
		spec.Runtime, spec.Script = program[0], program[1]
	} else {
		spec.Executable = program[0]
		if len(program) > 1 {
			return fmt.Errorf("arguments must follow -- for process and command tasks")
		}
	}
	submitted, err := newCLIClient().submitTask(taskRequest{Task: spec})
	if err != nil {
		return err
	}
	fmt.Printf("Task ID       %s\nType          %s\nState         %s\nWaiting for result...\n", submitted.ID, spec.Type, submitted.Status)
	seconds := 30
	if value, parseErr := strconv.Atoi(getenv("NODREN_JOB_TIMEOUT_SECS", "30")); parseErr == nil && value > 0 {
		seconds = value
	}
	completed, err := newCLIClient().waitForTask(submitted.ID, time.Duration(seconds)*time.Second)
	if err != nil {
		return err
	}
	if completed.Execution == nil {
		return &cliError{message: "Invalid Controller response: completed task has no execution result"}
	}
	printTaskResult(*completed.Execution)
	return nil
}

func cliCheck(args []string) error {
	api := newCLIClient()
	if len(args) > 0 {
		return cliInfo(args)
	}
	health, err := api.health()
	if err != nil {
		return err
	}
	nodes, err := api.nodes()
	if err != nil {
		return err
	}
	fmt.Printf("[OK] CLI\n[OK] Controller: %s %s\n[OK] Workers: %d\n", health.Service, health.Version, len(nodes))
	return nil
}

func cliDoctor() error {
	api := newCLIClient()
	health, err := api.health()
	if err != nil {
		return err
	}
	nodes, err := api.nodes()
	if err != nil {
		return err
	}
	jobs, err := api.jobs()
	if err != nil {
		return err
	}
	fmt.Printf("Nodren Doctor\n[OK] Controller: %s\n[OK] Workers: %d\n[OK] Jobs: %d\n", health.Status, len(nodes), len(jobs))
	fmt.Println("[INFO] Native capabilities are provided by the worker release artifact.")
	return nil
}

func cliConfig(args []string) error {
	if len(args) > 0 && !strings.EqualFold(args[0], "show") {
		return fmt.Errorf("config set is not available because Controller configuration is process-owned; use environment variables")
	}
	api := newCLIClient()
	fmt.Printf("Controller: %s\nNode TCP bind: %s\nHTTP bind: %s\nState file: %s\n", api.baseURL, getenv("NODREN_NODE_ADDR", ":9000"), getenv("NODREN_HTTP_ADDR", ":8080"), getenv("NODREN_STATE_FILE", "nodren-state.json"))
	fmt.Println("Worker controller address: --controller <host:port> or NODREN_CONTROLLER_ADDR")
	return nil
}

func workloadPayload(command string, args []string) ([]byte, error) {
	if command == "sum" || command == "xor" {
		if len(args) == 0 {
			return nil, fmt.Errorf("usage: nodren run %s <byte>...", command)
		}
		payload := make([]byte, len(args))
		for index, value := range args {
			parsed, err := strconv.ParseUint(value, 10, 8)
			if err != nil {
				return nil, fmt.Errorf("%s arguments must be unsigned bytes: %s", command, value)
			}
			payload[index] = byte(parsed)
		}
		return payload, nil
	}
	if command == "dot_product" {
		if len(args) != 2 {
			return nil, fmt.Errorf("usage: nodren run dot_product <left comma-separated vector> <right comma-separated vector>")
		}
		left, err := parseVector(args[0])
		if err != nil {
			return nil, err
		}
		right, err := parseVector(args[1])
		if err != nil {
			return nil, err
		}
		if len(left) == 0 || len(left) != len(right) {
			return nil, fmt.Errorf("dot_product vectors must be non-empty and have equal length")
		}
		payload := make([]byte, 4+8*len(left))
		binary.LittleEndian.PutUint32(payload[:4], uint32(len(left)))
		cursor := 4
		for _, value := range append(left, right...) {
			binary.LittleEndian.PutUint32(payload[cursor:cursor+4], uint32(value))
			cursor += 4
		}
		return payload, nil
	}
	if len(args) == 0 {
		return []byte("payload"), nil
	}
	return []byte(strings.Join(args, " ")), nil
}

func parseVector(value string) ([]int32, error) {
	parts := strings.Split(value, ",")
	values := make([]int32, len(parts))
	for index, part := range parts {
		parsed, err := strconv.ParseInt(part, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("dot_product vector contains invalid integer: %s", part)
		}
		values[index] = int32(parsed)
	}
	return values, nil
}

func assignedJobs(jobs []string) string {
	if len(jobs) == 0 {
		return "none"
	}
	return strings.Join(jobs, ", ")
}

func gpuDescription(gpu GPUInfo) string {
	if gpu.Model == "" {
		return "none"
	}
	return fmt.Sprintf("%s %s (%dGB)", gpu.Vendor, gpu.Model, gpu.VRAMGB)
}

func printHelp() {
	fmt.Printf(`Nodren %s

Usage:
    nodren.exe                 Start the Controller
    nodren.exe <command> ...   Query the running Controller

CONTROLLER
    status                  Show live Controller and job state
    controller info         Show Controller information
    workers list             List registered workers
    workers stats            Show telemetry and scheduler capacity
    workers info <worker>   Show worker details
    workers ping <worker>   Check Controller-observed reachability
    workers pause <worker>  Stop new scheduling to a worker
    workers resume <worker> Resume scheduling to a worker
    workers remove <worker> Disconnect and requeue worker work
    jobs list               List jobs
    jobs info <job>         Show job details and progress
    jobs stats <job>         Show timing and scheduler decisions
    jobs partitions <job>    Show partition states and assignment reasons
    jobs run <workload> ... Submit and wait for a result
    jobs cancel <job>       Cancel a queued or running job
    jobs pause <job>        Pause a queued job
    jobs resume <job>       Resume a paused job
    distribution show [job] Show distribution state
    distribution auto <job> Use automatic distribution
    distribution set <job> worker=percent...
    check                   Check Controller/API availability
    doctor                  Run live diagnostics
    config show             Show network configuration
    monitor                  Stream controller events (alias: events)
    version                 Show version

WORKLOADS
    run sum <byte>...
    run xor <byte>...
    run dot_product <left> <right>
    run process <executable> [-- arguments...]
    run command <executable> [-- arguments...]
    run script <runtime> <script> [-- arguments...]

TASKS
    tasks list              List generalized tasks
    tasks info <task>       Show task details and execution result
    tasks logs <task>       Show captured stdout/stderr
    tasks result <task>     Show exit code and execution result
    tasks artifacts <task>  List task artifact metadata
    tasks cancel <task>     Cancel a queued or running task
    tasks retry <task>      Retry a failed or timed-out task
    artifacts upload <file> Upload a controller input artifact
    artifacts download <id> <file>
                            Download an artifact to a local file

AI WORKLOADS
    ai inspect <model-or-folder>
    ai plan <model-or-folder> [--workers N] [--strategy NAME]
    ai run <model-or-folder> [--workers N] [--strategy NAME]
    ai workers                List workers and accelerator metadata
    ai status <execution-id>  Show the distributed execution plan/status
    ai cancel <execution-id>  Cancel all ranks in an execution

NETWORK
    NODREN_NODE_ADDR        Controller worker TCP bind, default :9000
    NODREN_HTTP_ADDR        Controller HTTP bind, default :8080
    NODREN_CONTROLLER_URL   CLI HTTP address, default http://127.0.0.1:8080
    NODREN_JOB_TIMEOUT_SECS Synchronous job timeout, default 30

The worker accepts --controller <host:port> and --controller=<host:port>.
`, nodrenVersion)
}

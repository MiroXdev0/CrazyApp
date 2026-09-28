package main

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
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
	case "devices", "nodes":
		return cliDevices()
	case "info":
		return cliInfo(args[1:])
	case "ping":
		return cliPing(args[1:])
	case "jobs", "job":
		return cliJobs()
	case "run":
		return cliRun(args[1:])
	case "check":
		return cliCheck(args[1:])
	case "doctor":
		return cliDoctor()
	case "config":
		return cliConfig()
	case "stop", "restart", "reload":
		fmt.Println("[INFO] stop/restart lifecycle control is not implemented yet; no processes were changed.")
		return nil
	case "logs", "log":
		fmt.Println("[INFO] Centralized log retrieval is not implemented yet.")
		return nil
	default:
		return fmt.Errorf("unknown command %q; run 'nodren help'", args[0])
	}
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
	fmt.Println("Nodren Status")
	fmt.Println("-------------")
	fmt.Printf("Controller     ONLINE (%s)\n", api.baseURL)
	fmt.Printf("Workers        %d\n", health.Nodes)
	fmt.Printf("Ready          %d\n", ready)
	fmt.Printf("Running Jobs   %d\n", counts[JobRunning])
	fmt.Printf("Queued Jobs    %d\n", counts[JobQueued])
	fmt.Printf("Completed      %d\n", counts[JobCompleted])
	fmt.Printf("Failed         %d\n", counts[JobFailed])
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
		if worker == "" {
			worker = "-"
		}
		fmt.Printf("%s  %s  state=%s  worker=%s  cpu=%d ram=%dGB gpu=%t\n", job.ID, job.Command, job.Status, worker, job.Requirements.CPUCores, job.Requirements.RAMGB, job.Requirements.GPURequired)
		if job.Result != nil && job.Result.Error != "" {
			fmt.Printf("  error=%s %s\n", job.Result.ErrorCode, job.Result.Error)
		}
	}
	return nil
}

func cliRun(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nodren run <sum|xor|dot_product> [arguments...]")
	}
	command := strings.ToLower(args[0])
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
	if submitted.NodeID != "" {
		fmt.Printf("Worker       %s\n", submitted.NodeID)
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
	fmt.Printf("State        %s\nWorker       %s\nResult       %d\nDuration     %d us\n", completed.Status, completed.NodeID, completed.Result.Value, completed.Result.DurationUS)
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

func cliConfig() error {
	api := newCLIClient()
	fmt.Printf("Controller: %s\nNode TCP bind: %s\nHTTP bind: %s\n", api.baseURL, getenv("NODREN_NODE_ADDR", ":9000"), getenv("NODREN_HTTP_ADDR", ":8080"))
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
    devices                 List registered workers
    info <worker>           Show worker details
    ping <worker>           Show Controller-observed reachability
    jobs                    List jobs
    run <workload> ...      Submit and wait for a result
    check                   Check Controller/API availability
    doctor                  Run live diagnostics
    config                  Show network configuration
    version                 Show version

WORKLOADS
    run sum <byte>...
    run xor <byte>...
    run dot_product <left> <right>

NETWORK
    NODREN_NODE_ADDR        Controller worker TCP bind, default :9000
    NODREN_HTTP_ADDR        Controller HTTP bind, default :8080
    NODREN_CONTROLLER_URL   CLI HTTP address, default http://127.0.0.1:8080
    NODREN_JOB_TIMEOUT_SECS Synchronous job timeout, default 30

The worker accepts --controller <host:port> and --controller=<host:port>.
`, nodrenVersion)
}

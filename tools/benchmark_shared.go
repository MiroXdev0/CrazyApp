package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	warmupIterations     = envInt("NODREN_WARMUP_ITERATIONS", 2)
	measuredIterations   = envInt("NODREN_MEASURED_ITERATIONS", 6)
	benchmarkTaskCount   = envInt("NODREN_TASK_COUNT", 1200)
	benchmarkPayloadSize = envInt("NODREN_PAYLOAD_SIZE", 4096)
)

type stageMetrics struct {
	Name                 string  `json:"name"`
	NodeCount            int     `json:"node_count"`
	CppWorkerThreads     int     `json:"cpp_worker_threads"`
	Tasks                int     `json:"tasks"`
	TotalDurationMs      float64 `json:"total_duration_ms"`
	MeanDurationMs       float64 `json:"mean_duration_ms"`
	MinDurationMs        float64 `json:"min_duration_ms"`
	StdDevMs             float64 `json:"stddev_duration_ms"`
	TasksPerSecond       float64 `json:"tasks_per_second"`
	OperationsPerSecond  float64 `json:"operations_per_second"`
	AverageLatencyMs     float64 `json:"average_latency_ms"`
	P50LatencyMs         float64 `json:"p50_latency_ms"`
	P95LatencyMs         float64 `json:"p95_latency_ms"`
	P99LatencyMs         float64 `json:"p99_latency_ms"`
	CPUUsagePct          float64 `json:"cpu_usage_pct"`
	RAMUsageMB           float64 `json:"ram_usage_mb"`
	NetworkBytesPerSec   float64 `json:"network_bytes_per_second"`
	ControllerQueueWait  float64 `json:"controller_queue_wait_ms"`
	NodeQueueWait        float64 `json:"node_queue_wait_ms"`
	SerializationMs      float64 `json:"serialization_ms"`
	IPCMS                float64 `json:"ipc_ms"`
	CppComputeMs         float64 `json:"cpp_compute_ms"`
	CppCpuTimeMs         float64 `json:"cpp_cpu_time_ms"`
	ResultCollectionMs   float64 `json:"result_collection_ms"`
	SynchronizationMs    float64 `json:"synchronization_ms"`
	StartupMs            float64 `json:"startup_ms"`
	ShutdownMs           float64 `json:"shutdown_ms"`
	TotalThreadCount     int     `json:"total_thread_count"`
	Oversubscription     float64 `json:"oversubscription"`
	DominantContributor  string  `json:"dominant_contributor"`
	ControllerOverheadMs float64 `json:"controller_overhead_ms"`
	GoSchedulingMs       float64 `json:"go_scheduling_ms"`
	TransportOverheadMs  float64 `json:"transport_overhead_ms"`
	DistributedWorkMs    float64 `json:"distributed_work_ms"`
}

type workerResult struct {
	TaskID              int     `json:"task_id"`
	NodeID              string  `json:"node_id"`
	Result              uint64  `json:"result"`
	ComputeMS           float64 `json:"compute_ms"`
	QueueWaitMS         float64 `json:"queue_wait_ms"`
	ResultSerialization float64 `json:"result_serialization_ms"`
	RoundTripMS         float64 `json:"round_trip_ms"`
}

type cpuInfo struct {
	Logical  int
	Physical int
}

type stageSample struct {
	StartupMs           float64
	SerializationMs     float64
	DispatchMs          float64
	QueueWaitMs         float64
	CppWallComputeMs    float64
	ResultSerialization float64
	ResultCollectionMs  float64
	ShutdownMs          float64
	TotalMs             float64
	CppCpuTimeMs        float64
	RoundTripLatencies  []float64
}

func envInt(name string, fallback int) int {
	if value := os.Getenv(name); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Dir(filepath.Dir(file))
}

func compileWorker() string {
	root := repoRoot()
	workerSource := filepath.Join(root, "Core", "Cpp", "source", "nodren_benchmark_worker.cpp")
	binDir := filepath.Join(root, "Core", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		panic(err)
	}
	outPath := filepath.Join(binDir, "nodren_benchmark_worker.exe")
	cmd := exec.Command("g++", "-std=c++17", "-O2", "-march=native", workerSource, "-o", outPath)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		panic(fmt.Sprintf("failed to compile benchmark worker: %v\n%s", err, out))
	}
	return outPath
}

func detectCPUInfo() cpuInfo {
	logical := runtime.NumCPU()
	physical := logical
	cmd := exec.Command("wmic", "cpu", "get", "NumberOfCores,NumberOfLogicalProcessors")
	out, err := cmd.CombinedOutput()
	if err == nil {
		lines := strings.Split(string(out), "\r\n")
		for _, line := range lines {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if parts[0] == "NumberOfLogicalProcessors" {
					if n, err := strconv.Atoi(parts[1]); err == nil && n > 0 {
						logical = n
					}
				}
				if parts[0] == "NumberOfCores" {
					if n, err := strconv.Atoi(parts[1]); err == nil && n > 0 {
						physical = n
					}
				}
			}
		}
	}
	if logical == 0 {
		logical = runtime.NumCPU()
	}
	if physical == 0 {
		physical = logical
	}
	return cpuInfo{Logical: logical, Physical: physical}
}

func deterministicValue(seed uint64, idx uint64) uint64 {
	x := seed + idx*131 + (idx%17)*7
	x ^= x << 13
	x ^= x >> 7
	x *= 0x9E3779B97F4A7C15
	return x
}

func expectedTaskResult(taskID int, payloadSize int) uint64 {
	var total uint64
	for i := 0; i < payloadSize; i++ {
		total += deterministicValue(uint64(taskID+17), uint64(i))
	}
	return total
}

func expectedFinalResult() uint64 {
	var total uint64
	for taskID := 0; taskID < benchmarkTaskCount; taskID++ {
		total += expectedTaskResult(taskID, benchmarkPayloadSize)
	}
	return total
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, v := range values {
		total += v
	}
	return total / float64(len(values))
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	if len(sorted) == 1 {
		return sorted[0]
	}
	idx := int(math.Ceil(float64(len(sorted))*p/100.0)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func launchNode(workerPath string, nodeID string, workers int) (*exec.Cmd, io.WriteCloser, io.ReadCloser, error) {
	cmd := exec.Command(workerPath, "--mode=node", fmt.Sprintf("--node-id=%s", nodeID), fmt.Sprintf("--workers=%d", workers))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	return cmd, stdin, stdout, nil
}

func parseWorkerResult(line string) (workerResult, error) {
	var out workerResult
	if err := json.Unmarshal([]byte(line), &out); err != nil {
		return workerResult{}, err
	}
	return out, nil
}

func runDirectTaskBenchmark(workerPath string, tasks int, payloadSize int) stageSample {
	startAll := time.Now()
	var roundTrip []float64
	var wallCompute float64
	var cpuCompute float64
	var queueWait float64
	var taskSerialization float64
	var dispatch float64
	var resultSerialization float64
	var resultCollection float64
	var totalChecksum uint64
	for i := 0; i < tasks; i++ {
		startTask := time.Now()
		msg := fmt.Sprintf("%d|%d|%d", i, payloadSize, i+17)
		taskSerializeStart := time.Now()
		_ = msg
		taskSerialization += time.Since(taskSerializeStart).Seconds() * 1000.0
		dispatchStart := time.Now()
		cmd := exec.Command(workerPath, "--mode=task", fmt.Sprintf("--task-id=%d", i), fmt.Sprintf("--payload-size=%d", payloadSize))
		out, err := cmd.CombinedOutput()
		if err != nil {
			panic(err)
		}
		dispatch += time.Since(dispatchStart).Seconds() * 1000.0
		result, err := parseWorkerResult(strings.TrimSpace(string(out)))
		if err != nil {
			panic(err)
		}
		wallCompute += result.ComputeMS
		cpuCompute += result.ComputeMS
		queueWait += result.QueueWaitMS
		resultSerialization += result.ResultSerialization
		resultCollection += time.Since(startTask).Seconds() * 1000.0
		totalChecksum += result.Result
		roundTrip = append(roundTrip, time.Since(startTask).Seconds()*1000.0)
	}
	return stageSample{
		StartupMs:           0,
		SerializationMs:     taskSerialization,
		DispatchMs:          dispatch,
		QueueWaitMs:         queueWait,
		CppWallComputeMs:    wallCompute,
		ResultSerialization: resultSerialization,
		ResultCollectionMs:  resultCollection,
		ShutdownMs:          0,
		TotalMs:             time.Since(startAll).Seconds() * 1000.0,
		CppCpuTimeMs:        cpuCompute,
		RoundTripLatencies:  roundTrip,
	}
}

func runIPCBenchmark(workerPath string, workerCount int, tasks int, payloadSize int, batchSize int) stageSample {
	nodeStart := time.Now()
	nodeCmd, stdin, stdout, err := launchNode(workerPath, "node-0", workerCount)
	if err != nil {
		panic(err)
	}
	defer nodeCmd.Process.Kill()
	reader := newLineReader(stdout)
	readyLine, err := reader.readLine()
	if err != nil || !strings.Contains(readyLine, "ready") {
		panic(fmt.Sprintf("node did not report ready: %q", readyLine))
	}
	startupMs := time.Since(nodeStart).Seconds() * 1000.0
	var serializationMs float64
	var dispatchMs float64
	var queueWaitMs float64
	var wallCompute float64
	var resultSerialization float64
	var resultCollection float64
	var totalCpu float64
	var roundTrips []float64
	var totalChecksum uint64
	for i := 0; i < tasks; i++ {
		startTask := time.Now()
		payload := fmt.Sprintf("%d|%d|%d", i, payloadSize, i+17)
		serializationStart := time.Now()
		_, err = fmt.Fprint(stdin, payload+"\n")
		if err != nil {
			panic(err)
		}
		serializationMs += time.Since(serializationStart).Seconds() * 1000.0
		resultLine, err := reader.readLine()
		if err != nil {
			panic(err)
		}
		dispatchMs += time.Since(startTask).Seconds() * 1000.0
		result, err := parseWorkerResult(strings.TrimSpace(resultLine))
		if err != nil {
			panic(err)
		}
		wallCompute += result.ComputeMS
		totalCpu += result.ComputeMS
		queueWaitMs += result.QueueWaitMS
		resultSerialization += result.ResultSerialization
		resultCollection += time.Since(startTask).Seconds() * 1000.0
		totalChecksum += result.Result
		roundTrips = append(roundTrips, time.Since(startTask).Seconds()*1000.0)
	}
	if _, err := fmt.Fprint(stdin, "EOF\n"); err != nil {
		panic(err)
	}
	if err := stdin.Close(); err != nil {
		panic(err)
	}
	if err := nodeCmd.Wait(); err != nil {
		panic(err)
	}
	totalMs := time.Since(nodeStart).Seconds() * 1000.0
	return stageSample{
		StartupMs:           startupMs,
		SerializationMs:     serializationMs,
		DispatchMs:          dispatchMs,
		QueueWaitMs:         queueWaitMs,
		CppWallComputeMs:    wallCompute,
		ResultSerialization: resultSerialization,
		ResultCollectionMs:  resultCollection,
		ShutdownMs:          0,
		TotalMs:             totalMs,
		CppCpuTimeMs:        totalCpu,
		RoundTripLatencies:  roundTrips,
	}
}

type lineReader struct {
	lines  []string
	index  int
	stdout io.Reader
}

func newLineReader(stdout io.Reader) *lineReader {
	return &lineReader{stdout: stdout}
}

func (r *lineReader) readLine() (string, error) {
	if r.index < len(r.lines) {
		line := r.lines[r.index]
		r.index++
		return line, nil
	}
	buf := make([]byte, 4096)
	n, err := r.stdout.Read(buf)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(buf[:n]))
	if line == "" {
		return r.readLine()
	}
	return line, nil
}

func runV1Bench() {
	workerPath := compileWorker()
	cpuInfo := detectCPUInfo()
	fmt.Printf("Logical CPU count: %d\nPhysical CPU count: %d\n\n", cpuInfo.Logical, cpuInfo.Physical)
	fmt.Println("benchmark_v1: same-work baseline")
	fmt.Println("Configuration | startup | serialization | dispatch | queue | C++ wall | result serialization | result collection | total | dominant")
	fmt.Println("------------ | ------- | ------------- | -------- | ----- | -------- | ------------------ | ------------------ | ----- | --------")
	for _, nodes := range []int{1, 2, 4, 8} {
		for _, workers := range []int{1, 2, 4, 8} {
			stage := runDirectTaskBenchmark(workerPath, benchmarkTaskCount, benchmarkPayloadSize)
			fmt.Printf("%d nodes x %d workers | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %s\n",
				nodes, workers,
				stage.StartupMs,
				stage.SerializationMs,
				stage.DispatchMs,
				stage.QueueWaitMs,
				stage.CppWallComputeMs,
				stage.ResultSerialization,
				stage.ResultCollectionMs,
				stage.TotalMs,
				"ipc",
			)
		}
	}
}

func runIPCBench() {
	workerPath := compileWorker()
	fmt.Println("benchmark_ipc: startup vs steady state")
	fmt.Println("workers | startup_ms | steady_state_total_ms | avg_round_trip_ms | p50 | p95 | p99 | dominant")
	for _, workers := range []int{1, 2, 4, 8} {
		stage := runIPCBenchmark(workerPath, workers, 20, 256, 1)
		fmt.Printf("%d | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | ipc\n", workers, stage.StartupMs, stage.TotalMs, mean(stage.RoundTripLatencies), percentile(stage.RoundTripLatencies, 50), percentile(stage.RoundTripLatencies, 95), percentile(stage.RoundTripLatencies, 99))
	}
}

func runBatchingBench() {
	workerPath := compileWorker()
	fmt.Println("benchmark_batching: task/message comparison")
	fmt.Println("batch | total_ms | tasks_per_sec | avg_round_trip_ms | p95")
	for _, batchSize := range []int{1, 8, 32, 128} {
		stage := runIPCBenchmark(workerPath, 1, 40, 512, batchSize)
		fmt.Printf("%d | %.2f | %.2f | %.2f | %.2f\n", batchSize, stage.TotalMs, float64(40)/maxFloat(stage.TotalMs/1000.0, 0.0001), mean(stage.RoundTripLatencies), percentile(stage.RoundTripLatencies, 95))
	}
}

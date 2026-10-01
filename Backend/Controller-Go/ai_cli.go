package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type aiCLIOptions struct {
	workers    uint32
	strategy   AIExecutionStrategy
	runtime    string
	entryPoint string
	precision  string
	cpu        uint32
	ramGB      uint64
	gpuCount   uint32
	vramGB     uint64
	arguments  []string
}

func cliAI(args []string) error {
	if len(args) == 0 {
		printAIHelp()
		return nil
	}
	switch strings.ToLower(args[0]) {
	case "help", "--help", "-h":
		printAIHelp()
		return nil
	case "inspect":
		if len(args) != 2 {
			return fmt.Errorf("usage: nodren ai inspect <model-or-folder>")
		}
		return cliAIInspect(args[1])
	case "plan", "run":
		path, options, err := parseAIArguments(args[1:])
		if err != nil {
			return err
		}
		spec, cleanup, err := cliAIWorkload(path, options)
		if err != nil {
			return err
		}
		defer cleanup()
		if strings.EqualFold(args[0], "plan") {
			plan, err := newCLIClient().planAI(spec)
			if err != nil {
				return err
			}
			printAIPlan(plan)
			return nil
		}
		execution, err := newCLIClient().startAI(spec)
		if err != nil {
			return err
		}
		fmt.Printf("AI execution %s started with %d ranks\n", execution.ID, len(execution.TaskIDs))
		printAIPlan(execution.Plan)
		return nil
	case "workers":
		workers, err := newCLIClient().aiWorkers()
		if err != nil {
			return err
		}
		for _, worker := range workers {
			fmt.Printf("%s state=%s GPU=%s CPU=%d RAM=%dGB\n", worker.Info.ID, worker.State, gpuDescription(worker.Info.GPU), worker.Info.CPUCores, worker.Info.RAMGB)
		}
		return nil
	case "status":
		if len(args) != 2 {
			return fmt.Errorf("usage: nodren ai status <execution-id>")
		}
		execution, err := newCLIClient().aiExecution(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Execution %s status=%s strategy=%s\n", execution.ID, execution.Status, execution.Plan.Strategy)
		printAIPlan(execution.Plan)
		fmt.Printf("Tasks       %s\n", strings.Join(execution.TaskIDs, ", "))
		return nil
	case "cancel":
		if len(args) != 2 {
			return fmt.Errorf("usage: nodren ai cancel <execution-id>")
		}
		execution, err := newCLIClient().aiExecutionAction(args[1], "cancel")
		if err != nil {
			return err
		}
		fmt.Printf("AI execution %s: %s\n", execution.ID, execution.Status)
		return nil
	case "checkpoint":
		return fmt.Errorf("AI checkpoint upload/resume is not implemented yet")
	default:
		return fmt.Errorf("unknown AI command %q; available: inspect, plan, run, workers, status, cancel", args[0])
	}
}

func printAIHelp() {
	fmt.Println(`Usage: nodren ai <command> [arguments]

Commands:
  inspect <path>                         Inspect a model file or project folder
  plan <path> [options]                  Build a capability-aware execution plan
  run <path> [options] [-- arguments...] Start an AI execution
  workers                                List AI-capable workers
  status <execution-id>                  Show an AI execution
  cancel <execution-id>                  Cancel an AI execution

The distributed-process adapter currently supports SINGLE and DISTRIBUTED_PROCESS.
Tensor/pipeline parallelism, checkpoint upload, and output artifact collection are
rejected until a runtime adapter implements them.`)
}

func parseAIArguments(args []string) (string, aiCLIOptions, error) {
	if len(args) == 0 || args[0] == "--" {
		return "", aiCLIOptions{}, fmt.Errorf("usage: nodren ai run <model-or-folder> [options] [-- arguments...]")
	}
	options := aiCLIOptions{workers: 1, cpu: 1, ramGB: 1}
	path := args[0]
	separator := len(args)
	for index, value := range args[1:] {
		if value == "--" {
			separator = index + 1
			break
		}
	}
	for index := 1; index < separator; index++ {
		value := args[index]
		name, argument, hasValue := strings.Cut(value, "=")
		if !hasValue {
			name = value
		}
		readValue := func() (string, error) {
			if hasValue {
				return argument, nil
			}
			if index+1 >= separator {
				return "", fmt.Errorf("option %s requires a value", name)
			}
			index++
			return args[index], nil
		}
		switch strings.ToLower(name) {
		case "--workers":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			value, err := strconv.ParseUint(text, 10, 32)
			if err != nil || value == 0 {
				return "", options, fmt.Errorf("invalid --workers value %q", text)
			}
			options.workers = uint32(value)
		case "--strategy":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			options.strategy = AIExecutionStrategy(strings.ReplaceAll(strings.ToUpper(text), "-", "_"))
		case "--runtime":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			options.runtime = text
		case "--entrypoint":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			options.entryPoint = text
		case "--precision":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			options.precision = text
		case "--cpu":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			value, parseErr := strconv.ParseUint(text, 10, 32)
			if parseErr != nil || value == 0 {
				return "", options, fmt.Errorf("invalid --cpu value %q", text)
			}
			options.cpu = uint32(value)
		case "--ram":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			options.ramGB, err = parseAISizeGB(text)
			if err != nil {
				return "", options, err
			}
		case "--gpu", "--gpu-count":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			value, parseErr := strconv.ParseUint(text, 10, 32)
			if parseErr != nil || value == 0 {
				return "", options, fmt.Errorf("invalid GPU count %q", text)
			}
			options.gpuCount = uint32(value)
		case "--gpu-vram":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			options.vramGB, err = parseAISizeGB(text)
			if err != nil {
				return "", options, err
			}
		default:
			return "", options, fmt.Errorf("unknown AI option %q", value)
		}
	}
	if separator < len(args) {
		options.arguments = append([]string(nil), args[separator+1:]...)
	}
	return path, options, nil
}

func parseAISizeGB(value string) (uint64, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	multiplier := float64(1)
	switch {
	case strings.HasSuffix(normalized, "GB"):
		normalized = strings.TrimSpace(strings.TrimSuffix(normalized, "GB"))
	case strings.HasSuffix(normalized, "MB"):
		normalized = strings.TrimSpace(strings.TrimSuffix(normalized, "MB"))
		multiplier = 1.0 / 1024.0
	default:
		return 0, fmt.Errorf("size %q must use GB or MB", value)
	}
	amount, err := strconv.ParseFloat(normalized, 64)
	if err != nil || amount <= 0 {
		return 0, fmt.Errorf("invalid size %q", value)
	}
	return uint64(math.Ceil(amount * multiplier)), nil
}

func cliAIWorkload(path string, options aiCLIOptions) (AIWorkloadSpec, func(), error) {
	info, err := os.Stat(path)
	if err != nil {
		return AIWorkloadSpec{}, func() {}, &cliError{message: "Cannot inspect AI workload: " + err.Error()}
	}
	if options.strategy == "" {
		if options.workers > 1 {
			options.strategy = AIStrategyDistributedProcess
		} else {
			options.strategy = AIStrategySingle
		}
	}
	spec := AIWorkloadSpec{
		Type:        "AI",
		Adapter:     "distributed-process",
		WorkerCount: options.workers,
		Strategy:    options.strategy,
		Runtime:     options.runtime,
		EntryPoint:  options.entryPoint,
		Precision:   options.precision,
		Arguments:   options.arguments,
		Requirements: ResourceRequirements{
			CPUCores:    options.cpu,
			RAMGB:       options.ramGB,
			GPURequired: options.gpuCount > 0 || options.vramGB > 0,
			GPUCount:    options.gpuCount,
			VRAMGB:      options.vramGB,
		},
		Model: AIModel{Name: filepath.Base(path), Runtime: options.runtime, Precision: options.precision},
	}
	cleanup := func() {}
	if info.IsDir() {
		packagePath, manifest, packageErr := buildTaskPackage(path)
		if packageErr != nil {
			return AIWorkloadSpec{}, cleanup, packageErr
		}
		cleanup = func() { _ = os.Remove(packagePath) }
		artifact, uploadErr := newCLIClient().uploadArtifactWithKind(packagePath, "model")
		if uploadErr != nil {
			cleanup()
			return AIWorkloadSpec{}, func() {}, uploadErr
		}
		artifact.Name = "package.tar"
		artifact.Kind = "package"
		spec.Model.Artifacts = []TaskArtifact{artifact}
		if spec.EntryPoint == "" {
			spec.EntryPoint = manifest.EntryPoint
		}
		if spec.Runtime == "" {
			spec.Runtime = manifest.Runtime
		}
		if len(spec.Arguments) == 0 {
			spec.Arguments = append([]string(nil), manifest.Arguments...)
		}
		if len(manifest.Environment) > 0 {
			spec.Environment = cloneStringMap(manifest.Environment)
		}
		if spec.Runtime == "" && manifest.Executable != "" && spec.EntryPoint == "" {
			spec.EntryPoint = manifest.Executable
		}
	} else {
		artifact, uploadErr := newCLIClient().uploadArtifactWithKind(path, "model")
		if uploadErr != nil {
			return AIWorkloadSpec{}, cleanup, uploadErr
		}
		artifact.Name = filepath.Base(path)
		spec.Model.Artifacts = []TaskArtifact{artifact}
		if spec.EntryPoint == "" {
			spec.EntryPoint = filepath.Base(path)
		}
	}
	if spec.EntryPoint == "" {
		cleanup()
		return AIWorkloadSpec{}, func() {}, errors.New("AI workload needs --entrypoint or a package manifest entry point")
	}
	return spec, cleanup, nil
}

func cliAIInspect(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return &cliError{message: "Cannot inspect AI workload: " + err.Error()}
	}
	fmt.Printf("Path        %s\nKind        %s\n", path, map[bool]string{true: "folder/project", false: "file/model"}[info.IsDir()])
	if !info.IsDir() {
		fmt.Printf("Size        %d bytes\n", info.Size())
		return nil
	}
	packagePath, manifest, err := buildTaskPackage(path)
	if err != nil {
		return err
	}
	defer os.Remove(packagePath)
	info, err = os.Stat(packagePath)
	if err != nil {
		return err
	}
	fmt.Printf("Package     %d bytes\nEntry point %s\nRuntime     %s\n", info.Size(), manifest.EntryPoint, manifest.Runtime)
	return nil
}

func printAIPlan(plan AIExecutionPlan) {
	fmt.Printf("Execution   %s\nGroup       %s\nAdapter     %s\nStrategy    %s\nWorld size  %d\nLeader      %s\n", plan.ExecutionID, plan.GroupID, plan.Adapter, plan.Strategy, plan.WorldSize, plan.LeaderWorkerID)
	for _, worker := range plan.Workers {
		fmt.Printf("  rank=%d worker=%s gpu=%d vram=%dGB\n", worker.Rank, worker.WorkerID, worker.GPUIndex, worker.VRAMGB)
	}
	fmt.Printf("Communication %s (controller-mediated=%t)\n", plan.Communication.Mode, plan.Communication.ControllerMediated)
}

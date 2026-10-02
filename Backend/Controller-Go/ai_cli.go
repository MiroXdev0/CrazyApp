package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type aiCLIOptions struct {
	workers     uint32
	strategy    AIExecutionStrategy
	runtime     string
	entryPoint  string
	precision   string
	device      string
	input       string
	output      string
	workerID    string
	cpu         uint32
	cpuAuto     bool
	context     uint64
	gpuLayers   int32
	temperature float64
	maxTokens   uint32
	ramGB       uint64
	gpuCount    uint32
	vramGB      uint64
	arguments   []string
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
		printAIExecutionDetails(execution)
		streaming := strings.EqualFold(spec.Model.Format, "gguf")
		var stopOutputStream func()
		if streaming {
			stopOutputStream, err = newCLIClient().streamAIOutput(execution.TaskIDs)
			if err != nil {
				fmt.Printf("Streaming unavailable; output will be shown after completion: %v\n", err)
				stopOutputStream = func() {}
			}
			defer stopOutputStream()
		}
		if strings.EqualFold(args[0], "run") {
			fmt.Println("Waiting for inference result...")
			final, waitErr := waitForAIExecution(newCLIClient(), execution.ID)
			if waitErr != nil {
				return waitErr
			}
			fmt.Printf("AI execution %s finished: %s\n", final.ID, final.Status)
			for _, taskID := range final.TaskIDs {
				result, resultErr := newCLIClient().taskResult(taskID)
				if resultErr != nil {
					return resultErr
				}
				if streaming {
					fmt.Printf("\nStatus       %s\nDuration     %d us\n", result.Status, result.DurationUS)
					if result.Error != "" {
						fmt.Printf("Error        %s %s\n", result.ErrorCode, result.Error)
					}
					for _, artifact := range result.OutputArtifacts {
						fmt.Printf("Output artifact: %s  %d bytes  sha256=%s\n", artifact.Name, artifact.Size, artifact.SHA256)
					}
				} else {
					printTaskResult(result)
				}
				if options.output != "" {
					if len(result.OutputArtifacts) == 0 {
						return errors.New("inference completed without the requested output artifact")
					}
					for _, artifact := range result.OutputArtifacts {
						if err := newCLIClient().downloadArtifact(artifact.ID, options.output); err != nil {
							return err
						}
						fmt.Printf("Result file  %s\n", options.output)
					}
				}
			}
		}
		return nil
	case "workers":
		workers, err := newCLIClient().aiWorkers()
		if err != nil {
			return err
		}
		for _, worker := range workers {
			fmt.Printf("%s state=%s GPU=%s CPU=%d RAM=%dGB runtimes=%s capabilities=%s\n", worker.Info.ID, worker.State, gpuDescription(worker.Info.GPU), worker.Info.CPUCores, worker.Info.RAMGB, strings.Join(worker.Info.Runtimes, ","), strings.Join(worker.Info.Capabilities, ","))
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
		fmt.Printf("Execution %s status=%s phase=%s strategy=%s progress_known=%t progress=%.1f%%\n", execution.ID, execution.Status, execution.Phase, execution.Plan.Strategy, execution.ProgressKnown, execution.ProgressPercent)
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

func waitForAIExecution(client *cliClient, id string) (AIExecution, error) {
	deadline := time.Now().Add(24 * time.Hour)
	for time.Now().Before(deadline) {
		execution, err := client.aiExecution(id)
		if err != nil {
			return AIExecution{}, err
		}
		switch execution.Status {
		case "COMPLETED", "FAILED", "CANCELLED":
			return execution, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return AIExecution{}, fmt.Errorf("timed out waiting for AI execution %s", id)
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

The built-in ONNX Runtime adapter supports real CPU inference and CUDA inference
when the selected worker advertises onnxruntime (and onnxruntime-cuda for GPU).
The llama.cpp adapter supports real GGUF execution when the selected worker
has an executable llama.cpp CLI build. Use --input <json|npy|npz|prompt.txt>,
--output <json|npy|npz|response.txt>, --device auto|cpu|gpu,
and --worker <id> where supported. GGUF uses --input as a prompt file and
supports --context, --gpu-layers, --temperature, and --max-tokens. The distributed-process
adapter currently supports SINGLE and DISTRIBUTED_PROCESS. CPU defaults to all
available logical CPUs on the selected worker; use --cpu N to share capacity.
Tensor/pipeline parallelism, checkpoint upload, and output artifact collection are
rejected until a runtime adapter implements them.`)
}

func parseAIArguments(args []string) (string, aiCLIOptions, error) {
	if len(args) == 0 || args[0] == "--" {
		return "", aiCLIOptions{}, fmt.Errorf("usage: nodren ai run <model-or-folder> [options] [-- arguments...]")
	}
	options := aiCLIOptions{workers: 1, cpuAuto: true, device: "auto"}
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
		case "--device":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			text = strings.ToLower(strings.TrimSpace(text))
			if text != "auto" && text != "cpu" && text != "gpu" && text != "cuda" {
				return "", options, fmt.Errorf("invalid --device value %q", text)
			}
			options.device = text
		case "--input":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			options.input = text
		case "--output":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			options.output = text
		case "--worker":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			options.workerID = strings.TrimSpace(text)
			if options.workerID == "" {
				return "", options, errors.New("--worker requires a worker id")
			}
		case "--cpu":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			if strings.EqualFold(strings.TrimSpace(text), "auto") {
				options.cpu = 0
				options.cpuAuto = true
				continue
			}
			value, parseErr := strconv.ParseUint(text, 10, 32)
			if parseErr != nil || value == 0 {
				return "", options, fmt.Errorf("invalid --cpu value %q", text)
			}
			options.cpu = uint32(value)
			options.cpuAuto = false
		case "--context":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			value, parseErr := strconv.ParseUint(text, 10, 64)
			if parseErr != nil || value == 0 {
				return "", options, fmt.Errorf("invalid --context value %q", text)
			}
			options.context = value
		case "--gpu-layers":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			value, parseErr := strconv.ParseInt(text, 10, 32)
			if parseErr != nil || value < -1 {
				return "", options, fmt.Errorf("invalid --gpu-layers value %q", text)
			}
			options.gpuLayers = int32(value)
		case "--temperature":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			value, parseErr := strconv.ParseFloat(text, 64)
			if parseErr != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
				return "", options, fmt.Errorf("invalid --temperature value %q", text)
			}
			options.temperature = value
		case "--max-tokens":
			text, err := readValue()
			if err != nil {
				return "", options, err
			}
			value, parseErr := strconv.ParseUint(text, 10, 32)
			if parseErr != nil || value == 0 {
				return "", options, fmt.Errorf("invalid --max-tokens value %q", text)
			}
			options.maxTokens = uint32(value)
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
	inspection, inspectErr := inspectAIPath(path)
	if inspectErr != nil {
		return AIWorkloadSpec{}, func() {}, &cliError{message: "Cannot inspect AI model: " + inspectErr.Error()}
	}
	device := options.device
	if device == "cpu" && (options.gpuCount > 0 || options.vramGB > 0) {
		return AIWorkloadSpec{}, func() {}, errors.New("--device cpu cannot be combined with --gpu or --gpu-vram")
	}
	if device == "cpu" && options.gpuLayers != 0 {
		return AIWorkloadSpec{}, func() {}, errors.New("--gpu-layers requires GPU-capable execution")
	}
	if device == "auto" && (options.gpuCount > 0 || options.vramGB > 0) {
		device = "gpu"
	}
	spec := AIWorkloadSpec{
		Type:        "AI",
		Adapter:     "distributed-process",
		WorkerCount: options.workers,
		Strategy:    options.strategy,
		Runtime:     options.runtime,
		Device:      device,
		CPUAuto:     options.cpuAuto,
		Target:      TaskTarget{AllowedWorkerIDs: nonEmptyStringSlice(options.workerID)},
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
		Model: AIModel{
			Name:            filepath.Base(path),
			Runtime:         options.runtime,
			Precision:       options.precision,
			Format:          inspection.Format,
			Architecture:    inspection.Architecture,
			Quantization:    inspection.Quantization,
			ContextLength:   inspection.ContextLength,
			TensorCount:     inspection.TensorCount,
			ParameterCount:  inspection.ParameterCount,
			SizeBytes:       inspection.SizeBytes,
			EstimatedRAMGB:  inspection.EstimatedRAMGB,
			EstimatedVRAMGB: inspection.EstimatedVRAMGB,
			Inspection:      &inspection,
		},
		ContextSize: options.context,
		GPULayers:   options.gpuLayers,
		Temperature: options.temperature,
		MaxTokens:   options.maxTokens,
	}
	cleanup := func() {}
	generatedRuntime := false
	_, hasPackageManifest := os.Stat(filepath.Join(path, "nodren.json"))
	runtimeAdapter := findAIRuntimeAdapter(inspection.Format, options.runtime)
	if runtimeAdapter != nil && inspection.Format == "onnx" && options.entryPoint == "" && (!info.IsDir() || hasPackageManifest != nil) {
		modelPath, modelErr := findAIModelFile(path, "onnx")
		if modelErr != nil {
			return AIWorkloadSpec{}, func() {}, modelErr
		}
		packagePath, manifest, packageCleanup, packageErr := buildGeneratedONNXPackage(modelPath, options.input, options.output, device)
		if packageErr != nil {
			return AIWorkloadSpec{}, func() {}, packageErr
		}
		artifact, uploadErr := newCLIClient().uploadArtifactWithKind(packagePath, "model")
		packageCleanup()
		if uploadErr != nil {
			return AIWorkloadSpec{}, func() {}, uploadErr
		}
		artifact.Name = "package.tar"
		artifact.Kind = "package"
		spec.Model.Artifacts = []TaskArtifact{artifact}
		spec.EntryPoint = manifest.EntryPoint
		spec.Runtime = manifest.Runtime
		spec.Arguments = append([]string(nil), manifest.Arguments...)
		spec.Model.Runtime = runtimeAdapter.Name()
		spec.Model.Inspection.ExecutionReady = true
		spec.Model.Inspection.InspectionOnly = false
		spec.Target.RequiredCapabilities = append(spec.Target.RequiredCapabilities, runtimeAdapter.RequiredWorkerCapabilities(device)...)
		if options.output != "" {
			outputName := "result" + strings.ToLower(filepath.Ext(options.output))
			spec.OutputArtifacts = []TaskArtifact{{ID: fmt.Sprintf("AI-OUT-%d", time.Now().UnixNano()), Name: outputName, Kind: "output"}}
		}
		generatedRuntime = true
	} else if info.IsDir() {
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
		spec.Model.Inspection.ExecutionReady = spec.EntryPoint != "" && (spec.Runtime != "" || manifest.Executable != "")
	} else if runtimeAdapter != nil && runtimeAdapter.Name() == "llama.cpp" && options.entryPoint == "" {
		artifact, uploadErr := newCLIClient().uploadArtifactWithKind(path, "model")
		if uploadErr != nil {
			return AIWorkloadSpec{}, cleanup, uploadErr
		}
		artifact.Name = filepath.Base(path)
		artifact.Kind = "model"
		spec.Model.Artifacts = []TaskArtifact{artifact}
		if options.input != "" {
			prompt, promptErr := newCLIClient().uploadArtifactWithKind(options.input, "prompt")
			if promptErr != nil {
				return AIWorkloadSpec{}, cleanup, promptErr
			}
			prompt.Name = filepath.Base(options.input)
			prompt.Kind = "prompt"
			spec.DatasetArtifacts = []TaskArtifact{prompt}
		}
		spec.Runtime = runtimeAdapter.Name()
		spec.Model.Runtime = runtimeAdapter.Name()
		spec.Model.Inspection.ExecutionReady = true
		spec.Model.Inspection.InspectionOnly = false
		spec.Target.RequiredCapabilities = append(spec.Target.RequiredCapabilities, runtimeAdapter.RequiredWorkerCapabilities(device)...)
		if options.output != "" {
			spec.OutputArtifacts = []TaskArtifact{{ID: fmt.Sprintf("AI-OUT-%d", time.Now().UnixNano()), Name: "response.txt", Kind: "output"}}
		}
	} else {
		if runtimeAdapter != nil && runtimeAdapter.Name() == "llama.cpp" && options.entryPoint == "" {
			return AIWorkloadSpec{}, func() {}, errors.New("llama.cpp runtime adapter could not prepare the GGUF workload")
		}
		if inspection.Format != "" && options.entryPoint == "" && options.runtime == "" {
			return AIWorkloadSpec{}, func() {}, errors.New("recognized model format has no built-in runtime adapter; for ONNX, use --device and ensure the worker advertises onnxruntime")
		}
		artifact, uploadErr := newCLIClient().uploadArtifactWithKind(path, "model")
		if uploadErr != nil {
			return AIWorkloadSpec{}, cleanup, uploadErr
		}
		artifact.Name = filepath.Base(path)
		spec.Model.Artifacts = []TaskArtifact{artifact}
		if spec.EntryPoint == "" {
			spec.EntryPoint = filepath.Base(path)
		}
		spec.Model.Inspection.ExecutionReady = options.entryPoint != "" && options.runtime != ""
	}
	if !generatedRuntime && spec.EntryPoint == "" {
		cleanup()
		return AIWorkloadSpec{}, func() {}, errors.New("AI workload needs --entrypoint or a package manifest entry point")
	}
	if options.output != "" && !generatedRuntime && runtimeAdapter == nil {
		cleanup()
		return AIWorkloadSpec{}, func() {}, errors.New("--output requires a built-in runtime adapter that supports result artifacts")
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
		inspection, inspectErr := inspectAIPath(path)
		if inspectErr != nil {
			return inspectErr
		}
		printAIInspection(inspection)
		return nil
	}
	inspection, inspectErr := inspectAIPath(path)
	if inspectErr != nil {
		return inspectErr
	}
	packagePath, manifest, err := buildTaskPackage(path)
	if err != nil {
		printAIInspection(inspection)
		return fmt.Errorf("model inspection completed, but the folder is not executable as a Nodren package: %w", err)
	}
	defer os.Remove(packagePath)
	info, err = os.Stat(packagePath)
	if err != nil {
		return err
	}
	fmt.Printf("Package     %d bytes\nEntry point %s\nRuntime     %s\n", info.Size(), manifest.EntryPoint, manifest.Runtime)
	printAIInspection(inspection)
	return nil
}

func printAIInspection(inspection AIModelInspection) {
	fmt.Printf("Format      %s\nStatus      %s\n", valueOrUnknown(inspection.Format), valueOrUnknown(inspection.Status))
	fmt.Printf("Architecture %s\nParameters  %d\nTensors     %d\nQuantize    %s\nContext     %d\nSize        %d bytes\nEstimated RAM  %d GB\nEstimated VRAM %d GB\n", valueOrUnknown(inspection.Architecture), inspection.ParameterCount, inspection.TensorCount, valueOrUnknown(inspection.Quantization), inspection.ContextLength, inspection.SizeBytes, inspection.EstimatedRAMGB, inspection.EstimatedVRAMGB)
	if len(inspection.RuntimeRequirements) > 0 {
		fmt.Printf("Runtime requirements: %s\n", strings.Join(inspection.RuntimeRequirements, ", "))
	}
	if inspection.SupportedRuntime != "" {
		fmt.Printf("Built-in runtime %s\n", inspection.SupportedRuntime)
	}
	if len(inspection.Diagnostics) > 0 {
		fmt.Println("Diagnostics:")
		for _, diagnostic := range inspection.Diagnostics {
			fmt.Printf("  - %s\n", diagnostic)
		}
	}
}

func valueOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func nonEmptyStringSlice(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return []string{value}
}

func printAIPlan(plan AIExecutionPlan) {
	fmt.Printf("Execution   %s\nGroup       %s\nAdapter     %s\nStrategy    %s\nWorld size  %d\nLeader      %s\n", plan.ExecutionID, plan.GroupID, plan.Adapter, plan.Strategy, plan.WorldSize, plan.LeaderWorkerID)
	for _, worker := range plan.Workers {
		fmt.Printf("  rank=%d worker=%s device=%s gpu=%d vram=%dGB cpu=%d\n", worker.Rank, worker.WorkerID, valueOrUnknown(worker.Device), worker.GPUIndex, worker.VRAMGB, worker.CPUCores)
	}
	fmt.Printf("Communication %s (controller-mediated=%t)\n", plan.Communication.Mode, plan.Communication.ControllerMediated)
}

func printAIExecutionDetails(execution AIExecution) {
	worker := "-"
	cpu := uint32(0)
	vram := uint64(0)
	if len(execution.Plan.Workers) > 0 {
		worker = execution.Plan.Workers[0].WorkerID
		cpu = execution.Plan.Workers[0].CPUCores
		vram = execution.Plan.Workers[0].VRAMGB
	}
	fmt.Printf("Model       %s\nFormat      %s\nRuntime     %s\nWorker      %s\nDevice      %s\nCPU         %d\nRAM         %d GB\nVRAM        %d GB\nStatus      %s\nPhase       %s\n", execution.Plan.Workload.Model.Name, valueOrUnknown(execution.Plan.Workload.Model.Format), valueOrUnknown(execution.Plan.Workload.Model.Runtime), worker, execution.Plan.Workload.Device, cpu, execution.Plan.Workload.Requirements.RAMGB, vram, execution.Status, execution.Phase)
}

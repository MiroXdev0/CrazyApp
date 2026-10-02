package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The script is embedded so a released nodren.exe does not depend on the
// source checkout being present when it creates a worker package.
//
//go:embed ai_onnx_runner.py
var onnxRunner []byte

const onnxRuntimeAdapterName = "onnxruntime-python"

// AIRuntimeAdapter is the controller-side contract for a model runtime. The
// worker remains authoritative for installed packages and provider support;
// this interface describes runtime capabilities and the worker launch contract.
type AIRuntimeAdapter interface {
	Name() string
	Supports(format string) bool
	Formats() []string
	SupportsCPU() bool
	SupportsGPU() bool
	GPUBackend() string
	Dependencies() []string
	RequiredWorkerCapabilities(device string) []string
}

type onnxRuntimeAdapter struct{}

type llamaCPPAdapter struct{}

func (onnxRuntimeAdapter) Name() string { return onnxRuntimeAdapterName }

func (onnxRuntimeAdapter) Supports(format string) bool { return strings.EqualFold(format, "onnx") }

func (onnxRuntimeAdapter) Formats() []string { return []string{"onnx"} }

func (onnxRuntimeAdapter) SupportsCPU() bool { return true }

func (onnxRuntimeAdapter) SupportsGPU() bool { return true }

func (onnxRuntimeAdapter) GPUBackend() string { return "CUDA" }

func (onnxRuntimeAdapter) Dependencies() []string { return []string{"python", "onnxruntime", "numpy"} }

func (onnxRuntimeAdapter) RequiredWorkerCapabilities(device string) []string {
	capabilities := []string{"onnxruntime"}
	if strings.EqualFold(device, "gpu") || strings.EqualFold(device, "cuda") {
		capabilities = append(capabilities, "onnxruntime-cuda")
	}
	return capabilities
}

func (llamaCPPAdapter) Name() string { return "llama.cpp" }

func (llamaCPPAdapter) Supports(format string) bool { return strings.EqualFold(format, "gguf") }

func (llamaCPPAdapter) Formats() []string { return []string{"gguf"} }

func (llamaCPPAdapter) SupportsCPU() bool { return true }

func (llamaCPPAdapter) SupportsGPU() bool { return true }

func (llamaCPPAdapter) GPUBackend() string { return "llama.cpp backend (runtime-dependent)" }

func (llamaCPPAdapter) Dependencies() []string { return []string{"llama.cpp executable"} }

func (llamaCPPAdapter) RequiredWorkerCapabilities(device string) []string {
	capabilities := []string{"llama.cpp"}
	if strings.EqualFold(device, "gpu") || strings.EqualFold(device, "cuda") {
		capabilities = append(capabilities, "llama.cpp-gpu")
	}
	return capabilities
}

func buildLlamaCPPLaunch(spec AIWorkloadSpec, plan AIExecutionPlan, worker AIWorkerAssignment) GeneralTaskSpec {
	environment := cloneStringMap(spec.Environment)
	if environment == nil {
		environment = make(map[string]string)
	}
	device := worker.Device
	if device == "" {
		device = spec.Device
		if device == "auto" {
			device = "cpu"
		}
	}
	modelName := ""
	inputArtifacts := append([]TaskArtifact(nil), spec.Model.Artifacts...)
	if len(spec.Model.Artifacts) > 0 {
		modelName = spec.Model.Artifacts[0].Name
	}
	arguments := []string{"-m", modelName, "-t", fmt.Sprintf("%d", maxUint32(spec.Requirements.CPUCores, 1))}
	if spec.ContextSize > 0 {
		arguments = append(arguments, "-c", fmt.Sprintf("%d", spec.ContextSize))
	}
	if spec.MaxTokens > 0 {
		arguments = append(arguments, "-n", fmt.Sprintf("%d", spec.MaxTokens))
	}
	if spec.Temperature > 0 {
		arguments = append(arguments, "--temp", fmt.Sprintf("%g", spec.Temperature))
	}
	if device == "gpu" {
		layers := spec.GPULayers
		if layers == 0 {
			layers = -1
		}
		arguments = append(arguments, "-ngl", fmt.Sprintf("%d", layers))
	}
	if len(spec.DatasetArtifacts) > 0 {
		prompt := spec.DatasetArtifacts[0]
		inputArtifacts = append(inputArtifacts, prompt)
		arguments = append(arguments, "-f", prompt.Name)
	} else {
		arguments = append(arguments, "-p", "")
	}
	arguments = append(arguments, spec.Arguments...)
	environment["NODREN_AI_ADAPTER"] = "llama.cpp"
	environment["NODREN_AI_EXECUTION_ID"] = plan.ExecutionID
	environment["NODREN_AI_GROUP_ID"] = plan.GroupID
	environment["NODREN_AI_WORKER_ID"] = worker.WorkerID
	environment["NODREN_AI_DEVICE"] = device
	environment["NODREN_AI_CPU_THREADS"] = fmt.Sprintf("%d", maxUint32(spec.Requirements.CPUCores, 1))
	environment["NODREN_AI_MODEL_FORMAT"] = "gguf"
	environment["NODREN_AI_RUNTIME"] = "llama.cpp"
	environment["NODREN_AI_MODEL_SHA256"] = modelArtifactHash(spec.Model.Artifacts)
	if device == "gpu" {
		environment["NODREN_AI_GPU_INDEX"] = fmt.Sprintf("%d", worker.GPUIndex)
		environment["CUDA_VISIBLE_DEVICES"] = fmt.Sprintf("%d", worker.GPUIndex)
	}
	target := spec.Target
	target.AllowedWorkerIDs = []string{worker.WorkerID}
	target.PreferredWorkerID = worker.WorkerID
	return GeneralTaskSpec{
		Type:             TaskTypeProcess,
		Version:          "1",
		Executable:       "__nodren_llama_cpp__",
		Runtime:          "llama.cpp",
		Arguments:        arguments,
		Environment:      environment,
		InputArtifacts:   inputArtifacts,
		OutputArtifacts:  append([]TaskArtifact(nil), spec.OutputArtifacts...),
		Requirements:     spec.Requirements,
		Target:           target,
		WorkloadKind:     "ai-llama.cpp",
		Strategy:         ExecutionSingle,
		RequiredWorkers:  1,
		Replicas:         1,
		StdoutLimitBytes: 1 << 20,
	}
}

func modelArtifactHash(artifacts []TaskArtifact) string {
	if len(artifacts) == 0 {
		return ""
	}
	return artifacts[0].SHA256
}

var aiRuntimeAdapters = []AIRuntimeAdapter{onnxRuntimeAdapter{}, llamaCPPAdapter{}}

func findAIRuntimeAdapter(format, runtime string) AIRuntimeAdapter {
	runtime = strings.ToLower(strings.TrimSpace(runtime))
	if !onnxRuntimeRequested(runtime) && !(strings.EqualFold(format, "gguf") && (runtime == "" || runtime == "llama.cpp" || runtime == "llama-cpp" || runtime == "llama-cli")) {
		return nil
	}
	for _, adapter := range aiRuntimeAdapters {
		if adapter.Supports(format) {
			return adapter
		}
	}
	return nil
}

func onnxRuntimeRequested(runtime string) bool {
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "", "python", "python3", "onnxruntime", onnxRuntimeAdapterName:
		return true
	default:
		return false
	}
}

func findAIModelFile(root, format string) (string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return root, nil
	}
	var candidates []string
	err = filepath.Walk(root, func(path string, entry os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (entry.Name() == ".git" || entry.Name() == "target" || entry.Name() == "__pycache__") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(recognizedAIFormat(filepath.Ext(path)), format) {
			candidates = append(candidates, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no %s model file was found in %s", format, root)
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, _ := os.Stat(candidates[i])
		right, _ := os.Stat(candidates[j])
		if left == nil || right == nil || left.Size() == right.Size() {
			return candidates[i] < candidates[j]
		}
		return left.Size() > right.Size()
	})
	return candidates[0], nil
}

func buildGeneratedONNXPackage(modelPath, inputPath, outputPath, device string) (string, localPackageManifest, func(), error) {
	modelInfo, err := os.Stat(modelPath)
	if err != nil || modelInfo.IsDir() {
		return "", localPackageManifest{}, func() {}, fmt.Errorf("ONNX model file is not readable: %s", modelPath)
	}
	root, err := os.MkdirTemp("", "nodren-onnx-package-*")
	if err != nil {
		return "", localPackageManifest{}, func() {}, err
	}
	cleanupRoot := func() { _ = os.RemoveAll(root) }
	modelName := filepath.Base(modelPath)
	if err := copyLocalFile(modelPath, filepath.Join(root, modelName)); err != nil {
		cleanupRoot()
		return "", localPackageManifest{}, func() {}, err
	}
	if err := os.WriteFile(filepath.Join(root, "run_onnx.py"), onnxRunner, 0o644); err != nil {
		cleanupRoot()
		return "", localPackageManifest{}, func() {}, err
	}
	manifest := localPackageManifest{
		TaskPackageManifest: TaskPackageManifest{
			EntryPoint: "run_onnx.py",
			Runtime:    "python",
			Arguments:  []string{"--model", modelName, "--device", device},
		},
	}
	if inputPath != "" {
		inputInfo, inputErr := os.Stat(inputPath)
		if inputErr != nil || inputInfo.IsDir() {
			cleanupRoot()
			return "", localPackageManifest{}, func() {}, fmt.Errorf("AI input file is not readable: %s", inputPath)
		}
		inputName := "input" + strings.ToLower(filepath.Ext(inputPath))
		if filepath.Ext(inputPath) == "" {
			inputName = "input.json"
		}
		if err := copyLocalFile(inputPath, filepath.Join(root, inputName)); err != nil {
			cleanupRoot()
			return "", localPackageManifest{}, func() {}, err
		}
		manifest.Arguments = append(manifest.Arguments, "--input", inputName)
	}
	if outputPath != "" {
		extension := strings.ToLower(filepath.Ext(outputPath))
		if extension != ".json" && extension != ".npy" && extension != ".npz" {
			cleanupRoot()
			return "", localPackageManifest{}, func() {}, fmt.Errorf("unsupported AI output format %q; use .json, .npy, or .npz", extension)
		}
		manifest.Arguments = append(manifest.Arguments, "--output", "result"+extension)
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		cleanupRoot()
		return "", localPackageManifest{}, func() {}, err
	}
	if err := os.WriteFile(filepath.Join(root, "nodren.json"), manifestBytes, 0o644); err != nil {
		cleanupRoot()
		return "", localPackageManifest{}, func() {}, err
	}
	packagePath, packaged, err := buildTaskPackage(root)
	cleanupRoot()
	if err != nil {
		return "", packaged, func() {}, err
	}
	return packagePath, packaged, func() { _ = os.Remove(packagePath) }, nil
}

func copyLocalFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(output, input)
	return err
}

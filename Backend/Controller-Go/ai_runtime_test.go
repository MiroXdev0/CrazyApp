package main

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLlamaCPPLaunchUsesStructuredModelAndPromptArguments(t *testing.T) {
	spec := AIWorkloadSpec{
		Device:           "gpu",
		Model:            AIModel{Format: "gguf", Runtime: "llama.cpp", Artifacts: []TaskArtifact{{ID: "MODEL", Name: "model.gguf", SHA256: "abc"}}},
		DatasetArtifacts: []TaskArtifact{{ID: "PROMPT", Name: "prompt.txt", Kind: "prompt"}},
		Requirements:     ResourceRequirements{CPUCores: 32, RAMGB: 8, GPURequired: true, GPUCount: 1, VRAMGB: 12},
		ContextSize:      4096,
		GPULayers:        24,
		Temperature:      0.2,
		MaxTokens:        128,
	}
	worker := AIWorkerAssignment{WorkerID: "worker-gpu", GPUIndex: 1, Device: "gpu"}
	launch := buildLlamaCPPLaunch(spec, AIExecutionPlan{ExecutionID: "AI-1", GroupID: "AIG-1"}, worker)
	if launch.Type != TaskTypeProcess || launch.Executable != "__nodren_llama_cpp__" || launch.WorkloadKind != "ai-llama.cpp" {
		t.Fatalf("unexpected llama launch: %#v", launch)
	}
	joined := strings.Join(launch.Arguments, " ")
	for _, expected := range []string{"-m model.gguf", "-f prompt.txt", "-t 32", "-c 4096", "-ngl 24", "--temp 0.2", "-n 128"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("llama launch is missing %q: %s", expected, joined)
		}
	}
	if launch.Environment["NODREN_AI_DEVICE"] != "gpu" || launch.Environment["CUDA_VISIBLE_DEVICES"] != "1" {
		t.Fatalf("GPU environment was not isolated: %#v", launch.Environment)
	}
}

func TestGGUFPlanningSelectsRealLlamaGPUCapability(t *testing.T) {
	controller := NewController("", "")
	node := &NodeRecord{
		Info: NodeInfo{
			ID: "llama-worker", CPUCores: 64, RAMGB: 128,
			GPU:            GPUInfo{Vendor: "NVIDIA", Model: "A10", Count: 1, VRAMGB: 24, Runtime: "CUDA"},
			Runtimes:       []string{"llama.cpp"},
			Capabilities:   []string{"process", "llama.cpp", "llama.cpp-cpu", "llama.cpp-gpu"},
			ExecutionTypes: []string{"PROCESS"},
		},
		State: NodeReady,
	}
	controller.nodes[node.Info.ID] = node
	controller.sessions[node.Info.ID] = newSession(nil)
	controller.artifacts["MODEL"] = &ArtifactRecord{ID: "MODEL", Name: "model.gguf", Size: 1, Kind: "model"}
	plan, err := controller.createAIPlan(AIWorkloadSpec{
		Model: AIModel{
			Format: "gguf", Runtime: "llama.cpp", EstimatedRAMGB: 8, EstimatedVRAMGB: 8,
			Artifacts:  []TaskArtifact{{ID: "MODEL", Name: "model.gguf", Size: 1}},
			Inspection: &AIModelInspection{Format: "gguf", Status: "VALIDATED", ExecutionReady: true},
		},
		CPUAuto: true, Device: "auto",
		Requirements: ResourceRequirements{RAMGB: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Workers) != 1 || plan.Workers[0].Device != "gpu" || plan.Launches[0].Task.WorkloadKind != "ai-llama.cpp" {
		t.Fatalf("GGUF plan did not select the llama GPU path: %#v", plan)
	}
	if plan.Launches[0].Task.Requirements.VRAMGB != 8 || !plan.Launches[0].Task.Requirements.GPURequired {
		t.Fatalf("GGUF GPU reservation was not propagated to the task: %#v", plan.Launches[0].Task.Requirements)
	}
}

func TestGeneratedONNXPackageContainsExecutableRunnerAndInput(t *testing.T) {
	root := t.TempDir()
	model := filepath.Join(root, "model.onnx")
	input := filepath.Join(root, "sample.JSON")
	if err := os.WriteFile(model, []byte("onnx-model-placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, []byte(`{"input":[[1,2]]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	packagePath, manifest, cleanup, err := buildGeneratedONNXPackage(model, input, "", "cpu")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if manifest.EntryPoint != "run_onnx.py" || manifest.Runtime != "python" {
		t.Fatalf("unexpected generated manifest: %+v", manifest)
	}
	archive, err := os.Open(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	reader := tar.NewReader(archive)
	found := map[string]bool{}
	for {
		header, readErr := reader.Next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		found[header.Name] = true
	}
	for _, name := range []string{"nodren.manifest.json", "run_onnx.py", "model.onnx", "input.json"} {
		if !found[name] {
			t.Fatalf("generated package is missing %s: %#v", name, found)
		}
	}
}

func TestONNXRuntimeAdapterRequiresWorkerCapabilities(t *testing.T) {
	adapter := findAIRuntimeAdapter("onnx", "onnxruntime")
	if adapter == nil || adapter.Name() != onnxRuntimeAdapterName {
		t.Fatal("ONNX Runtime adapter was not registered")
	}
	capabilities := adapter.RequiredWorkerCapabilities("gpu")
	if len(capabilities) != 2 || capabilities[0] != "onnxruntime" || capabilities[1] != "onnxruntime-cuda" {
		t.Fatalf("unexpected GPU runtime capabilities: %v", capabilities)
	}
	if adapter := findAIRuntimeAdapter("gguf", ""); adapter == nil || adapter.Name() != "llama.cpp" {
		t.Fatal("GGUF runtime foundation was not registered")
	}
}

package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type aiInspectionRequest struct {
	Path string `json:"path"`
}

func (c *Controller) enrichAIArtifactsLocked(spec *AIWorkloadSpec) error {
	if c == nil || spec == nil {
		return nil
	}
	for _, artifact := range spec.Model.Artifacts {
		stored := c.artifacts[artifact.ID]
		if stored == nil || strings.EqualFold(stored.Kind, "package") {
			continue
		}
		path := c.artifactPath(stored.ID)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		inspection, err := inspectAIModelFileNamed(path, stored.Name)
		if err != nil {
			return fmt.Errorf("AI model artifact %s could not be inspected: %w", artifact.ID, err)
		}
		if inspection.Format == "" {
			continue
		}
		if spec.Model.Format != "" && !strings.EqualFold(spec.Model.Format, inspection.Format) {
			return fmt.Errorf("AI model format metadata %q does not match artifact format %q", spec.Model.Format, inspection.Format)
		}
		spec.Model.Format = inspection.Format
		if spec.Model.Architecture == "" {
			spec.Model.Architecture = inspection.Architecture
		}
		if spec.Model.Quantization == "" {
			spec.Model.Quantization = inspection.Quantization
		}
		if spec.Model.ContextLength == 0 {
			spec.Model.ContextLength = inspection.ContextLength
		}
		if spec.Model.TensorCount == 0 {
			spec.Model.TensorCount = inspection.TensorCount
		}
		if spec.Model.ParameterCount == 0 {
			spec.Model.ParameterCount = inspection.ParameterCount
		}
		if spec.Model.SizeBytes == 0 {
			spec.Model.SizeBytes = inspection.SizeBytes
		}
		if spec.Model.EstimatedRAMGB == 0 {
			spec.Model.EstimatedRAMGB = inspection.EstimatedRAMGB
		}
		if spec.Model.EstimatedVRAMGB == 0 {
			spec.Model.EstimatedVRAMGB = inspection.EstimatedVRAMGB
		}
		inspection.ExecutionReady = strings.TrimSpace(spec.EntryPoint) != "" && strings.TrimSpace(spec.Runtime) != ""
		if strings.EqualFold(inspection.Format, "gguf") && strings.EqualFold(spec.Model.Runtime, "llama.cpp") {
			inspection.ExecutionReady = true
			inspection.InspectionOnly = false
		}
		spec.Model.Inspection = &inspection
		return nil
	}
	return nil
}

func (c *Controller) handleAIInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request aiInspectionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || strings.TrimSpace(request.Path) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "inspection requires a local path"})
		return
	}
	inspection, err := inspectAIPath(request.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, inspection)
}

const maxSafeTensorsHeader = 64 << 20

type safeTensorDescription struct {
	DType      string   `json:"dtype"`
	Shape      []uint64 `json:"shape"`
	DataOffset []uint64 `json:"data_offsets"`
}

func inspectAIPath(path string) (AIModelInspection, error) {
	info, err := os.Stat(path)
	if err != nil {
		return AIModelInspection{}, err
	}
	if !info.IsDir() {
		return inspectAIModelFile(path)
	}

	var candidates []string
	if walkErr := filepath.Walk(path, func(candidate string, entry os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if candidate != path && (entry.Name() == ".git" || entry.Name() == "target" || entry.Name() == "__pycache__") {
				return filepath.SkipDir
			}
			return nil
		}
		if recognizedAIFormat(filepath.Ext(candidate)) != "" {
			candidates = append(candidates, candidate)
		}
		return nil
	}); walkErr != nil {
		return AIModelInspection{}, walkErr
	}
	if len(candidates) == 0 {
		return AIModelInspection{
			InspectionOnly: true,
			Diagnostics: []string{
				"no recognized model file was found; the folder can still be used as a process package if it contains a valid Nodren entrypoint",
			},
		}, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, _ := os.Stat(candidates[i])
		right, _ := os.Stat(candidates[j])
		if left == nil || right == nil {
			return candidates[i] < candidates[j]
		}
		if left.Size() == right.Size() {
			return candidates[i] < candidates[j]
		}
		return left.Size() > right.Size()
	})
	inspection, err := inspectAIModelFile(candidates[0])
	if err != nil {
		return inspection, err
	}
	inspection.Diagnostics = append([]string{
		fmt.Sprintf("selected model file %s from package folder", filepath.Base(candidates[0])),
	}, inspection.Diagnostics...)
	if len(candidates) > 1 {
		var totalSize, totalParameters, totalRAM, totalVRAM uint64
		status := inspection.Status
		matched := 0
		for _, candidate := range candidates {
			part, partErr := inspectAIModelFile(candidate)
			if partErr != nil {
				return AIModelInspection{}, partErr
			}
			if part.Format != inspection.Format {
				continue
			}
			matched++
			totalSize = saturatingAdd(totalSize, part.SizeBytes)
			totalParameters = saturatingAdd(totalParameters, part.ParameterCount)
			totalRAM = saturatingAdd(totalRAM, part.EstimatedRAMGB)
			totalVRAM = saturatingAdd(totalVRAM, part.EstimatedVRAMGB)
			if part.Status == "INVALID" {
				status = "INVALID"
			}
		}
		if matched > 0 {
			inspection.SizeBytes = totalSize
			inspection.ParameterCount = totalParameters
			inspection.EstimatedRAMGB = totalRAM
			inspection.EstimatedVRAMGB = totalVRAM
			inspection.Status = status
			inspection.Diagnostics = append(inspection.Diagnostics, fmt.Sprintf("aggregated metadata from %d same-format model files", matched))
		}
	}
	return inspection, nil
}

func inspectAIModelFile(path string) (AIModelInspection, error) {
	return inspectAIModelFileNamed(path, filepath.Base(path))
}

func inspectAIModelFileNamed(path, name string) (AIModelInspection, error) {
	info, err := os.Stat(path)
	if err != nil {
		return AIModelInspection{}, err
	}
	format := recognizedAIFormat(filepath.Ext(name))
	if format == "" {
		return AIModelInspection{
			SizeBytes:      uint64(maxInt64(info.Size(), 0)),
			InspectionOnly: true,
			Diagnostics: []string{
				"file format is not recognized; no model runtime was selected",
			},
		}, nil
	}
	inspection := AIModelInspection{
		Format:         format,
		Status:         "RECOGNIZED_UNVERIFIED",
		SizeBytes:      uint64(maxInt64(info.Size(), 0)),
		InspectionOnly: true,
		ExecutionReady: false,
		Diagnostics: []string{
			fmt.Sprintf("recognized %s model metadata", format),
			"execution requires a compatible runtime adapter on the selected worker",
		},
	}
	inspection.RuntimeRequirements = runtimeRequirementsForFormat(format)
	if format == "onnx" {
		inspection.SupportedRuntime = "onnxruntime-python"
	}
	if format == "safetensors" {
		if err := inspectSafeTensors(path, &inspection); err != nil {
			inspection.Status = "INVALID"
			inspection.Diagnostics = append(inspection.Diagnostics, "safetensors header could not be inspected: "+err.Error())
		} else {
			inspection.Status = "VALIDATED"
		}
	}
	if format == "gguf" {
		inspection.SupportedRuntime = "llama.cpp"
		if err := inspectGGUF(path, &inspection); err != nil {
			inspection.Status = "INVALID"
			inspection.Diagnostics = append(inspection.Diagnostics, "GGUF metadata could not be inspected: "+err.Error())
		} else {
			inspection.Status = "VALIDATED"
		}
	}
	if inspection.EstimatedRAMGB == 0 {
		inspection.EstimatedRAMGB = conservativeModelMemoryGB(inspection.SizeBytes, 1.25)
	}
	if inspection.EstimatedVRAMGB == 0 {
		inspection.EstimatedVRAMGB = conservativeModelMemoryGB(inspection.SizeBytes, 1.10)
	}
	return inspection, nil
}

func runtimeRequirementsForFormat(format string) []string {
	switch format {
	case "safetensors", "pytorch-checkpoint":
		return []string{"external Python ML runtime (for example PyTorch/Transformers)"}
	case "onnx":
		return []string{"ONNX Runtime Python package (CPU or CUDA provider)"}
	case "gguf", "ggml":
		return []string{"llama.cpp CLI executable (CPU or a build with a compatible GPU backend)"}
	case "binary-model-or-weights":
		return []string{"model-specific external runtime; format is ambiguous"}
	default:
		return nil
	}
}

func recognizedAIFormat(extension string) string {
	switch strings.ToLower(extension) {
	case ".safetensors":
		return "safetensors"
	case ".onnx":
		return "onnx"
	case ".gguf":
		return "gguf"
	case ".ggml":
		return "ggml"
	case ".pt", ".pth", ".ckpt":
		return "pytorch-checkpoint"
	case ".bin":
		return "binary-model-or-weights"
	default:
		return ""
	}
}

func inspectSafeTensors(path string, inspection *AIModelInspection) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	var headerLength uint64
	if err := binary.Read(file, binary.LittleEndian, &headerLength); err != nil {
		return err
	}
	if headerLength == 0 || headerLength > maxSafeTensorsHeader {
		return errors.New("header length is outside the safety limit")
	}
	header := make([]byte, headerLength)
	if _, err := io.ReadFull(file, header); err != nil {
		return err
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(header, &values); err != nil {
		return err
	}
	var weightBytes uint64
	for name, raw := range values {
		if name == "__metadata__" {
			var metadata map[string]string
			if json.Unmarshal(raw, &metadata) == nil {
				for _, key := range []string{"model_type", "architecture", "general.architecture"} {
					if value := strings.TrimSpace(metadata[key]); value != "" && inspection.Architecture == "" {
						inspection.Architecture = value
					}
				}
			}
			continue
		}
		var tensor safeTensorDescription
		if err := json.Unmarshal(raw, &tensor); err != nil {
			continue
		}
		count, ok := product(tensor.Shape)
		if !ok {
			continue
		}
		inspection.ParameterCount = saturatingAdd(inspection.ParameterCount, count)
		bytesPerValue := dtypeBytes(tensor.DType)
		if len(tensor.DataOffset) == 2 && tensor.DataOffset[1] >= tensor.DataOffset[0] {
			weightBytes = saturatingAdd(weightBytes, tensor.DataOffset[1]-tensor.DataOffset[0])
		} else if bytesPerValue > 0 {
			weightBytes = saturatingAdd(weightBytes, saturatingMul(count, bytesPerValue))
		}
	}
	if weightBytes > 0 {
		inspection.EstimatedRAMGB = conservativeModelMemoryGB(weightBytes, 1.25)
		inspection.EstimatedVRAMGB = conservativeModelMemoryGB(weightBytes, 1.10)
	}
	return nil
}

type ggufReader struct {
	file *os.File
}

func (r *ggufReader) u32() (uint32, error) {
	var value uint32
	err := binary.Read(r.file, binary.LittleEndian, &value)
	return value, err
}

func (r *ggufReader) u64() (uint64, error) {
	var value uint64
	err := binary.Read(r.file, binary.LittleEndian, &value)
	return value, err
}

func (r *ggufReader) string() (string, error) {
	length, err := r.u64()
	if err != nil {
		return "", err
	}
	if length > 64<<20 {
		return "", errors.New("GGUF string exceeds safety limit")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r.file, data); err != nil {
		return "", err
	}
	return string(data), nil
}

func (r *ggufReader) value(kind uint32) (any, error) {
	switch kind {
	case 0:
		var value uint8
		err := binary.Read(r.file, binary.LittleEndian, &value)
		return uint64(value), err
	case 1:
		var value int8
		err := binary.Read(r.file, binary.LittleEndian, &value)
		return int64(value), err
	case 2:
		var value uint16
		err := binary.Read(r.file, binary.LittleEndian, &value)
		return uint64(value), err
	case 3:
		var value int16
		err := binary.Read(r.file, binary.LittleEndian, &value)
		return int64(value), err
	case 4:
		value, err := r.u32()
		return uint64(value), err
	case 5:
		value, err := r.u32()
		return int64(int32(value)), err
	case 6:
		var value float32
		err := binary.Read(r.file, binary.LittleEndian, &value)
		return value, err
	case 7:
		var value bool
		err := binary.Read(r.file, binary.LittleEndian, &value)
		return value, err
	case 8:
		return r.string()
	case 9:
		elementKind, err := r.u32()
		if err != nil {
			return nil, err
		}
		count, err := r.u64()
		if err != nil || count > 1<<20 {
			return nil, errors.New("GGUF metadata array exceeds safety limit")
		}
		for index := uint64(0); index < count; index++ {
			if _, err := r.value(elementKind); err != nil {
				return nil, err
			}
		}
		return nil, nil
	case 10:
		return r.u64()
	case 11:
		value, err := r.u64()
		return int64(value), err
	case 12:
		var value float64
		err := binary.Read(r.file, binary.LittleEndian, &value)
		return value, err
	default:
		return nil, fmt.Errorf("unsupported GGUF metadata type %d", kind)
	}
}

func inspectGGUF(path string, inspection *AIModelInspection) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	magic := make([]byte, 4)
	if _, err := io.ReadFull(file, magic); err != nil || string(magic) != "GGUF" {
		return errors.New("invalid GGUF magic")
	}
	r := ggufReader{file: file}
	version, err := r.u32()
	if err != nil || version == 0 || version > 3 {
		return fmt.Errorf("unsupported GGUF version %d", version)
	}
	tensorCount, err := r.u64()
	if err != nil || tensorCount > 10_000_000 {
		return errors.New("invalid GGUF tensor count")
	}
	metadataCount, err := r.u64()
	if err != nil || metadataCount > 1_000_000 {
		return errors.New("invalid GGUF metadata count")
	}
	inspection.TensorCount = tensorCount
	for index := uint64(0); index < metadataCount; index++ {
		key, err := r.string()
		if err != nil {
			return err
		}
		kind, err := r.u32()
		if err != nil {
			return err
		}
		value, err := r.value(kind)
		if err != nil {
			return err
		}
		if text, ok := value.(string); ok && (key == "general.architecture" || strings.HasSuffix(key, ".architecture")) {
			inspection.Architecture = text
		}
		if number, ok := value.(uint64); ok && (strings.HasSuffix(key, ".context_length") || key == "llama.context_length") {
			inspection.ContextLength = number
		}
	}
	quantizations := map[string]uint64{}
	var estimatedBytes uint64
	for index := uint64(0); index < tensorCount; index++ {
		if _, err := r.string(); err != nil {
			return err
		}
		dimensions, err := r.u32()
		if err != nil || dimensions > 64 {
			return errors.New("invalid GGUF tensor dimensions")
		}
		shape := make([]uint64, dimensions)
		for dimension := range shape {
			shape[dimension], err = r.u64()
			if err != nil {
				return err
			}
		}
		tensorType, err := r.u32()
		if err != nil {
			return err
		}
		if _, err := r.u64(); err != nil {
			return err
		}
		count, ok := product(shape)
		if !ok {
			continue
		}
		inspection.ParameterCount = saturatingAdd(inspection.ParameterCount, count)
		name, block, bytes := ggufTensorType(tensorType)
		if name != "" {
			quantizations[name]++
		}
		if bytes > 0 {
			blocks := (count + block - 1) / block
			estimatedBytes = saturatingAdd(estimatedBytes, saturatingMul(blocks, bytes))
		}
	}
	var mostCommon string
	var mostCommonCount uint64
	for name, count := range quantizations {
		if count > mostCommonCount {
			mostCommon, mostCommonCount = name, count
		}
	}
	inspection.Quantization = mostCommon
	if estimatedBytes > 0 {
		inspection.EstimatedRAMGB = conservativeModelMemoryGB(estimatedBytes, 1.20)
		inspection.EstimatedVRAMGB = conservativeModelMemoryGB(estimatedBytes, 1.10)
	}
	return nil
}

func ggufTensorType(kind uint32) (string, uint64, uint64) {
	switch kind {
	case 0:
		return "F32", 1, 4
	case 1:
		return "F16", 1, 2
	case 2:
		return "Q4_0", 32, 18
	case 3:
		return "Q4_1", 32, 20
	case 6:
		return "Q5_0", 32, 22
	case 7:
		return "Q5_1", 32, 24
	case 8:
		return "Q8_0", 32, 34
	case 10:
		return "Q2_K", 256, 84
	case 11:
		return "Q3_K", 256, 110
	case 12:
		return "Q4_K", 256, 144
	case 13:
		return "Q5_K", 256, 176
	case 14:
		return "Q6_K", 256, 210
	default:
		return "", 0, 0
	}
}

func product(values []uint64) (uint64, bool) {
	result := uint64(1)
	for _, value := range values {
		if value == 0 || result > math.MaxUint64/value {
			return 0, false
		}
		result *= value
	}
	return result, true
}

func dtypeBytes(dtype string) uint64 {
	switch strings.ToUpper(dtype) {
	case "F64", "I64", "U64":
		return 8
	case "F32", "I32", "U32":
		return 4
	case "F16", "BF16", "I16", "U16":
		return 2
	case "F8_E4M3", "F8_E5M2", "I8", "U8", "BOOL":
		return 1
	default:
		return 0
	}
}

func conservativeModelMemoryGB(size uint64, multiplier float64) uint64 {
	if size == 0 {
		return 0
	}
	bytes := float64(size) * multiplier
	return uint64(math.Max(1, math.Ceil(bytes/(1024*1024*1024))))
}

func saturatingAdd(left, right uint64) uint64 {
	if math.MaxUint64-left < right {
		return math.MaxUint64
	}
	return left + right
}

func saturatingMul(left, right uint64) uint64 {
	if left != 0 && right > math.MaxUint64/left {
		return math.MaxUint64
	}
	return left * right
}

func maxInt64(value, minimum int64) int64 {
	if value < minimum {
		return minimum
	}
	return value
}

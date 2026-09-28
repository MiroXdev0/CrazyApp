package main

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	minimumDistributedUnits uint64 = 64
	maximumPartitionUnits   uint64 = 1 << 20
)

type workloadPartition struct {
	Payload []byte
	Units   uint64
}

// workloadDefinition keeps partitioning and reduction rules with the
// workload. The scheduler only deals with opaque partition payloads and
// numeric partial results.
type workloadDefinition interface {
	unitCount(payload []byte) (uint64, error)
	partition(payload []byte, unitsPerPartition uint64) ([]workloadPartition, error)
	merge(values []int64) (int64, error)
}

type byteReductionWorkload struct {
	xor bool
}

func (w byteReductionWorkload) unitCount(payload []byte) (uint64, error) {
	if len(payload) == 0 {
		return 0, errors.New("payload must contain at least one byte")
	}
	return uint64(len(payload)), nil
}

func (w byteReductionWorkload) partition(payload []byte, unitsPerPartition uint64) ([]workloadPartition, error) {
	units, err := w.unitCount(payload)
	if err != nil {
		return nil, err
	}
	if unitsPerPartition == 0 {
		return nil, errors.New("partition size must be greater than zero")
	}
	partitions := make([]workloadPartition, 0, (units+unitsPerPartition-1)/unitsPerPartition)
	for start := uint64(0); start < units; start += unitsPerPartition {
		end := start + unitsPerPartition
		if end > units {
			end = units
		}
		partitions = append(partitions, workloadPartition{
			Payload: append([]byte(nil), payload[start:end]...),
			Units:   end - start,
		})
	}
	return partitions, nil
}

func (w byteReductionWorkload) merge(values []int64) (int64, error) {
	if len(values) == 0 {
		return 0, errors.New("cannot reduce an empty result set")
	}
	var result int64
	if w.xor {
		for _, value := range values {
			result ^= value
		}
		return result, nil
	}
	for _, value := range values {
		result += value
	}
	return result, nil
}

type dotProductWorkload struct{}

func (dotProductWorkload) decode(payload []byte) (uint32, []int32, []int32, error) {
	if len(payload) < 4 {
		return 0, nil, nil, errors.New("dot_product payload is missing its element count")
	}
	count := binary.LittleEndian.Uint32(payload[:4])
	if count == 0 {
		return 0, nil, nil, errors.New("dot_product requires at least one element")
	}
	if uint64(count) > uint64((len(payload)-4)/8) || 4+int(count)*8 != len(payload) {
		return 0, nil, nil, errors.New("dot_product payload length does not match its element count")
	}
	left := make([]int32, count)
	right := make([]int32, count)
	rightOffset := 4 + int(count)*4
	for index := range left {
		left[index] = int32(binary.LittleEndian.Uint32(payload[4+index*4 : 8+index*4]))
		right[index] = int32(binary.LittleEndian.Uint32(payload[rightOffset+index*4 : rightOffset+index*4+4]))
	}
	return count, left, right, nil
}

func (d dotProductWorkload) unitCount(payload []byte) (uint64, error) {
	count, _, _, err := d.decode(payload)
	return uint64(count), err
}

func (d dotProductWorkload) partition(payload []byte, unitsPerPartition uint64) ([]workloadPartition, error) {
	count, left, right, err := d.decode(payload)
	if err != nil {
		return nil, err
	}
	if unitsPerPartition == 0 {
		return nil, errors.New("partition size must be greater than zero")
	}
	partitions := make([]workloadPartition, 0, (uint64(count)+unitsPerPartition-1)/unitsPerPartition)
	for start := uint64(0); start < uint64(count); start += unitsPerPartition {
		end := start + unitsPerPartition
		if end > uint64(count) {
			end = uint64(count)
		}
		partitionCount := int(end - start)
		encoded := make([]byte, 4+partitionCount*8)
		binary.LittleEndian.PutUint32(encoded[:4], uint32(partitionCount))
		for offset := 0; offset < partitionCount; offset++ {
			binary.LittleEndian.PutUint32(encoded[4+offset*4:8+offset*4], uint32(left[int(start)+offset]))
			rightOffset := 4 + partitionCount*4 + offset*4
			binary.LittleEndian.PutUint32(encoded[rightOffset:rightOffset+4], uint32(right[int(start)+offset]))
		}
		partitions = append(partitions, workloadPartition{Payload: encoded, Units: end - start})
	}
	return partitions, nil
}

func (dotProductWorkload) merge(values []int64) (int64, error) {
	if len(values) == 0 {
		return 0, errors.New("cannot reduce an empty result set")
	}
	var result int64
	for _, value := range values {
		result += value
	}
	return result, nil
}

func workloadDefinitionFor(command string) (workloadDefinition, bool) {
	switch command {
	case "sum":
		return byteReductionWorkload{}, true
	case "xor":
		return byteReductionWorkload{xor: true}, true
	case "dot_product":
		return dotProductWorkload{}, true
	default:
		return nil, false
	}
}

func partitionSizeFor(totalUnits uint64, eligibleWorkers int) uint64 {
	if totalUnits == 0 {
		return 1
	}
	if totalUnits <= minimumDistributedUnits {
		return totalUnits
	}
	if eligibleWorkers <= 1 {
		return maximumPartitionUnits
	}
	targetPartitions := uint64(eligibleWorkers * 4)
	if targetPartitions == 0 {
		targetPartitions = 1
	}
	unitsPerPartition := (totalUnits + targetPartitions - 1) / targetPartitions
	if unitsPerPartition < minimumDistributedUnits {
		unitsPerPartition = minimumDistributedUnits
	}
	if unitsPerPartition > maximumPartitionUnits {
		unitsPerPartition = maximumPartitionUnits
	}
	return unitsPerPartition
}

func unsupportedWorkloadDefinition(command string) error {
	return fmt.Errorf("unsupported workload: %s", command)
}

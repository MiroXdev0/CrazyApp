package main

import (
	"fmt"
	"testing"
)

func BenchmarkChoosePartitionNode(b *testing.B) {
	controller := NewController("", "")
	for index := 0; index < 32; index++ {
		id := fmt.Sprintf("worker-%02d", index)
		node := readyNode(id, uint32(2+(index%8)), uint64(4+(index%16)))
		node.PerformanceFactor = 0.75 + float64(index%5)*0.15
		node.Telemetry.CPUUtilizationPercent = float64(index % 60)
		updateNodeCapacity(node)
		controller.nodes[id] = node
		controller.sessions[id] = newSession(nil)
	}
	job := &Job{ID: "benchmark", Requirements: ResourceRequirements{CPUCores: 1, RAMGB: 1}, Distribution: DistributionInfo{Mode: DistributionAutomatic, TotalPartitions: 256}}
	for index := 0; index < 256; index++ {
		job.Partitions = append(job.Partitions, Partition{ID: fmt.Sprintf("p-%03d", index), Index: index, Units: uint64(index + 1), State: PartitionQueued})
	}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = controller.choosePartitionNodeLocked(job)
	}
}

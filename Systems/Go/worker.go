package main

import "fmt"

type WorkerStatus struct {
	ID       string
	Health   string
	CPUCores int
	RAMGB    int
	GPU      string
}

func ReportStatus(status WorkerStatus) {
	fmt.Printf("[worker] %s healthy: %s | cores=%d | ram=%dGB\n", status.ID, status.Health, status.CPUCores, status.RAMGB)
}

func main() {
	status := WorkerStatus{
		ID:       "worker-02",
		Health:   "online",
		CPUCores: 8,
		RAMGB:    32,
		GPU:      "NVIDIA",
	}

	ReportStatus(status)
}

package main

import "fmt"

type ResourceRequirements struct {
	CPUCores int
	RAMGB    int
	GPU      bool
}

type NodeInfo struct {
	ID       string
	Host     string
	OS       string
	Arch     string
	CPUCores int
	RAMGB    int
	GPU      string
}

type JobRequest struct {
	ID          string
	Command     string
	Priority    int
	Requirements ResourceRequirements
}

func registerNode(node NodeInfo) {
	fmt.Printf("[controller] node registered: %s (%s %s)\n", node.ID, node.OS, node.Arch)
}

func dispatchJob(job JobRequest) {
	fmt.Printf("[controller] dispatching job %s -> %s\n", job.ID, job.Command)
}

func main() {
	worker := NodeInfo{
		ID:       "node-01",
		Host:     "desktop-01",
		OS:       "Windows",
		Arch:     "x64",
		CPUCores: 12,
		RAMGB:    16,
		GPU:      "RTX",
	}

	job := JobRequest{
		ID:      "JOB-1847",
		Command: "simulation.exe",
		Priority: 5,
		Requirements: ResourceRequirements{
			CPUCores: 4,
			RAMGB:    8,
			GPU:      false,
		},
	}

	registerNode(worker)
	dispatchJob(job)
	fmt.Println("[controller] NEXUS control plane online")
}

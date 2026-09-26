package main

import "fmt"

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
	Requirements struct {
		CPUCores int
		RAMGB    int
		GPU      bool
	}
}

func RegisterNode(node NodeInfo) {
	fmt.Printf("[controller] registered node %s (%s %s)\n", node.ID, node.OS, node.Arch)
}

func DispatchJob(job JobRequest) {
	fmt.Printf("[controller] dispatching job %s -> %s\n", job.ID, job.Command)
}

func main() {
	node := NodeInfo{
		ID:       "node-01",
		Host:     "desktop-01",
		OS:       "Windows",
		Arch:     "x64",
		CPUCores: 12,
		RAMGB:    16,
		GPU:      "RTX",
	}

	job := JobRequest{ID: "JOB-1847", Command: "simulation.exe", Priority: 5}
	job.Requirements.CPUCores = 4
	job.Requirements.RAMGB = 8
	job.Requirements.GPU = false

	RegisterNode(node)
	DispatchJob(job)
}

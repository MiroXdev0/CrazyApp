package main

import (
	"fmt"
	"sync"
	"time"
)

type NodeState string

const (
	NodeStateReady   NodeState = "READY"
	NodeStateBusy    NodeState = "BUSY"
	NodeStateLost    NodeState = "LOST"
	NodeStateOffline NodeState = "OFFLINE"
)

type RegistryNode struct {
	ID            string
	Hostname      string
	CPUThreads    int
	RAMGB         int
	OS            string
	Architecture  string
	Status        NodeState
	LastHeartbeat time.Time
	ConnectedAt   time.Time
}

type NodeRegistry struct {
	mu    sync.RWMutex
	nodes map[string]*RegistryNode
}

func NewNodeRegistry() *NodeRegistry {
	return &NodeRegistry{nodes: make(map[string]*RegistryNode)}
}

func (r *NodeRegistry) Add(node *RegistryNode) {
	r.mu.Lock()
	defer r.mu.Unlock()
	node.Status = NodeStateReady
	node.LastHeartbeat = time.Now()
	node.ConnectedAt = time.Now()
	r.nodes[node.ID] = node
}

func (r *NodeRegistry) UpdateHeartbeat(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if node, ok := r.nodes[nodeID]; ok {
		node.LastHeartbeat = time.Now()
		node.Status = NodeStateReady
	}
}

func (r *NodeRegistry) MarkLost(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if node, ok := r.nodes[nodeID]; ok {
		node.Status = NodeStateLost
	}
}

func (r *NodeRegistry) Remove(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.nodes, nodeID)
}

func (r *NodeRegistry) Snapshot() []RegistryNode {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]RegistryNode, 0, len(r.nodes))
	for _, node := range r.nodes {
		clone := *node
		out = append(out, clone)
	}
	return out
}

func (r *NodeRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.nodes)
}

func (r *NodeRegistry) PrintSummary() {
	for _, node := range r.Snapshot() {
		fmt.Printf("[%s] %s | %s | %s | %d threads | %d GB | %s\n",
			node.ID,
			node.Hostname,
			node.OS,
			node.Architecture,
			node.CPUThreads,
			node.RAMGB,
			node.Status,
		)
	}
}

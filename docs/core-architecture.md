# NEXUS Core Architecture

The core is the foundation of the ecosystem. It defines how nodes register, how jobs are scheduled, how resources are assigned, and how results are reconstructed.

## 1. Core principles

- Stable protocol first
- Resource-aware scheduling
- Fault tolerant execution
- Heterogeneous hardware support
- Separate control plane from execution plane
- Shared contracts across languages

## 2. Control plane

The control plane is responsible for orchestration.

Components:
- node registry
- scheduler
- job queue
- health monitor
- result aggregator
- config manager

Responsibilities:
- register workers
- accept jobs
- determine target workers
- track worker health
- retry failed jobs
- reconstruct outputs

## 3. Execution plane

The execution plane runs actual work on worker nodes.

Components:
- worker runtime
- process launcher
- resource limiter
- sandbox boundary
- output collector

Responsibilities:
- run commands
- collect stdout/stderr
- report resource usage
- return outputs
- notify controller on status changes

## 4. Core runtime model

```text
Controller
  ├── Node Registry
  ├── Scheduler
  ├── Job Queue
  ├── Result Aggregator
  └── Health Monitor

Worker
  ├── Node Identity
  ├── Resource Reporter
  ├── Job Executor
  ├── Output Collector
  └── Heartbeat Sender
```

## 5. Job model

A job should be represented as a rich object:

```json
{
  "id": "JOB-1847",
  "name": "simulation",
  "priority": 5,
  "status": "queued",
  "target": {
    "cpu_cores": 4,
    "ram_gb": 8,
    "gpu_required": false
  },
  "command": "simulation.exe",
  "input": "./input/data.bin",
  "timeout_ms": 60000
}
```

## 6. Resource model

Each worker reports:
- CPU cores
- RAM
- GPU info
- architecture
- OS
- network address
- availability

The scheduler evaluates:
- worker score
- queue length
- current load
- architecture compatibility
- priority policy

## 7. Scheduling policy

At minimum, support:
- round robin
- highest-capacity-first
- priority-first
- affinity-first

Future policies:
- predictive scheduling
- cost-based placement
- energy-aware scheduling
- AI-assisted routing

## 8. Fault model

The core should explicitly handle:
- worker crash
- network partition
- resource exhaustion
- timeout
- partial result loss
- corrupted output

Recovery behaviors:
- retry
- requeue
- failover to another worker
- checkpoint restore

## 9. Protocol design

The protocol should be small and explicit:
- register_node
- heartbeat
- job_submit
- job_accept
- job_start
- job_progress
- job_complete
- job_failed
- result_upload

## 10. Data flow

```text
client -> controller
controller -> scheduler
scheduler -> worker
worker -> controller
controller -> result storage
```

## 11. Ecosystem dependency

The core is the root platform. Everything else depends on it:
- web dashboard
- desktop tools
- analytics package
- AI layer
- SDK and developer tooling
- deployment automation

The ecosystem grows from the core, not the other way around.

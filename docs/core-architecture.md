# Nodren Core Architecture

The core is the foundation of the ecosystem. It defines how nodes register, how jobs are scheduled, how resources are assigned, and how results are reconstructed.

## 1. Core principles

- Stable protocol first
- Resource-aware scheduling
- Fault tolerant execution
- Heterogeneous hardware support
- Separate control plane from execution plane
- Explicit contracts across languages

## 2. Control plane

The control plane is responsible for orchestration.

Components:
- node registry
- scheduler
- job queue
- health monitor
- result aggregator
- environment/argument configuration

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
- bounded task executor
- resource gate
- native Core dispatcher
- result transmitter

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
  "command": "sum",
  "priority": 50,
  "status": "QUEUED",
  "requirements": {
    "cpu_cores": 1,
    "ram_gb": 1,
    "gpu_required": false
  },
  "payload_base64": "AQIDBAU="
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

The current scheduler uses priority order and deterministic best-fit placement
by remaining CPU/RAM slack, with worker ID as the final tie-breaker.

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
- `REGISTER`
- `READY`
- `TASK_BATCH`
- `TASK_RESULT_BATCH`
- `HEARTBEAT` and `HEARTBEAT_ACK`
- `ERROR`
- `GOODBYE`

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
- optional web/desktop tools
- analytics and AI experiments
- future SDK and deployment automation

These are not required by the pre-release Controller/Worker runtime.

The ecosystem grows from the core, not the other way around.

# Nodren Architecture

This document defines the runtime model for the distributed compute platform.

## Mission

Nodren turns multiple ordinary computers into one virtual computational machine. The system coordinates CPU, memory, GPU, networking, and scheduling across heterogeneous hardware.

## Core components

- Controller: owns the node registry, job placement, health tracking, and result aggregation.
- Worker: reports local resources, executes jobs, and sends status and output back to the controller.
- Scheduler: chooses the best-fit worker for each job based on resources, priority, and availability.
- Binary protocol: `Backend/Protocol` documents the wire contract implemented by the Go Controller and Rust Worker.
- Native runtime: provides low-level memory primitives, runtime helpers, and system integration utilities.
- HTTP API and CLI: expose health, nodes, jobs, and workload submission.

## Execution flow

1. Workers register with the controller and publish hardware information.
2. The controller stores node metadata, health, and workload capacity.
3. A user submits a workload with resource requirements and priority.
4. The scheduler selects the best available workers.
5. Work is dispatched to a worker over the persistent binary session.
6. Results are collected and reconstructed.
7. Failed or overloaded nodes are retried or replaced.

## Future milestones

### Completed foundation
- 1 controller
- multiple workers
- basic registration
- resource-aware job dispatch
- concurrent worker execution
- result collection and reconnect/requeue behavior

### Current pre-release
- priority-aware scheduler
- health checks and reconnect
- standalone worker release
- explicit LAN/TCP addressing
- two-executable runtime packaging

### Future, not part of the pre-release
- shared memory model
- chunked data transfer
- partitioned storage
- snapshot and checkpointing

### Phase 4: distributed compute platform
- multi-architecture job placement
- GPU-aware scheduling
- AI-assisted workload optimization
- remote management from web dashboard

### Phase 5: developer ecosystem
- SDK
- CLI tools
- scripting environment
- plugin system
- deployment automation

## Planned modules

- Controller and scheduler
- standalone Worker
- `nodren` CLI commands
- future storage, dashboards, SDKs, and deployment automation

## Why this system exists

The project is intentionally ambitious because it combines several hard engineering challenges in one system:
- heterogeneous hardware scheduling
- distributed execution models
- resource isolation
- transport and serialization
- fault tolerance
- performance tuning
- observability

This is not a CRUD app or a website. It is a compute fabric for real workloads.

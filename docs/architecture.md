# Nodren Architecture

This document defines the runtime model for the distributed compute platform.

## Mission

Nodren turns multiple ordinary computers into one virtual computational machine. The system coordinates CPU, memory, GPU, networking, and scheduling across heterogeneous hardware.

## Core components

- Controller: owns the node registry, job placement, health tracking, and result aggregation.
- Worker: reports local resources, executes jobs, and sends status and output back to the controller.
- Scheduler: chooses the best-fit worker for each job based on resources, priority, and availability.
- Shared protocol: defines the wire contract for registration, job dispatch, heartbeats, and result reporting.
- Native runtime: provides low-level memory primitives, runtime helpers, and system integration utilities.
- Data plane: moves input, output, and intermediate data efficiently between nodes.
- Web dashboard: exposes cluster health, node metadata, job status, and deployment telemetry.
- Developer SDK: gives users a clean way to submit jobs, inspect clusters, and build custom workflows.

## Execution flow

1. Workers register with the controller and publish hardware information.
2. The controller stores node metadata, health, and workload capacity.
3. A user submits a workload with resource requirements and priority.
4. The scheduler selects the best available workers.
5. Work is partitioned and dispatched.
6. Results are collected and reconstructed.
7. Failed or overloaded nodes are retried or replaced.

## Future milestones

### Phase 1: minimal cluster
- 1 controller
- 2-3 workers
- basic registration
- basic job dispatch
- basic result collection

### Phase 2: scheduling and safety
- priority-aware scheduler
- task retries
- health checks
- automatic failover
- sandboxed execution boundaries

### Phase 3: distributed memory and storage
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

- nexus controller
- nexus worker
- nexus run
- nexus status
- nexus deploy
- nexus storage
- nexus scheduler
- nexus monitor
- nexus sdk

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

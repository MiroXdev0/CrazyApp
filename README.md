# NEXUS

NEXUS is a distributed compute platform that treats multiple ordinary PCs as one shared computational machine.

The project is designed around a real distributed runtime architecture:
- a controller for orchestration and scheduling
- workers for execution and resource reporting
- shared protocols for job and IPC contracts
- native low-level layers for memory and system runtime behavior
- web and desktop interfaces for management

## Core idea
A user submits a workload such as:

```bash
nexus run simulation.exe
```

The system decides:
- which node should run the task
- how to split the workload
- what CPU, RAM, and GPU resources are required
- where data should be staged
- how results are collected and reconstructed
- how failures are recovered

## Architecture

```text
NEXUS
├── Frontend/
│   ├── Desktop/      # C# + Avalonia UI
│   └── Web/          # TS + HTML + CSS dashboard
├── Backend/
│   ├── Server/       # API and orchestration services
│   ├── Data/         # analytics and data services
│   └── API/          # public service endpoints
├── Core/
│   ├── C/            # low-level runtime and memory helpers
│   ├── Cpp/          # compute engine and native execution logic
│   └── Assembly/     # architecture-specific primitives
├── Systems/
│   ├── Go/           # distributed control plane
│   └── Rust/         # worker and networking runtime
├── Shared/
│   ├── Protocol/     # job and node message contract
│   ├── IPC/          # local process communication
│   ├── Serialization/# serialization formats
│   └── C_API/        # C bridge interfaces
├── Database/
│   ├── SQL/          # schemas and seed data
│   ├── migrations/
│   └── schemas/
├── Python/
│   ├── Analytics/
│   ├── AI/
│   ├── Tools/
│   └── Scripts/
├── Native/
│   ├── include/
│   ├── lib/
│   └── bindings/
├── Tests/
│   ├── Cpp/
│   ├── Rust/
│   ├── CSharp/
│   ├── Python/
│   └── Integration/
├── docs/
├── tools/
├── scripts/
├── .gitignore
├── .gitattributes
├── README.md
└── LICENSE
```

## Version 1 target
The first version is intentionally small and focused:
- one controller
- several worker nodes
- basic job submission
- simple resource registration
- result collection

## Example flow

```text
Controller registers workers
Worker reports: CPU, RAM, OS, architecture
Controller submits a job
Worker executes the task
Worker returns output and status
Controller stores results and handles retries
```

## Languages
- C# for tooling and desktop management
- TypeScript for the web dashboard
- Go for orchestration services
- Rust for worker safety and networking
- C/C++ for low-level compute and runtime behavior
- Python for automation and analysis
- SQL for job, node, and telemetry state

## Current status
This repo is in the base-architecture phase. The goal is to establish the distributed-system skeleton before expanding into scheduling, runtime isolation, and advanced workload management.

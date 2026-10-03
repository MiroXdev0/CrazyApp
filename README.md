# Nodren

**Distributed compute infrastructure for turning multiple machines into one execution platform.**

Nodren connects computers into a distributed execution cluster and schedules workloads across available resources.

## ✨ Features

* Distributed workload execution
* CPU & GPU resource-aware scheduling
* Real ONNX and GGUF/llama.cpp AI execution
* Live worker and cluster telemetry
* Automatic worker recovery
* Native C/C++ execution core
* CLI and desktop applications
* Artifact transfer and integrity verification

## 🏗️ Architecture

```text
CLI / Desktop / Web
        │
        ▼
 Go Controller
        │
   TCP Protocol
        │
   ┌────┼────┐
   ▼    ▼    ▼
Worker Worker Worker
   │    │    │
   └────┼────┘
        ▼
 Native Core
```

## 🚀 Quick Start

### 1. Start the Controller

```bash
nodren
```

### 2. Start a Worker

```bash
nodren-worker
```

### 3. Check the cluster

```bash
nodren status
nodren devices
```

## 🤖 AI

Nodren supports real AI execution through:

* ONNX Runtime
* GGUF / llama.cpp
* CPU and GPU execution
* Resource-aware scheduling
* Model inspection
* Streaming output

## 📦 Release

**Latest:** `V1.0.1-Windows(x86_64)`

Windows x86_64 is currently the primary supported platform. Linux support is under development.

## 🛠️ Built With

**Go · Rust · C · C++ · Assembly · TypeScript · C#**

## 📖 Documentation

See the repository documentation for architecture, development, configuration, and advanced usage.

## 📄 License

See [LICENSE](LICENSE).

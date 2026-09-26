# CrazyApp Rust runtime

This is the safe systems boundary for the CrazyApp core.

Responsibilities:
- safe runtime initialization and lifecycle
- task queue tracking
- serializable task envelope handling
- C-compatible runtime hooks used by the native subsystem

The C++ execution engine remains responsible for high-performance execution. Rust owns the safety and concurrency boundary around the runtime state.

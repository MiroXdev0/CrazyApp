# CrazyApp Core

This directory contains the first native core milestone for CrazyApp.

The implementation is intentionally limited to the first milestone described in the architecture:

- C memory layer for allocation and arena management
- C++ execution engine for task scheduling and execution
- x86-64 assembly primitive for a hot-path numeric reduction
- safe startup/shutdown lifecycle for a local node runtime

The Core is intentionally independent from the UI, the web layer, and the higher-level controller.

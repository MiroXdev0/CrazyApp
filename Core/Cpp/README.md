# Nodren Core — C++

The C++ layer owns:
- native task execution
- bounded lock-free task transport
- worker threads
- CPU feature dispatch
- C ABI integration

The hot path uses POD task descriptors and avoids `std::function`, JSON,
or per-task heap allocation.

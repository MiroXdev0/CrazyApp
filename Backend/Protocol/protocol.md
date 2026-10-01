# Nodren Backend Protocol v1

Transport: persistent TCP connection.

Frame layout, little-endian:

| Offset | Size | Field |
|---:|---:|---|
| 0 | 4 | Magic `NDRN` |
| 4 | 2 | Protocol version |
| 6 | 2 | Message type |
| 8 | 8 | Request ID |
| 16 | 4 | Payload size |
| 20 | N | Payload |

Maximum frame payload: 16 MiB.

## Messages

1. `HELLO`
2. `REGISTER`
3. `REGISTER_ACK`
4. `HEARTBEAT`
5. `HEARTBEAT_ACK`
6. `TASK_BATCH`
7. `TASK_RESULT_BATCH`
8. `ERROR`
9. `GOODBYE`
10. `READY`
11. `TASK_SUBMIT`
12. `TASK_ACK`
13. `TASK_CANCEL`
14. `TASK_STATE`
15. `TASK_RESULT`
16. `ARTIFACT_BEGIN`
17. `ARTIFACT_CHUNK`
18. `ARTIFACT_END`
19. `CAPABILITIES`

The Go controller owns scheduling and job placement. The Rust worker owns local resource validation and execution management.

HTTP/JSON is only the management API. The node data plane uses the binary protocol.

`TASK_SUBMIT` carries a typed `GeneralTaskEnvelope`, not JSON-over-TCP. It
contains the task type (`PROCESS`, `SCRIPT`, `COMMAND`, or
`NATIVE_WORKLOAD`), structured arguments and environment, resource/target
constraints, timeout/output limits, retry policy, and artifact metadata.
`TASK_RESULT` carries status, an optional exit code, bounded stdout/stderr,
truncation flags, duration, and an error category.

Input artifacts are transferred before `TASK_SUBMIT` with
`ARTIFACT_BEGIN`/`ARTIFACT_CHUNK`/`ARTIFACT_END`. Chunks are bounded and the
controller stores artifact bytes separately from the main state snapshot;
metadata includes size and SHA-256. The worker stages input files in a
task-local directory.

## Resources and scheduling

`REGISTER` advertises CPU cores, RAM in GiB, architecture, operating system,
and optional GPU vendor/model/VRAM. `TASK_BATCH` carries the job's CPU, RAM,
and GPU requirements unchanged to the worker, where they are validated again.

The controller assigns new work only to connected `READY` workers or `BUSY`
workers that still have capacity. `LOST` and `OFFLINE` workers are excluded.
Eligible workers are selected using deterministic best-fit ordering: lowest remaining CPU slack, then lowest
remaining RAM slack, then lexicographically lowest worker ID. A worker can
hold multiple assignments while the sum of their requirements fits its
advertised capacity. `BUSY` means no remaining schedulable capacity, not that
one task is running.

The Rust worker uses a bounded task queue and executor threads derived from
its advertised CPU capacity. Each task acquires its CPU/RAM share before
native execution and releases it after execution. Results are sent
asynchronously so the connection reader can continue accepting work. A
connection loss still requeues controller assignments and stale results remain
ignored; execution is not exactly-once.

Jobs that exceed every known worker's advertised resources fail with
`resource_requirements`. Jobs remain queued when no worker is registered yet
or when a suitable worker is temporarily unavailable. The HTTP node and job
APIs expose resources, state, assigned jobs, and the selected worker.

## Partitioned workloads

The existing `TASK_BATCH` and `TASK_RESULT_BATCH` messages are reused for
partition execution; no new transport message is required. A Controller job
may expose a `distribution` object and `partitions` through HTTP. Each
partition is assigned a unique task ID and carries an opaque workload payload
through the existing `Task` fields. The Controller tracks partition state,
attempt, worker, unit count, and partial result, then reduces completed
partial values into the final job result.

Jobs use automatic distribution by default. For the current workloads, inputs
of 64 units or fewer remain one task. Larger inputs are split into bounded
chunks and assigned according to effective capacity. The documented estimate
is `CPU cores + RAM GiB / 4`, multiplied by the smaller available CPU/RAM
fraction. This is a scheduling estimate, not a hardware benchmark. GPU
metadata remains a requirement filter because native GPU execution is not
implemented.

Optional manual mode is requested in HTTP with
`distribution_mode: "manual"` and a `manual_allocations` map whose values
must total 100. Manual mode still respects worker state and resource limits.
On worker loss, incomplete partitions are requeued with a new attempt while
completed partitions remain terminal. Stale task results continue to be
ignored by task ID and ownership checks.

## Workloads and results

`TASK_BATCH` carries an opaque payload together with a workload command. The
controller does not interpret workload payloads. The current native dispatcher
supports:

- `sum`: payload bytes are treated as unsigned integer values and reduced.
- `xor`: payload bytes are treated as unsigned integer values and XORed.
- `dot_product`: payload is little-endian `u32 count`, followed by `count`
  little-endian `i32` values for the left vector and `count` values for the
  right vector.

`TASK_RESULT_BATCH` preserves the numeric result and includes an optional
machine-readable `error_code` after the existing result fields. Current
failure categories include `malformed_payload`, `unsupported_workload`,
`resource_requirements`, `execution_failed`, and `internal_error`.

## General execution and trust model

The Rust worker owns OS process execution. `PROCESS` and `COMMAND` use
structured arguments without shell concatenation. `SCRIPT` selects an
explicitly named installed runtime (Python, Node, shell, or PowerShell) and
validates its advertised availability before execution. Cancellation kills the
child process; timeout kills it after the task deadline. Stdout and stderr are
captured with bounded per-task buffers.

Nodren currently assumes a trusted cluster: a registered worker may execute a
requested executable with its OS privileges. Protocol validation, task-type
allowlisting, path/name checks, resource checks, timeouts, and cancellation
are implemented, but this is not a sandbox and does not provide
authentication, authorization, containers, or VM isolation.

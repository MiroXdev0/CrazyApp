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

The Go controller owns scheduling and job placement. The Rust worker owns local resource validation and execution management.

HTTP/JSON is only the management API. The node data plane uses the binary protocol.

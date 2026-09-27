# Nodren Controller

This service is the control plane for the first Nodren prototype.

It is designed to:
- accept node connections
- assign unique node IDs
- maintain a node registry
- monitor heartbeat health
- track node state
- provide a stable protocol foundation for future scheduling and workload distribution

## Current behavior

- starts an HTTP-based controller service
- exposes node and job status endpoints
- keeps a simple in-memory registry of nodes
- serves as the base for the real distributed cluster runtime

## Next milestone

Add real TCP sockets, binary or line-based protocol framing, and node heartbeat logic.

# Distributed mesh with HTTP daemons and shared state

Model-shelf evolves from a single-machine local tool into a distributed model mesh. Each node runs a lightweight HTTP daemon (port 8844). All nodes maintain full mesh state via gossip + periodic poll. Communication is peer-to-peer with seed-based discovery. Authentication uses a shared mesh key. File transfers use destination-pull (the node that needs the model initiates the fetch from a peer or from Hugging Face).

We chose HTTP daemons over SSH-based command execution because SSH requires an SSH server on every target (problematic on Windows), couples the tool to SSH key management, and doesn't support push-based state updates. We chose full mesh state on all nodes (over hub-and-spoke or controller-only state) because the mesh is small (< 10 nodes) and any node may need to make placement decisions or respond to queries.

Graceful degradation preserves backwards compatibility: if no daemon is configured, `model-shelf resolve` works as a standalone local tool with no mesh awareness.

## Considered Options

- **SSH-based**: Controller pushes commands via SSH. Rejected because Windows SSH server is friction, and it doesn't support daemon features (gossip, status reporting, state caching).
- **Hub-and-spoke**: Single coordinator owns all state. Rejected because it's a single point of failure and the mesh is small enough that full replication is trivial.
- **Separate binary** (`model-mesh`): Rejected in favour of a single binary with graceful degradation — avoids ecosystem fragmentation and confusion over which command to use.

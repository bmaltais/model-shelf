# Controller-orchestrated self-fetch upgrade

Mesh upgrades are initiated from a Controller node via `model-shelf upgrade`. The controller fans out a `POST /v1/upgrade` request (carrying only the target version string) to all reachable peers in parallel; each node independently fetches the correct binary for its own OS/arch from GitHub Releases, verifies its SHA256 against the published `checksums.txt`, saves a `.bak` of the existing binary, replaces it, and restarts its own service. The controller polls peers for health confirmation, then self-upgrades last.

## Considered options

**Controller pushes binaries** — controller downloads each platform binary and streams it to each peer over the mesh. Rejected: requires tracking OS/arch per node in gossip state (not carried today), uses controller bandwidth for every node, and the controller becomes a bottleneck. The self-fetch pattern parallelises downloads naturally.

**Any node can initiate** — any node can fan out an upgrade to all peers. Rejected: inconsistent with the Controller role's existing responsibility for issuing commands and maintaining mesh state.

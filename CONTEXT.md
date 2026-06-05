# Model Shelf

A distributed model mesh for managing, placing, and resolving Hugging Face models across multiple machines. Agents issue commands via CLI; model-shelf coordinates placement, transfers, and lifecycle across the mesh.

## Language

**Node**:
A machine registered in the mesh that runs the model-shelf daemon.
_Avoid_: server, host, peer

**Mesh**:
The network of all nodes that coordinate model placement, transfers, and state.
_Avoid_: cluster, swarm, grid

**Controller**:
A node role that issues commands, runs agents, and maintains full mesh state.
_Avoid_: master, orchestrator, manager

**Store**:
A node role that holds model files on disk. Can receive downloads from Hugging Face or transfers from other nodes.
_Avoid_: cache, repository, warehouse

**Executor**:
A node role that runs inference. Has a GPU and a runtime (vLLM, Ollama). May also be a Store.
_Avoid_: worker, runner, inference node

**Pull**:
Async command to make a model available on a node. Fire-and-forget; returns a job ID.
_Avoid_: fetch, download (download is one strategy pull may use internally)

**Resolve**:
Synchronous command to get a local path to a model, downloading if needed. Local-only, backwards-compatible.
_Avoid_: locate, find

**Placement**:
The logic that decides which node receives a model when no explicit target is specified. Considers VRAM capacity, disk space, and current load.
_Avoid_: scheduling, routing

**Eviction**:
Removing a model from a node to free space. Never evicts the last copy in the mesh. Uses LRU by last-accessed timestamp.
_Avoid_: deletion, cleanup, garbage collection

**Job**:
A tracked async operation — download from Hugging Face, transfer between nodes, or eviction cascade. Part of mesh state, gossiped to all nodes.
_Avoid_: task, work item, request

**Mesh Key**:
A shared secret generated at mesh creation. All daemon-to-daemon communication requires it.
_Avoid_: token, password, API key

**Shelf**:
The directory tree on a node where models are stored, organized by format/publisher/repo.
_Avoid_: cache, library, vault

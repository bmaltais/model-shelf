# Fire-and-forget pull with LRU eviction and smart placement

The `pull` command is async (fire-and-forget, returns a job ID). When no `--target` is specified, model-shelf auto-selects the best Executor based on VRAM capacity (estimated from HF file size + formula) and available disk space. When a target node lacks space, model-shelf cascades: evicts the least-recently-used model (that exists on another node or can be re-downloaded) to a peer with space, then proceeds with the pull.

We chose fire-and-forget over synchronous because large model downloads take 10-30+ minutes and agent tool calls have timeouts. We chose LRU by internally-tracked last-accessed timestamp (over filesystem atime or manual priority) because it works without the `serve` feature and provides reasonable automatic behavior from day one. The key invariant: never evict the last copy of a model from the mesh.

`resolve` remains as the synchronous local-only command (returns a path immediately) for backwards compatibility with existing agents and the Claude Code plugin.

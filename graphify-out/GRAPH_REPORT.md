# Graph Report - .  (2026-06-06)

## Corpus Check
- Corpus is ~44,490 words - fits in a single context window. You may not need a graph.

## Summary
- 756 nodes · 1731 edges · 25 communities (22 shown, 3 thin omitted)
- Extraction: 77% EXTRACTED · 23% INFERRED · 0% AMBIGUOUS · INFERRED: 391 edges (avg confidence: 0.78)
- Token cost: 0 input · 0 output

## Community Hubs (Navigation)
- [[_COMMUNITY_Python CLI Commands|Python CLI Commands]]
- [[_COMMUNITY_Go Mesh Node Commands|Go Mesh Node Commands]]
- [[_COMMUNITY_Go Model Resolver|Go Model Resolver]]
- [[_COMMUNITY_Go Config and Tests|Go Config and Tests]]
- [[_COMMUNITY_Daemon Unit Tests|Daemon Unit Tests]]
- [[_COMMUNITY_Daemon HTTP Handlers|Daemon HTTP Handlers]]
- [[_COMMUNITY_Daemon Pull and Inventory|Daemon Pull and Inventory]]
- [[_COMMUNITY_Python Config Module|Python Config Module]]
- [[_COMMUNITY_Architecture Decisions|Architecture Decisions]]
- [[_COMMUNITY_Shelf Relocation|Shelf Relocation]]
- [[_COMMUNITY_Job Status Tracking|Job Status Tracking]]
- [[_COMMUNITY_Go Config Module|Go Config Module]]
- [[_COMMUNITY_Daemon Job Store|Daemon Job Store]]
- [[_COMMUNITY_Linux Service Manager|Linux Service Manager]]
- [[_COMMUNITY_Model Search (HF Hub)|Model Search (HF Hub)]]
- [[_COMMUNITY_Mesh Pull Command|Mesh Pull Command]]
- [[_COMMUNITY_macOS Service Manager|macOS Service Manager]]
- [[_COMMUNITY_Storage Detection|Storage Detection]]
- [[_COMMUNITY_Claude Code Plugin|Claude Code Plugin]]
- [[_COMMUNITY_Go Storage Detection|Go Storage Detection]]
- [[_COMMUNITY_Install Script|Install Script]]
- [[_COMMUNITY_Session Hooks|Session Hooks]]
- [[_COMMUNITY_CI Build Pipeline|CI Build Pipeline]]

## God Nodes (most connected - your core abstractions)
1. `New()` - 43 edges
2. `Gossip` - 30 edges
3. `cmdInit()` - 28 edges
4. `ConfigPath()` - 25 edges
5. `WriteTo()` - 23 edges
6. `Daemon` - 22 edges
7. `Config` - 21 edges
8. `Path` - 21 edges
9. `resolve_model()` - 20 edges
10. `cmdJoin()` - 18 edges

## Surprising Connections (you probably didn't know these)
- `bool` --uses--> `Config`  [INFERRED]
  src/model_shelf/config.py → src/model_shelf/resolver.py
- `bool` --uses--> `StorageNotAvailableError`  [INFERRED]
  tests/test_resolver.py → src/model_shelf/resolver.py
- `Path` --uses--> `StorageNotAvailableError`  [INFERRED]
  tests/test_resolver.py → src/model_shelf/resolver.py
- `bool` --uses--> `ShelfNotInitializedError`  [INFERRED]
  tests/test_resolver.py → src/model_shelf/resolver.py
- `Path` --uses--> `ShelfNotInitializedError`  [INFERRED]
  tests/test_resolver.py → src/model_shelf/resolver.py

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Node Roles** — context_controller, context_store, context_executor [EXTRACTED 1.00]
- **Supported Model Formats** — readme_gguf_format, readme_mlx_format, readme_safetensors_format [EXTRACTED 1.00]
- **Model Lifecycle in Mesh** — context_pull, context_placement, context_eviction, context_job [INFERRED 0.85]

## Communities (25 total, 3 thin omitted)

### Community 0 - "Python CLI Commands"
Cohesion: 0.06
Nodes (90): cmd_find(), cmd_init(), cmd_list(), cmd_resolve(), _fmt_size(), _format_choice_label(), main(), _pick_candidate_interactively() (+82 more)

### Community 1 - "Go Mesh Node Commands"
Cohesion: 0.07
Nodes (76): Duration, Config, MeshNode, T, T, T, MeshNode, T (+68 more)

### Community 2 - "Go Model Resolver"
Cohesion: 0.06
Nodes (69): T, Request, T, hfRepoFile, Check, Config, DownloadLockError, acquireLock() (+61 more)

### Community 3 - "Go Config and Tests"
Cohesion: 0.06
Nodes (60): T, T, T, Config, T, Status, GetHostname(), LoadFrom() (+52 more)

### Community 4 - "Daemon Unit Tests"
Cohesion: 0.07
Nodes (60): New(), TestAuthMiddleware_HealthPublic(), TestAuthMiddleware_InvalidKey(), TestAuthMiddleware_MissingKey(), TestAuthMiddleware_NoKey(), TestAuthMiddleware_ValidKey(), TestHealthEndpoint(), TestHealthMethodNotAllowed() (+52 more)

### Community 5 - "Daemon HTTP Handlers"
Cohesion: 0.08
Nodes (27): CancelFunc, Context, diskUsage(), Event, EventType, Gossip, deduplicateByAddress(), validEventType() (+19 more)

### Community 6 - "Daemon Pull and Inventory"
Cohesion: 0.08
Nodes (35): Inventory, dirSizeBytes(), ExtractQuant(), inventoryStatePath(), LoadInventory(), looksLikeQuant(), NewInventory(), scanGGUF() (+27 more)

### Community 7 - "Python Config Module"
Cohesion: 0.14
Nodes (28): bootstrap_default_config(), load_config(), _load_raw(), Load (or first-run bootstrap) Model Shelf config from a TOML file.  Lookup order, Write a config file at `path`. Overwrites if present.      If `shelf_root` is No, Where `model-shelf init` should write the config (mirrors load_config)., Create a default config at `path` (or USER_CONFIG) if missing. Returns the path., _read() (+20 more)

### Community 8 - "Architecture Decisions"
Cohesion: 0.08
Nodes (26): ADR 0001: Distributed Mesh with HTTP Daemons, Gossip Protocol, Graceful Degradation, HTTP Daemon (port 8844), ADR 0002: Async Pull with LRU Eviction, LRU Eviction, Smart Placement, VRAM Estimation (+18 more)

### Community 9 - "Shelf Relocation"
Cohesion: 0.15
Nodes (24): _extract_volume_subpath(), find_shelf_at_subpath(), Find the shelf even if its drive was renamed or remounted under a different name, If shelf_root is under /Volumes/<name>/<sub...>, return (/Volumes, sub)., Return the first mounted /Volumes/* drive that has `<vol>/<subpath>` as a direct, Return the effective shelf_root, possibly relocated to a renamed/swapped drive., relocate_shelf(), Path (+16 more)

### Community 10 - "Job Status Tracking"
Cohesion: 0.19
Nodes (23): Job, Time, Server, T, Time, cmdStatus(), fmtBytes(), httpGet() (+15 more)

### Community 11 - "Go Config Module"
Cohesion: 0.19
Nodes (17): BootstrapDefaultConfig(), LoadConfig(), loadFromMeshConfig(), loadRaw(), readConfig(), TestLoadConfig_ExplicitPathExists(), TestLoadConfig_ExplicitPathNonExistentParent(), TestLoadConfig_ExplicitPathNotFound() (+9 more)

### Community 12 - "Daemon Job Store"
Cohesion: 0.18
Nodes (8): Job, generateJobID(), jobStateOrder(), shouldReplace(), JobStatus, JobStore, RWMutex, Time

### Community 13 - "Linux Service Manager"
Cohesion: 0.21
Nodes (17): Status, T, getStatus(), install(), start(), stop(), systemctl(), systemctlOutput() (+9 more)

### Community 14 - "Model Search (HF Hub)"
Cohesion: 0.19
Nodes (15): find_models(), FindResult, Search Hugging Face Hub for models matching a loose text query.  Lets the agent, Search the HF Hub. Returns results, optionally filtered to one format., int, str, _FakeModelInfo, _patch_list_models() (+7 more)

### Community 15 - "Mesh Pull Command"
Cohesion: 0.24
Nodes (16): MeshNode, T, cmdPull(), knownNodeNames(), loadMeshKey(), pullConnectionHint(), pullStatusHint(), resolveTargetNode() (+8 more)

### Community 16 - "macOS Service Manager"
Cohesion: 0.22
Nodes (16): Status, T, getStatus(), install(), launchctl(), launchctlOutput(), plistContent(), plistDir() (+8 more)

### Community 17 - "Storage Detection"
Cohesion: 0.26
Nodes (14): detect_storage_candidates(), Detect plausible Model Shelf locations on the user's machine.  Scans /Volumes/ f, Return ranked candidates.      Order: external drives with existing shelves firs, Path, _make_external_drive(), bool, Path, str (+6 more)

### Community 18 - "Claude Code Plugin"
Cohesion: 0.22
Nodes (8): author, name, description, homepage, license, name, repository, version

### Community 19 - "Go Storage Detection"
Cohesion: 1.00
Nodes (3): DetectStorageCandidates(), DetectStorageCandidatesAt(), StorageCandidate

## Knowledge Gaps
- **63 isolated node(s):** `name`, `description`, `version`, `name`, `homepage` (+58 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **3 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `New()` connect `Daemon Unit Tests` to `Go Mesh Node Commands`, `Go Config and Tests`, `Daemon HTTP Handlers`, `Daemon Pull and Inventory`?**
  _High betweenness centrality (0.147) - this node is a cross-community bridge._
- **Why does `cmdInit()` connect `Go Config and Tests` to `Go Mesh Node Commands`, `Go Model Resolver`?**
  _High betweenness centrality (0.057) - this node is a cross-community bridge._
- **Why does `GetHostname()` connect `Go Config and Tests` to `Go Mesh Node Commands`, `Daemon Unit Tests`?**
  _High betweenness centrality (0.051) - this node is a cross-community bridge._
- **Are the 40 inferred relationships involving `New()` (e.g. with `NewGossip()` and `LoadInventory()`) actually correct?**
  _`New()` has 40 INFERRED edges - model-reasoned connections that need verification._
- **Are the 25 inferred relationships involving `cmdInit()` (e.g. with `TestCmdInit_Help()` and `TestCmdInit_CreatesConfigAndShelf()`) actually correct?**
  _`cmdInit()` has 25 INFERRED edges - model-reasoned connections that need verification._
- **Are the 20 inferred relationships involving `ConfigPath()` (e.g. with `TestCmdInventory_DaemonNotRunning()` and `TestCmdInventory_Empty()`) actually correct?**
  _`ConfigPath()` has 20 INFERRED edges - model-reasoned connections that need verification._
- **Are the 20 inferred relationships involving `WriteTo()` (e.g. with `.Create()` and `TestCmdInventory_DaemonNotRunning()`) actually correct?**
  _`WriteTo()` has 20 INFERRED edges - model-reasoned connections that need verification._
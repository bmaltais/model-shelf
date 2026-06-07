# Model Shelf

A local-first resolver for Hugging Face models — GGUF, MLX, and safetensors. Your agent checks your curated library before downloading.

```
> model-shelf resolve "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M

  shelf  /Volumes/MyDrive/ModelShelf/models/gguf          HIT

  status      found
  source      local_shelf
  format      gguf
  path        /Volumes/MyDrive/ModelShelf/models/gguf/Qwen/Qwen3-14B-GGUF/Qwen3-14B-Q4_K_M.gguf
```

## Why

Local AI workflows download the same model files over and over — across tools, runtimes, and machines. Model Shelf gives you one curated library at a path you own, and one shell command that asks: *do I already have this locally?*

- Handles **GGUF, MLX, and safetensors** through one CLI — format auto-detected from the repo id.
- **Publisher/repo layout** that mirrors Hugging Face Hub (and matches LM Studio's expected structure) — `gguf/Qwen/Qwen3-14B-GGUF/Qwen3-14B-Q4_K_M.gguf` instead of `models--Qwen--Qwen3-14B-GGUF/snapshots/abc.../...`.
- Works with **any** storage you already mount: external SSD, Thunderbolt DAS, NAS, or just an internal folder.
- Downloads land **directly** in the shelf at the friendly path — no parallel Hugging Face cache to manage or clean up.
- A single shell command (`model-shelf resolve … --json`) means any agent that can run shell commands can plug it in — no special protocols, no extra server.

## Install

### Claude Code (one command)

```
/plugin install model-shelf@bmaltais/model-shelf
```

That's it. The plugin installs a [skill](skills/resolve/SKILL.md) that tells the agent to always resolve through Model Shelf, plus a SessionStart hook that auto-installs the CLI via `uv` on first session. Requires [`uv`](https://docs.astral.sh/uv/) — install with `curl -LsSf https://astral.sh/uv/install.sh | sh` if you don't have it.

### Quick install (recommended)

Auto-detects your OS and architecture, installs to `~/.local/bin`:

```bash
curl -LsSf https://raw.githubusercontent.com/bmaltais/model-shelf/main/install.sh | sh
```

> **Note:** Ensure `~/.local/bin` is on your PATH. Add `export PATH="$HOME/.local/bin:$PATH"` to your shell profile if needed.

### Go binary (manual download)

Download the latest binary for your platform from [Releases](https://github.com/bmaltais/model-shelf/releases):

```bash
# User-local install (no sudo required) — recommended
mkdir -p ~/.local/bin

# macOS Apple Silicon
curl -L https://github.com/bmaltais/model-shelf/releases/latest/download/model-shelf-darwin-arm64 -o ~/.local/bin/model-shelf
chmod +x ~/.local/bin/model-shelf

# macOS Intel
curl -L https://github.com/bmaltais/model-shelf/releases/latest/download/model-shelf-darwin-amd64 -o ~/.local/bin/model-shelf
chmod +x ~/.local/bin/model-shelf

# Linux amd64
curl -L https://github.com/bmaltais/model-shelf/releases/latest/download/model-shelf-linux-amd64 -o ~/.local/bin/model-shelf
chmod +x ~/.local/bin/model-shelf

# Linux arm64
curl -L https://github.com/bmaltais/model-shelf/releases/latest/download/model-shelf-linux-arm64 -o ~/.local/bin/model-shelf
chmod +x ~/.local/bin/model-shelf

# Windows (PowerShell)
Invoke-WebRequest -Uri https://github.com/bmaltais/model-shelf/releases/latest/download/model-shelf-windows-amd64.exe -OutFile model-shelf.exe
```

<details>
<summary>System-wide install (requires sudo)</summary>

Replace the URL with the correct binary for your OS/architecture (see platform list above):

```bash
curl -L https://github.com/bmaltais/model-shelf/releases/latest/download/model-shelf-linux-amd64 -o model-shelf
chmod +x model-shelf && sudo mv model-shelf /usr/local/bin/
```
</details>

Or build from source (requires Go 1.22+):

```bash
cd go && go build -o model-shelf ./cmd/model-shelf
```

### Python (pip / uv) — legacy, deprecated

> **Deprecated:** The Python package is the legacy implementation that predates the Go rewrite. It does not include mesh networking, peer transfers, or any features added after v0.14.0. Use the Go binary (curl install or build from `go/`) for all current functionality.

```bash
uv tool install git+https://github.com/bmaltais/model-shelf
# or
pip install git+https://github.com/bmaltais/model-shelf
```

Requires Python 3.11+.

## Configure

Initialize a mesh node:

```bash
# Controller + store node (generates a mesh key for other nodes to join)
model-shelf init --role controller,store --shelf /mnt/nas/ai-models

# Store-only node
model-shelf init --role store --shelf ~/.cache/model-shelf/models

# With a custom node name
model-shelf init --role store --shelf /data/models --name gpu-box-1

# Overwrite existing config
model-shelf init --role controller,store --shelf /data/models --force
```

**Required flags:**
- `--role <roles>` — comma-separated list of node roles: `controller`, `store`, `executor`
- `--shelf <path>` — path to the model storage directory

**Optional flags:**
- `--name <name>` — node name (defaults to hostname)
- `--seed <addrs>` — comma-separated seed peer addresses (e.g. `host1:8844,host2:8844`); pre-populates the peer list so the node can gossip without running `join` first
- `--key <mesh-key>` — write a specific mesh key to disk instead of generating one (useful when re-imaging a node that should rejoin an existing mesh)
- `--force` — overwrite existing config

Init creates the shelf directory structure (gguf/, mlx/, safetensors/ subdirectories) and writes the mesh config to `~/.model-shelf/config.toml`. If the node has the `controller` role, a mesh key is generated automatically.

> **Shelf auto-discovery:** If no `shelf_root` is set in config, `resolve` and `list` auto-discover the shelf — first by scanning mounted volumes for a `ModelShelf/models` directory (macOS), then falling back to `~/.cache/model-shelf/models/`. Use `--shelf` during `init` to pin a specific location.

### Join an existing mesh

After initializing a non-controller node, join the mesh via a controller:

```bash
model-shelf join ocilab1:8844 --key <mesh-key>
```

The mesh key is displayed when the controller is initialized. All nodes in the mesh share the same key for authentication.

**Non-interactive / CI join** — set `MODEL_SHELF_MESH_KEY` instead of passing `--key`:

```bash
export MODEL_SHELF_MESH_KEY="$(cat mesh.key)"
model-shelf join ocilab1:8844
```

The environment variable is read when `--key` is not provided. This is the recommended approach for automated provisioning scripts and CI pipelines where passing secrets on the command line is undesirable.

### Mesh daemon

The daemon runs in the background and handles health polling, gossip, and node coordination:

```bash
# Install as a system service (auto-starts on login)
model-shelf service install

# Or run in the foreground for debugging
model-shelf daemon

# Manage the service
model-shelf service start
model-shelf service stop
model-shelf service restart
model-shelf service status
model-shelf service uninstall
```

### View mesh status

```bash
# Human-readable table with status and disk metrics
model-shelf nodes

# JSON output for scripting (includes disk, uptime, gpu, last_seen)
model-shelf nodes --json
```

### GPU auto-detection

The daemon auto-detects GPU hardware on startup:

- **NVIDIA**: runs `nvidia-smi --query-gpu=name,memory.total,memory.free --format=csv,noheader,nounits`
- **Apple Silicon / unified memory**: detects via `sysctl` (total memory) and `vm_stat` (available)

GPU info is reported in `GET /v1/health` and gossiped to all mesh peers. Available VRAM is refreshed on each health check and gossip poll cycle (every 15 seconds).

Nodes without a GPU report `"gpu": null`.

**Manual override** in `~/.model-shelf/config.toml` for edge cases (multi-GPU, misdetection):

```toml
[gpu]
name = "NVIDIA A100-SXM4-80GB"
vram_total_gb = 80.0
```

## CLI

```bash
# Initialize a mesh node (required before anything else)
model-shelf init --role controller,store --shelf /path/to/models

# Join an existing mesh
model-shelf join <peer>:<port> --key <mesh-key>

# Leave the mesh (gossips departure to peers, clears mesh state)
model-shelf leave

# Start the mesh daemon (background service)
model-shelf service install    # install + enable + start
model-shelf service start      # start if already installed
model-shelf service stop       # stop
model-shelf service restart    # stop + start (applies config changes)
model-shelf service status     # show whether running
model-shelf service uninstall  # remove

# Run daemon in foreground (debugging)
model-shelf daemon [--port <N>]

# View mesh nodes (status, disk, GPU, last seen)
model-shelf nodes
model-shelf nodes --json

# List models across all mesh nodes (requires daemon running)
model-shelf inventory
model-shelf inventory --json

# Pull a model to a specific node (async, returns job ID)
# If a mesh peer already has the model, transfers from the peer instead of re-downloading
model-shelf pull "Qwen/Qwen3-14B-GGUF" --target gpu-box-1 --quant Q4_K_M
model-shelf pull "mlx-community/Qwen3-14B-4bit" --target mac-mini

# Smart placement: auto-select best Executor when --target is omitted
# Queries HF API for model size, estimates VRAM, picks the best Executor
model-shelf pull "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M
model-shelf pull "mlx-community/Qwen3-14B-4bit" --json

# Force re-pull even if the model is already present (deletes existing copy first)
model-shelf pull "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M --force

# Control where the model comes from
model-shelf pull "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M --source hf    # always download from HF
model-shelf pull "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M --source peer  # only transfer from a mesh peer
model-shelf pull "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M --source auto  # peer first, fall back to HF (default)

# Show job status (downloads, transfers)
# Defaults to mesh-wide on controller nodes or when seeds are configured
model-shelf status
model-shelf status --local           # only local daemon jobs
model-shelf status --mesh            # force mesh-wide aggregation
model-shelf status <job_id> --json

# Manage node roles
model-shelf role set controller,store
model-shelf role add executor
model-shelf role remove controller

# Search Hugging Face for a loose query
model-shelf find "qwen 3 4b mlx 4-bit" --format mlx --limit 5

# GGUF (format auto-detected; --quant required for gguf)
model-shelf resolve "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M

# MLX (auto-detected from mlx-community/* or *-mlx)
model-shelf resolve "mlx-community/Qwen3-14B-4bit"

# Safetensors (default when nothing else matches)
model-shelf resolve "Qwen/Qwen3-14B"

# Force a specific format
model-shelf resolve "Qwen/Qwen3-14B" --format safetensors

# Never reach out to the network, even on a miss.
model-shelf resolve "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M --no-download

# Skip slow mesh peer transfer, prefer HF CDN.
model-shelf resolve "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M --source hf

# Emit JSON for scripting.
model-shelf resolve "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M --json

# List what's on the local shelf (all three format subfolders).
model-shelf list
model-shelf list --json

# Override config file path (useful in multi-shelf setups or CI)
model-shelf list --config /path/to/config.toml --json
model-shelf resolve "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M --config /path/to/config.toml

# Print the installed version
model-shelf version

# Upgrade the binary to the latest GitHub release
model-shelf upgrade

# Pin to a specific release
model-shelf upgrade --version 0.5.9

# Skip confirmation prompt
model-shelf upgrade --yes

# Reinstall the same version (useful to repair a broken binary)
model-shelf upgrade --force
```

### Upgrade

`model-shelf upgrade` fetches the latest release from GitHub, verifies the SHA256 checksum against the published `checksums.txt`, saves the current binary as `<binary-path>.bak`, and atomically replaces the running binary. If the daemon service is installed it is restarted automatically; otherwise a reminder is printed.

```
Flags:
  --version <x.y.z>  Pin upgrade to a specific release (default: latest)
  --yes              Skip the confirmation prompt
  --force            Proceed even if the binary is already at the target version
```

Exit codes: `0` on found/downloaded, `1` on missing (not found anywhere), `2` on missing locally but available on a mesh peer (actionable — run `pull`).

### JSON output

`model-shelf resolve --json` returns:

```json
{
  "status": "found",
  "source": "local_shelf",
  "format": "gguf",
  "path": "/mnt/nas/ai-models/gguf/Qwen/Qwen3-14B-GGUF/Qwen3-14B-Q4_K_M.gguf",
  "checks": [{"location": "shelf", "root": "/mnt/nas/ai-models/gguf", "result": "hit"}]
}
```

When a model is missing locally but available on a mesh peer:

```json
{
  "status": "missing_locally",
  "source": "mesh",
  "format": "gguf",
  "path": null,
  "mesh_available": [{"node": "gpu-box-1", "path": "gguf/Qwen/Qwen3-14B-GGUF/Qwen3-14B-Q4_K_M.gguf"}],
  "checks": [{"location": "shelf", "root": "/mnt/nas/ai-models/gguf", "result": "miss"}]
}
```

`model-shelf nodes --json` returns:

```json
[
  {
    "name": "gpu-box-1",
    "address": "10.0.0.2",
    "port": 8844,
    "roles": ["executor", "store"],
    "status": "online",
    "missed_polls": 0,
    "disk_free_gb": 120.5,
    "disk_total_gb": 500.0,
    "uptime_seconds": 86400,
    "gpu": {"name": "NVIDIA RTX 4090", "vram_total_gb": 24.0, "vram_available_gb": 18.3},
    "last_seen": "2025-06-01T12:00:00Z"
  }
]
```

All fields:

| Field | Type | Description |
|---|---|---|
| `name` | string | Node name (set during `init`) |
| `address` | string | IP or hostname |
| `port` | int | Daemon listen port |
| `roles` | string[] | Node roles: `controller`, `store`, `executor` |
| `status` | string | `online` or `offline` |
| `missed_polls` | int | Consecutive failed health polls (omitted when 0) |
| `disk_free_gb` | float | Free disk space on shelf volume (GB) |
| `disk_total_gb` | float | Total disk space on shelf volume (GB) |
| `uptime_seconds` | float | Daemon uptime in seconds |
| `gpu` | object\|null | GPU info (see below); `null` if no GPU detected |
| `gpu.name` | string | GPU model name |
| `gpu.vram_total_gb` | float | Total VRAM (GB) |
| `gpu.vram_available_gb` | float | Available VRAM (GB) |
| `last_seen` | RFC3339\|null | Timestamp of last successful health poll |

`model-shelf list --json` returns:

```json
[
  {
    "repo_id": "Qwen/Qwen3-14B-GGUF",
    "format": "gguf",
    "quant": "Q4_K_M",
    "size_bytes": 8320000000,
    "path": "/mnt/nas/ai-models/gguf/Qwen/Qwen3-14B-GGUF/Qwen3-14B-Q4_K_M.gguf",
    "shelf_root": "/mnt/nas/ai-models"
  }
]
```

`model-shelf inventory --json` returns models across all mesh nodes:

```json
[
  {"node": "gpu-box-1", "repo_id": "Qwen/Qwen3-14B-GGUF", "format": "gguf", "quant": "Q4_K_M", "size_bytes": 8320000000},
  {"node": "nas-store", "repo_id": "mlx-community/Qwen3-14B-4bit", "format": "mlx", "size_bytes": 7500000000},
  {"node": "offline-node", "repo_id": "Qwen/Qwen3-14B-GGUF", "format": "gguf", "quant": "Q4_K_M", "size_bytes": 8320000000, "stale": true}
]
```

The `stale` field is `true` when the node was unreachable at query time — the row reflects cached state from the last successful contact.

`model-shelf status --json` returns in-flight and recent jobs:

```json
[
  {
    "job_id": "abc123",
    "type": "transfer",
    "repo_id": "Qwen/Qwen3-14B-GGUF",
    "format": "gguf",
    "quant": "Q4_K_M",
    "target": "gpu-box-1",
    "source": "nas-store",
    "status": "transferring",
    "bytes_downloaded": 4160000000,
    "bytes_total": 8320000000,
    "created_at": "2025-06-01T12:00:00Z",
    "done_at": null,
    "error": "",
    "last_progress": "2025-06-01T12:01:30Z"
  }
]
```

All fields:

| Field | Type | Description |
|---|---|---|
| `job_id` | string | Unique job identifier |
| `type` | string | `download` or `transfer` |
| `repo_id` | string | Hugging Face repo id |
| `format` | string | `gguf`, `mlx`, or `safetensors` |
| `quant` | string | Quantization (GGUF only, omitted otherwise) |
| `target` | string | Node the model is being pulled to |
| `source` | string | Source node name (peer transfers only) |
| `status` | string | `queued`, `downloading`, `transferring`, `evicting`, `completed`, `failed`, `already_present`, `already_in_progress` |
| `bytes_downloaded` | int64 | Bytes received so far |
| `bytes_total` | int64 | Total size (0 if unknown) |
| `created_at` | RFC3339 | When the job was enqueued |
| `done_at` | RFC3339\|null | When the job finished (`null` if still running) |
| `error` | string | Error message (non-empty only on `failed`) |
| `last_progress` | RFC3339 | Timestamp of the last progress update |

## Agent integration

If you installed via `/plugin install`, you're done — the bundled skill tells the agent to always call `model-shelf resolve` before any Hugging Face download, and the SessionStart hook keeps the CLI installed. You may want to pre-allow the CLI in permissions so the agent doesn't prompt every time:

```json
{
  "permissions": {
    "allow": ["Bash(model-shelf resolve:*)", "Bash(model-shelf list:*)"]
  }
}
```

For any other agent: copy [`skills/resolve/SKILL.md`](skills/resolve/SKILL.md) into wherever your agent reads instructions from, and allow `model-shelf resolve` as a tool. The agent calls:

```
model-shelf resolve "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M --json
```

and gets back:

```json
{
  "status": "found",
  "source": "local_shelf",
  "path":   "/Volumes/MyDrive/ModelShelf/hf-cache/.../Qwen3-14B-Q4_K_M.gguf",
  "checks": [ ... ]
}
```

## How it works

Format is detected from the repo id (override with `--format`):

| Repo pattern | Format | Notes |
|---|---|---|
| `*-GGUF` (case-insensitive) | `gguf` | requires `--quant` |
| `mlx-community/*` or `*-mlx` | `mlx` | directory of files |
| anything else | `safetensors` | directory of files |

For every resolve request:

1. **Curated shelf** — looks in `shelf_root/<format>/`. Hit → return.
2. **Mesh peers** — if the model is missing locally and the daemon is running, queries other nodes in the mesh. If found on a peer, returns `status="missing_locally"` with `mesh_available` listing which nodes have it. Skipped when `--source hf` is set.
3. **Download** — downloads from the Hugging Face Hub REST API directly into the shelf, so the file lands at the friendly path. For GGUF, the actual filename is looked up via the HF API. Pass `--no-download` to skip this step and return `status="missing"` instead. Skipped when `--source peer` is set.

The `--source` flag (or `prefer_source` in config) controls source selection:
- `auto` (default) — try mesh peers first, fall back to HF
- `hf` — skip mesh peers entirely, always download from Hugging Face CDN
- `peer` — only use mesh peers, never fall back to HF download

For `pull` operations to a target node:

1. **Peer transfer** — if another mesh node already has the model, the target pulls directly from the peer (node-to-node, no re-download from HF). GGUF files transfer as single files; MLX/safetensors transfer as tar archives.
2. **HF download** — if no peer has the model, downloads from Hugging Face Hub directly to the target.

### Smart Placement

When `--target` is omitted from `model-shelf pull`, smart placement auto-selects the best Executor node:

1. **Estimate VRAM** — queries the HF API for file sizes (without downloading). GGUF: file_size × 1.1; safetensors/mlx: sum of weight files × 1.1.
2. **Filter** — finds all online Executor nodes where total VRAM ≥ estimated requirement.
3. **Rank** — prefers Executors that already have the model on disk, then most free disk space, then fewest active jobs (as a tiebreaker to avoid piling new pulls onto a node that is already busy).
4. **Error** — if no Executor can fit the model, returns a clear error listing available VRAM vs. what's needed.

With `--json`, the response includes a `placement` field showing the selected node and reason.

Set `HF_TOKEN` or `HUGGING_FACE_HUB_TOKEN` for gated model access.

Curated-shelf paths:

| Repo | Quant | Path under `shelf_root` |
|---|---|---|
| `Qwen/Qwen3-14B-GGUF` | `Q4_K_M` | `gguf/Qwen/Qwen3-14B-GGUF/Qwen3-14B-Q4_K_M.gguf` |
| `meta-llama/Llama-3.1-8B-Instruct-GGUF` | `Q5_K_M` | `gguf/meta-llama/Llama-3.1-8B-Instruct-GGUF/Llama-3.1-8B-Instruct-Q5_K_M.gguf` |
| `mlx-community/Qwen3-14B-4bit` | — | `mlx/mlx-community/Qwen3-14B-4bit/` |
| `Qwen/Qwen3-14B` | — | `safetensors/Qwen/Qwen3-14B/` |

A directory-format shelf hit requires the directory to exist **and** contain a `config.json` — that's the minimal "this is actually a model" sanity check.

## Storage backends

The shelf path is set during `init`. Examples:

```bash
# External SSD / Thunderbolt DAS
model-shelf init --role store --shelf /Volumes/MyDAS/ModelShelf/models

# NAS mount
model-shelf init --role store --shelf /mnt/nas/ai-models

# Plain internal folder
model-shelf init --role store --shelf ~/.cache/model-shelf/models
```

## Status

v0.5.9 (Go binary) — GGUF, MLX, and safetensors via CLI. **Mesh networking** with gossip-based node discovery, 15-second health polling, automatic offline detection (~45s), disk/uptime/GPU metrics propagation, and **peer-to-peer model transfers**. Publisher/repo nested layout mirrors the Hugging Face Hub. `model-shelf init --role --shelf` configures mesh nodes; `model-shelf join` connects them. `model-shelf nodes --json` exposes full health metrics (including GPU) for scripting. Smart source selection (`--source auto|hf|peer`) and exit code 2 for `missing_locally`.

### Go version

The Go implementation (`go/`) provides the full CLI in a single static binary — no runtime dependencies. Cross-compiled for macOS, Linux, and Windows (amd64 + arm64). Includes mesh networking (`init`, `join`, `nodes`, `daemon`, `service`), model resolution (`resolve`, `find`, `list`), GPU auto-detection, peer-to-peer transfers, and gossip-based health propagation. Downloads from Hugging Face use the Hub REST API directly; set `HF_TOKEN` or `HUGGING_FACE_HUB_TOKEN` for gated model access.

Roadmap: `verify` subcommand, quantized-safetensors variants (AWQ/GPTQ).

## License

MIT

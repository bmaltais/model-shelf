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

### Python (pip / uv)

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
- `--force` — overwrite existing config

Init creates the shelf directory structure (gguf/, mlx/, safetensors/ subdirectories) and writes the mesh config to `~/.model-shelf/config.toml`. If the node has the `controller` role, a mesh key is generated automatically.

### Join an existing mesh

After initializing a non-controller node, join the mesh via a controller:

```bash
model-shelf join ocilab1:8844 --key <mesh-key>
```

The mesh key is displayed when the controller is initialized. All nodes in the mesh share the same key for authentication.

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
model-shelf service status
model-shelf service uninstall
```

### View mesh status

```bash
# Human-readable table with status and disk metrics
model-shelf nodes

# JSON output for scripting (includes disk_total_gb, disk_free_gb, uptime_seconds, last_seen)
model-shelf nodes --json
```

## CLI

```bash
# Initialize a mesh node (required before anything else)
model-shelf init --role controller,store --shelf /path/to/models

# Join an existing mesh
model-shelf join <peer>:<port> --key <mesh-key>

# Start the mesh daemon (background service)
model-shelf service install    # install + enable + start
model-shelf service start      # start if already installed
model-shelf service stop       # stop
model-shelf service status     # show whether running
model-shelf service uninstall  # remove

# Run daemon in foreground (debugging)
model-shelf daemon [--port <N>]

# View mesh nodes (status, disk metrics, last seen)
model-shelf nodes
model-shelf nodes --json

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

# Emit JSON for scripting.
model-shelf resolve "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M --json

# List what's on the curated shelf (all three format subfolders).
model-shelf list
```

Exit codes: `0` on found/downloaded, `1` on missing.

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
  "source": "hf_cache",
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
2. **Download** — if `allow_downloads = true`, calls `huggingface_hub` with `local_dir` pointed at the shelf, so the file lands directly at the friendly path. For GGUF, a single rename normalizes the HF capitalization to lowercase. Otherwise returns `status="missing"`.

No parallel cache to manage. `huggingface_hub` writes a small hidden `.cache/huggingface/` subfolder inside the shelf for download metadata (resumability) — it's filtered out of `model-shelf list`.

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

v0.14 — GGUF, MLX, and safetensors via CLI + Python lib + **Go binary**. **Mesh networking** with gossip-based node discovery, 15-second health polling, automatic offline detection (~45s), and disk/uptime metrics propagation. Publisher/repo nested layout mirrors the Hugging Face Hub. `model-shelf init --role --shelf` configures mesh nodes; `model-shelf join` connects them. `model-shelf nodes --json` exposes full health metrics for scripting.

### Go version

The Go implementation (`go/`) provides the full CLI in a single static binary — no runtime dependencies. Cross-compiled for macOS, Linux, and Windows (amd64 + arm64). Includes mesh networking (`init`, `join`, `nodes`, `daemon`, `service`), model resolution (`resolve`, `find`, `list`), and gossip-based health propagation. Downloads from Hugging Face use the Hub REST API directly; set `HF_TOKEN` or `HUGGING_FACE_HUB_TOKEN` for gated model access.

Roadmap: `verify` subcommand, quantized-safetensors variants (AWQ/GPTQ).

## License

MIT

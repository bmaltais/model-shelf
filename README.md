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
- **Publisher/repo layout** that mirrors Hugging Face Hub — `gguf/Qwen/Qwen3-14B-GGUF/Qwen3-14B-Q4_K_M.gguf` instead of `models--Qwen--Qwen3-14B-GGUF/snapshots/abc.../...`.
- Works with **any** storage you already mount: external SSD, Thunderbolt DAS, NAS, or just an internal folder.
- Downloads land **directly** in the shelf at the friendly path — no parallel Hugging Face cache to manage or clean up.
- **Resumes interrupted downloads** via HTTP range requests — `.partial` files are cleaned up on error.
- **Parallel snapshot downloads** for MLX and safetensors (4 goroutines by default).
- A single shell command (`model-shelf resolve … --json`) means any agent that can run shell commands can plug it in — no special protocols, no extra server.

## Install

### Go binary (recommended)

**Linux / macOS — one-liner installer:**

```bash
curl -fsSL https://raw.githubusercontent.com/bmaltais/model-shelf/main/install.sh | bash
```

Installs to `~/.local/bin` (no sudo required). Override the destination with `MODEL_SHELF_INSTALL_DIR=/usr/local/bin bash` before piping, or set the env var:

```bash
curl -fsSL https://raw.githubusercontent.com/bmaltais/model-shelf/main/install.sh | MODEL_SHELF_INSTALL_DIR=/usr/local/bin bash
```

<details>
<summary>Manual per-platform install</summary>

```bash
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

> **Note:** Ensure `~/.local/bin` is on your PATH. Add `export PATH="$HOME/.local/bin:$PATH"` to your shell profile if needed.
</details>

### Build from source

Requires Go 1.22+:

```bash
cd go && go build -o model-shelf ./cmd/model-shelf
```

### Claude Code plugin

```
/plugin install model-shelf@bmaltais/model-shelf
```

Installs a [skill](skills/resolve/SKILL.md) that tells the agent to always resolve through Model Shelf before downloading.

### Python (legacy, deprecated)

> **Deprecated:** The Python package is the original implementation. Use the Go binary for all current functionality.

```bash
uv tool install git+https://github.com/bmaltais/model-shelf
```

## Configure

### Init

Set up (or locate) your shelf:

```bash
# Interactive picker — shows mounted drives and lets you choose or enter a custom path
model-shelf init

# Explicit path — creates directories and pins the path in config
model-shelf init /Volumes/MyDrive/ModelShelf/models

# Internal fallback (no external drive)
model-shelf init ~/.cache/model-shelf/models
```

Init creates the shelf directory structure (`gguf/`, `mlx/`, `safetensors/` subdirectories) and writes `~/.config/model-shelf/config.toml` when a path is given explicitly.

**Shelf auto-discovery:** If `init` is run without arguments in an interactive terminal, it scans mounted volumes for existing `ModelShelf/models` directories and presents a TUI picker. If exactly one external shelf already exists, it's used silently. Without a TTY, a path argument is required.

### Config file

```toml
# ~/.config/model-shelf/config.toml
shelf_root      = "/Volumes/MyDrive/ModelShelf/models"
allow_downloads = true
```

Config lookup order:
1. `--config` flag
2. `$MODEL_SHELF_CONFIG` env var
3. `~/.config/model-shelf/config.toml`
4. Bootstrap default at (3) if missing

If `shelf_root` is omitted, the shelf is auto-discovered from mounted volumes on each run.

**Drive relocation:** If `shelf_root` is pinned in config but the drive is remounted under a different volume name, model-shelf scans sibling volumes for the same subpath (`ModelShelf/models`) and uses the first match automatically.

### HF authentication

Set `HF_TOKEN` for gated model access. The token is also read from `~/.cache/huggingface/token` (written by `huggingface-cli login`).

```bash
export HF_TOKEN="hf_..."
```

## CLI

```bash
# Initialize shelf (interactive picker, or explicit path)
model-shelf init [path]

# Resolve a model to a local path — downloads if missing (unless --no-download)
model-shelf resolve <repo_id> [--format gguf|mlx|safetensors] [--quant Q4_K_M]
model-shelf resolve <repo_id> --no-download   # local check only, exit 1 on miss
model-shelf resolve <repo_id> --json          # machine-readable output

# Search Hugging Face Hub
model-shelf find "<query>" [--format gguf|mlx|safetensors] [--limit 10] [--json]

# List what's on the local shelf
model-shelf list
model-shelf list --config /path/to/config.toml

# Print the installed version
model-shelf version

# Override config for any command
model-shelf <cmd> --config /path/to/config.toml
```

### Resolve in detail

```bash
# GGUF (format auto-detected; --quant required)
model-shelf resolve "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M

# MLX (auto-detected from mlx-community/* or *-mlx)
model-shelf resolve "mlx-community/Qwen3-14B-4bit"

# Safetensors (default when nothing else matches)
model-shelf resolve "Qwen/Qwen3-14B"

# Force a specific format
model-shelf resolve "Qwen/Qwen3-14B" --format safetensors

# Never reach out to the network
model-shelf resolve "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M --no-download

# JSON output for scripting
model-shelf resolve "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M --json
```

## JSON output

`model-shelf resolve --json` returns:

```json
{
  "status": "found",
  "source": "local_shelf",
  "format": "gguf",
  "path": "/Volumes/MyDrive/ModelShelf/models/gguf/Qwen/Qwen3-14B-GGUF/Qwen3-14B-Q4_K_M.gguf",
  "checks": [
    {"location": "shelf", "root": "/Volumes/MyDrive/ModelShelf/models/gguf", "result": "hit"}
  ]
}
```

Possible `status` values:

| Status | Meaning | Exit code |
|---|---|---|
| `found` | Already on the local shelf | 0 |
| `downloaded` | Just fetched from HF Hub | 0 |
| `missing` | Not found; downloads disabled (`--no-download` or `allow_downloads = false`) | 1 |

`model-shelf find --json` returns:

```json
[
  {"repo_id": "Qwen/Qwen3-14B-GGUF", "format": "gguf", "downloads": 1234567},
  {"repo_id": "mlx-community/Qwen3-14B-4bit", "format": "mlx", "downloads": 234567}
]
```

## Agent integration

If you installed via `/plugin install`, the bundled skill tells the agent to always call `model-shelf resolve` before any Hugging Face download. You may want to pre-allow the CLI in permissions:

```json
{
  "permissions": {
    "allow": ["Bash(model-shelf resolve:*)", "Bash(model-shelf find:*)", "Bash(model-shelf list:*)"]
  }
}
```

For any other agent: copy [`skills/resolve/SKILL.md`](skills/resolve/SKILL.md) into wherever your agent reads instructions from and allow `model-shelf` as a tool.

## How it works

Format is detected from the repo id (override with `--format`):

| Repo pattern | Format | Notes |
|---|---|---|
| `*-GGUF` (case-insensitive) | `gguf` | requires `--quant` |
| `mlx-community/*` or `*-mlx` | `mlx` | downloads as directory snapshot |
| anything else | `safetensors` | downloads as directory snapshot |

For every resolve request:

1. **Curated shelf** — looks in `shelf_root/<format>/`. Hit → return path immediately.
2. **Download** — if `allow_downloads = true` (default), fetches from the Hugging Face Hub REST API directly into the shelf so the file lands at the friendly path. GGUF files download as single files with resume support; MLX/safetensors download as parallel snapshots (filtered to `*.safetensors`, `*.json`, `tokenizer*`, etc.). Pass `--no-download` to skip and return `status="missing"` instead.

Multiple shelves are supported: if you have models on more than one drive, all existing `ModelShelf/models` candidates are checked before downloading.

Shelf paths:

| Repo | Quant | Path under `shelf_root` |
|---|---|---|
| `Qwen/Qwen3-14B-GGUF` | `Q4_K_M` | `gguf/Qwen/Qwen3-14B-GGUF/Qwen3-14B-Q4_K_M.gguf` |
| `meta-llama/Llama-3.1-8B-Instruct-GGUF` | `Q5_K_M` | `gguf/meta-llama/Llama-3.1-8B-Instruct-GGUF/Llama-3.1-8B-Instruct-Q5_K_M.gguf` |
| `mlx-community/Qwen3-14B-4bit` | — | `mlx/mlx-community/Qwen3-14B-4bit/` |
| `Qwen/Qwen3-14B` | — | `safetensors/Qwen/Qwen3-14B/` |

A directory-format shelf hit requires the directory to exist **and** contain a `config.json`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success (found or downloaded) |
| 1 | Model not found locally and downloads disabled |
| 2 | Storage unavailable / shelf not initialized |
| 3 | HF API / network error |

## License

MIT

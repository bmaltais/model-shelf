# Go Port Design: model-shelf

**Date:** 2026-06-13  
**Status:** Approved

## Overview

A clean-slate Go port of the Python `model-shelf` CLI, feature-for-feature. All mesh/daemon code is removed. The result is a single self-contained binary that resolves, downloads, lists, and searches Hugging Face models against a local shelf.

---

## Commands

```
model-shelf resolve <repo_id> [--format gguf|mlx|safetensors] [--quant Q4_K_M] [--no-download] [--json] [--config path]
model-shelf init [path] [--config path]
model-shelf find <query> [--format ...] [--limit N] [--json] [--config path]
model-shelf list [--config path]
model-shelf version
```

---

## Package Layout

```
go/
  cmd/model-shelf/
    main.go          cobra root + version flag
    resolve.go       resolve subcommand
    init.go          init subcommand (TUI picker)
    find.go          find subcommand
    list.go          list subcommand
  internal/
    config/          TOML load/write, config path resolution
    detect/          cross-platform shelf candidate discovery
    relocate/        find shelf on renamed/remounted drives
    resolver/        core resolve logic, shelf path helpers
    hf/              HF HTTP client (auth, download, parallel snapshot)
    search/          HF Hub search API
    tui/             interactive shelf picker (init)
```

---

## Dependencies

| Package | Purpose |
|---|---|
| `github.com/spf13/cobra` | CLI framework (subcommands, flags, --help, shell completion) |
| `github.com/BurntSushi/toml` | TOML config parsing (already in go.mod) |
| `github.com/charmbracelet/huh` | Interactive TUI picker for `init` |
| `github.com/schollz/progressbar/v3` | Download progress bars |

---

## Data Flow

### Resolve

```
model-shelf resolve Qwen/Qwen3-14B-GGUF --quant Q4_K_M
  → config.Load()
  → relocate.Shelf()          handle renamed/remounted drives
  → resolver.Resolve()
      for each shelf candidate:
          check shelf path → hit → return path
      if allow_downloads:
          hf.Download() → stream to shelf with progress bar
          return path
```

### Init

```
model-shelf init [path]
  if path arg given:
      use it, write config, exit
  else:
      detect.Candidates()
      if exactly one existing external shelf:
          use it silently
      else if TTY:
          huh picker → chosen path
      else (non-TTY):
          print candidates, require path arg, exit 2
  init_shelf(path)   create root + gguf/ mlx/ safetensors/ subdirs
  write_config(path) if path was explicit
```

---

## HF Client (`internal/hf`)

**Auth (in priority order):**
1. `HF_TOKEN` environment variable
2. `~/.cache/huggingface/token` file
3. Unauthenticated

**GGUF download:**
- `GET https://huggingface.co/{repo}/resolve/main/{file}`
- `Range` header for resume (checks existing `.partial` file size)
- CDN redirects followed automatically by `net/http`
- Atomic write: stream to `<file>.partial`, rename on completion, delete on error

**MLX / safetensors snapshot:**
- Fetch file list from `GET https://huggingface.co/api/models/{repo}`
- Filter by allow-patterns (`*.safetensors`, `*.json`, `tokenizer*`, `*.txt`, `*.md`)
- Download files in parallel via goroutine worker pool
- One progress bar per file

---

## Config (`internal/config`)

```toml
# ~/.config/model-shelf/config.toml
shelf_root      = "/Volumes/MyDrive/ModelShelf/models"  # optional
allow_downloads = true
```

**Lookup order:**
1. `--config` flag
2. `$MODEL_SHELF_CONFIG` env var
3. `~/.config/model-shelf/config.toml`
4. Bootstrap default at (3) if missing

If `shelf_root` is omitted, auto-discovered via `detect.Candidates()`.

---

## Cross-Platform Shelf Discovery (`internal/detect`)

| Platform | External scan paths |
|---|---|
| macOS | `/Volumes/*/ModelShelf/models` |
| Linux | `/media/$USER/*/ModelShelf/models`, `/mnt/*/ModelShelf/models` |
| Windows | `D:\ModelShelf\models`, `E:\ModelShelf\models`, … (all drive letters) |
| All | `~/.cache/model-shelf/models` (internal fallback) |

Candidates sorted: existing external first, then new external, then internal.

---

## Drive Relocate (`internal/relocate`)

If `shelf_root` is set in config but doesn't exist:
- Check if it's under a platform volume root (`/Volumes/`, `/media/`, `/mnt/`, drive letter)
- Scan sibling volumes for the same subpath (`ModelShelf/models`)
- Return first match; otherwise return original path (downstream error surfaces it)

---

## Resolver (`internal/resolver`)

**Formats:** `gguf`, `mlx`, `safetensors`

**Shelf paths:**
```
gguf:         <root>/gguf/<publisher>/<repo>/<file>.gguf
mlx:          <root>/mlx/<publisher>/<repo>/
safetensors:  <root>/safetensors/<publisher>/<repo>/
```

**Format detection** from repo ID (heuristic, same logic as Python):
- `gguf` token in name → gguf
- `mlx` token or `mlx-community` org → mlx
- else → safetensors

**GGUF filename** construction: strip `-GGUF` suffix from repo name, append `-{quant}.gguf` (skip quant if already in name).

**Snapshot hit detection:** directory exists AND contains `config.json`.

---

## Error Handling & Exit Codes

| Condition | Exit code |
|---|---|
| Success | 0 |
| Model not found locally (--no-download) | 1 |
| Storage unavailable / not initialized | 2 |
| HF API / network error | 3 |
| Bad arguments | 1 |

All errors to stderr. Output to stdout. `--json` flag on `resolve` and `find` emits JSON.

---

## Output Format

### `resolve` (human)
```
  shelf  /Volumes/MyDrive/ModelShelf/models/gguf  miss
  fetch  huggingface.co/Qwen/Qwen3-14B-GGUF       downloaded

  status      downloaded
  source      huggingface
  format      gguf
  path        /Volumes/MyDrive/ModelShelf/models/gguf/Qwen/Qwen3-14B-GGUF/Qwen3-14B-Q4_K_M.gguf
```

### `find` (human)
```
  [gguf       ] Qwen/Qwen3-14B-GGUF                                      1,234,567 downloads
```

### `list` (human)
```
shelf  /Volumes/MyDrive/ModelShelf/models  (primary)

  gguf/
    Qwen/Qwen3-14B-GGUF/Qwen3-14B-Q4_K_M.gguf  (8.2 GB)

  mlx/
    (empty)
```

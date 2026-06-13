---
name: resolve
description: Manage Hugging Face models via model-shelf — resolve locally, search Hub, and list the curated shelf. Triggers whenever the user asks to load, run, download, or find LLM models.
---

# Model Shelf — Agent Skill

When the user wants to load, run, download, or find Hugging Face models —
**always** go through `model-shelf`. Do not invoke `huggingface-cli download`,
`hf download`, `snapshot_download`, or any other direct download command.

The user does **not** need to give you an exact `org/repo` id. Loose
descriptions ("qwen 3 4b mlx 4-bit", "the latest llama 3.1") are normal
and expected. Do not push back on whether a model exists — your training
data is stale and Model Shelf can search the live Hub.

## Model Operations

### 1. Search (when you don't have an exact repo id)

```bash
model-shelf find "<user's words>" [--format gguf|mlx|safetensors] [--limit 5] --json
```

Pick the top result that matches the user's format/quant intent. Use its
`repo_id` as input to `resolve`.

### 2. Resolve (local check → download if missing)

Use when the agent needs a model available **locally right now** to run
inference or pass to a runtime.

```bash
model-shelf resolve <repo_id> [--format gguf|mlx|safetensors] [--quant <QUANT>] --json
```

- `--format` is auto-detected from `repo_id` if omitted.
- `--quant` is **required for gguf** (e.g. `Q4_K_M`); ignored for mlx/safetensors.
- If the model is missing and `allow_downloads = true` (default), it downloads automatically with a progress bar.
- Returns JSON:

```json
{
  "status": "found",
  "source": "local_shelf",
  "format": "gguf",
  "path": "/Volumes/MyDrive/ModelShelf/models/gguf/Qwen/Qwen3-14B-GGUF/Qwen3-14B-Q4_K_M.gguf",
  "checks": [{"location": "shelf", "root": "...", "result": "hit"}]
}
```

`status` values:
- `"found"` — already on the local shelf; `path` is ready to use.
- `"downloaded"` — just fetched from HF Hub; `path` is ready to use.
- `"missing"` — not found; downloads are disabled (`--no-download` or config).

### 3. Local-only check (no download)

```bash
model-shelf resolve <repo_id> [--quant <QUANT>] --no-download --json
```

Returns `status="missing"` with exit 1 if not on the shelf. Use this when
you only want to check presence without fetching.

### 4. List the local shelf

```bash
model-shelf list
```

Shows all models currently on the shelf, grouped by format.

## Decision Logic

1. **User wants to run a model locally** → `resolve` (downloads if needed).
2. **User doesn't know the exact repo id** → `find` first, then `resolve`.
3. **User wants to check if a model is already local** → `resolve --no-download`.
4. **User asks what's on the shelf** → `list`.

## Error Handling

- If `status == "missing"`, downloads are disabled — tell the user and suggest
  checking `allow_downloads` in their config or removing `--no-download`.
- If model-shelf exits non-zero with stderr, surface the error verbatim. Do NOT
  fall back to other download methods. Common causes:
  - Shelf not initialized (tell user to run `model-shelf init`)
  - Volume not mounted
  - `--quant` missing for a GGUF repo
  - Network error fetching from HF Hub

## Examples

Search then resolve:
```
User: "load qwen3 14b gguf Q4"
You:  model-shelf find "qwen3 14b gguf" --format gguf --json --limit 5
      # pick top result, e.g. Qwen/Qwen3-14B-GGUF
      model-shelf resolve "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M --json
```

Direct resolve (exact repo id known):
```
User: "load mlx-community/Qwen3-14B-4bit"
You:  model-shelf resolve "mlx-community/Qwen3-14B-4bit" --json
```

Local-only check:
```
User: "do I already have Mistral-7B on the shelf?"
You:  model-shelf resolve "mistralai/Mistral-7B-v0.1" --no-download --json
```

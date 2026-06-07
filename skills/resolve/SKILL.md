---
name: resolve
description: Manage Hugging Face models via model-shelf — resolve locally, pull to mesh nodes, check inventory, and administer the mesh. Triggers whenever the user asks to load, run, download, place, or manage LLM models across machines.
---

# Model Shelf — Agent Skill

When the user wants to load, run, download, or manage Hugging Face models —
**always** go through `model-shelf`. Do not invoke `huggingface-cli download`,
`hf download`, `snapshot_download`, or any other direct download command.

The user does **not** need to give you an exact `org/repo` id. Loose
descriptions ("qwen 3 4b mlx 4-bit", "the latest llama 3.1") are normal
and expected. Do not push back on whether a model exists — your training
data is stale and Model Shelf can search the live Hub.

## Model Operations

### 1. Search (when you don't have an exact repo id)

```bash
model-shelf find "<user's words>" [--format gguf|mlx|safetensors] --json --limit 5
```

Pick the top result that matches the user's format/quant intent. Use its
`repo_id` as input to resolve or pull.

### 2. Resolve (local, synchronous — returns a path)

Use when the agent needs a model available **locally right now** to run
inference or pass to a runtime.

```bash
model-shelf resolve <repo_id> [--format gguf|mlx|safetensors] [--quant <QUANT>] --json
```

- `--format` is auto-detected from `repo_id` if omitted.
- `--quant` is **required for gguf**; ignored otherwise.
- Returns JSON with the following fields:
  - `status`: `"found"` (already local), `"downloaded"` (just fetched), `"missing"` (not found anywhere, exit 1), or `"missing_locally"` (exists on a mesh peer but not locally, exit 2).
  - `path`: absolute path to the model file (present when status is `found` or `downloaded`).
  - `mesh_available`: array of peer locations that have the model (present when `status == "missing_locally"`).

### 3. Pull (async, mesh-aware — places model on a node)

Use when the user wants a model available on a specific machine, or wants
model-shelf to pick the best Executor automatically.

```bash
# Auto-place on best Executor (smart placement by VRAM + disk)
model-shelf pull <repo_id> [--format F] [--quant Q] --json

# Explicit target
model-shelf pull <repo_id> [--format F] [--quant Q] --target <node> --json
```

- Fire-and-forget. Returns a job ID immediately.
- If the user specifies where, pass `--target`. Otherwise let model-shelf decide.
- If the model already exists on a peer node, model-shelf transfers from
  the peer instead of re-downloading from HF (peer-to-peer transfer).
- Progress is tracked via `model-shelf status <job_id> --json`.

### 4. Check status of in-flight operations

```bash
model-shelf status --json           # aggregate from all mesh nodes (default)
model-shelf status --mesh --json    # explicitly aggregate across all nodes
model-shelf status --local --json   # local node only
model-shelf status <job_id> --json
```

### 5. Inventory (what models are where)

```bash
model-shelf inventory --json
```

## Mesh Administration

Use these when the user asks to set up machines, manage the mesh, or
configure nodes.

### Initialize a new node / first node in mesh

```bash
# First node (bootstraps mesh, generates mesh key)
model-shelf init --role controller,store --shelf /path/to/models

# Subsequent nodes — init first, then join
model-shelf init --role store,executor --shelf /data/models
model-shelf join <peer_node>
```

`--shelf` is required — the user must specify where models are stored.
`--role` accepts: controller, store, executor (comma-separated, combinable).
`init` does not accept a `--join` flag; use `model-shelf join` as a separate step.

### Join an existing mesh (if init was done standalone)

```bash
model-shelf join <peer_node> [--key <mesh_key>]
```

### View nodes in the mesh

```bash
model-shelf nodes --json
```

Returns node health including GPU capabilities and disk metrics:
```json
[
  {"name": "gpu-box-1", "status": "online", "roles": ["executor","store"], "gpu": {"name": "NVIDIA A100", "vram_total_gb": 80, "vram_available_gb": 72}, "disk_total_gb": 1000, "disk_free_gb": 450},
  {"name": "nas-store", "status": "online", "roles": ["store"], "gpu": null, "disk_total_gb": 4000, "disk_free_gb": 2000}
]
```

Nodes without a GPU report `"gpu": null`.

### Change node roles

```bash
model-shelf role set store,executor
model-shelf role add executor
model-shelf role remove store
```

### Service management

```bash
model-shelf service install    # install and enable (auto-starts on login)
model-shelf service start
model-shelf service stop
model-shelf service restart    # stop + start (required after config changes)
model-shelf service uninstall
```

### Leave the mesh entirely

```bash
model-shelf leave
```

## Decision Logic for the Agent

1. **User wants to run a model locally** → use `resolve`.
2. **User wants a model on a specific machine** → use `pull --target`.
3. **User wants a model available for inference but doesn't specify where** → use `pull` (no target, model-shelf picks best Executor by VRAM + disk).
4. **User asks what's available** → use `inventory`.
5. **User asks about download/transfer progress** → use `status`.
6. **User wants to set up a new machine** → use `init` + `join`.
7. **User asks about the mesh / what machines are connected** → use `nodes`.
8. **User asks about GPU capacity or which nodes have GPUs** → use `nodes --json` and check `gpu` field.

## Error Handling

- If `status == "missing"` on resolve, downloads are disabled — surface to user.
- If model-shelf exits non-zero with stderr, surface the error verbatim. Do NOT
  fall back to other download methods. Common causes:
  - Volume not mounted
  - Shelf not initialized (tell user to run `model-shelf init`)
  - Node unreachable
  - No Executor with sufficient VRAM for the requested model

## Examples

Loose user input — search then pull:
```
User: "get qwen 3 14b gguf Q4 on the gx10"
You:  model-shelf find "qwen3 14b gguf" --format gguf --json --limit 5
      # pick top result, e.g. Qwen/Qwen3-14B-GGUF
      model-shelf pull "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M --target gx10 --json
```

Auto-placement:
```
User: "I need a small coding model available for inference"
You:  model-shelf find "coding model 7b gguf" --format gguf --json --limit 5
      # pick best match
      model-shelf pull "Qwen/Qwen2.5-Coder-7B-GGUF" --quant Q4_K_M --json
      # model-shelf picks the best Executor automatically
```

Local resolve:
```
User: "load Qwen/Qwen3-14B-GGUF with Q4_K_M"
You:  model-shelf resolve "Qwen/Qwen3-14B-GGUF" --quant Q4_K_M --json
```

Mesh admin:
```
User: "set up this machine as a storage node and join the mesh"
You:  model-shelf init --role store --shelf /data/models
      model-shelf join mini1
```

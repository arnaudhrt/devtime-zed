# devtime for Zed

Local-first coding time tracking for [Zed](https://zed.dev). Tracks your editor activity by writing heartbeat events to `~/.devtime/events-YYYY-MM.jsonl`.

Compatible with the [devtime CLI](https://github.com/arnaudhrt/devtime) and dashboard.

## How it works

```
Zed Editor                          devtime-ls (Go)
─────────────                       ───────────────
Opens file  → didOpen   ──┐
Edits file  → didChange ──┼── LSP (stdio) ──→  Dedup (30s window)
Saves file  → didSave   ──┘                    Write JSONL to ~/.devtime/
```

The extension is two parts:

1. **devtime-ls** — A Go language server that receives LSP events and writes heartbeats to disk
2. **Zed extension** — A Rust WASM shim that downloads and launches devtime-ls

## Output format

```jsonl
{"ts":"2026-03-17T14:30:00+07:00","event":"heartbeat","project":"myapp","lang":"go","editor":"zed"}
{"ts":"2026-03-17T14:32:15+07:00","event":"heartbeat","project":"myapp","lang":"typescript","editor":"zed"}
```

Events are appended to `~/.devtime/events-YYYY-MM.jsonl` (one file per month).

## Install

Search for **devtime** in Zed's extension registry (`zed: extensions`).

## Development

### Build devtime-ls locally

```sh
make build-local
```

### Cross-compile all targets

```sh
make release    # outputs to dist/
```

Targets: macOS (arm64, amd64), Linux (arm64, amd64), Windows (amd64).

### Test as dev extension

1. Build or download devtime-ls for your platform
2. Open this repo in Zed
3. Run `zed: install dev extension` and select this directory
4. Open any file — check `~/.devtime/` for heartbeat events

### Release

Tag a version to trigger the GitHub Actions release workflow:

```sh
git tag v0.1.0
git push origin v0.1.0
```

## License

[MIT](LICENSE)

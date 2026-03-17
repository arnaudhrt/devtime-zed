# zed-devtime — Zed Extension Plan

## Overview

A Zed extension that tracks coding time by writing editor events to `~/.devtime/events-YYYY-MM.jsonl`, matching the output format of the existing VS Code devtime extension.

Architecture follows the same two-part pattern as `zed-wakatime`:

1. **Zed Extension (Rust → WASM)** — thin shim that downloads and launches the language server
2. **devtime-ls (Go binary)** — standalone LSP that receives editor events and writes JSONL

---

## Architecture

```
┌─────────────────────────────────────┐
│              Zed Editor             │
│                                     │
│  Opens file → didOpen notification  │
│  Edits file → didChange             │
│  Saves file → didSave              │
└──────────────┬──────────────────────┘
               │ LSP (stdio)
               ▼
┌─────────────────────────────────────┐
│         devtime-ls (Go binary)      │
│                                     │
│  Receives LSP events                │
│  Dedupes within 30s window          │
│  Appends JSONL to ~/.devtime/       │
└─────────────────────────────────────┘
```

### Key difference from zed-wakatime

WakaTime's LS shells out to `wakatime-cli` on every heartbeat. devtime-ls writes directly to a local JSONL file — no external CLI needed. This makes the extension simpler: only one binary to download instead of two.

---

## JSONL Output Format

Must match existing VS Code devtime output exactly:

```jsonl
{"ts":"2025-03-16T14:30:00+07:00","event":"heartbeat","project":"wannee","lang":"go","editor":"zed"}
{"ts":"2025-03-16T14:32:15+07:00","event":"heartbeat","project":"wannee","lang":"typescript","editor":"zed"}
```

### Fields

| Field     | Type   | Description                                   |
| --------- | ------ | --------------------------------------------- |
| `ts`      | string | Local ISO 8601 with timezone offset           |
| `event`   | string | `"heartbeat"` (focus/blur inferred from gaps) |
| `project` | string | Workspace folder name                         |
| `lang`    | string | Language ID from LSP                          |
| `editor`  | string | Always `"zed"`                                |

### Note on focus/blur

The VS Code extension emits explicit `focus` and `blur` events via `onDidChangeWindowState`. Zed's LSP protocol has no equivalent. Instead:

- Every LSP event maps to a `"heartbeat"` event
- The devtime CLI/dashboard infers focus/blur from gaps in heartbeat activity (e.g., >2 min gap = blur)

---

## Part 1: devtime-ls (Go Language Server)

### Why Go

- devtime CLI is already written in Go
- Could potentially live in the same repo / be a subcommand of `devtime`
- Go cross-compiles easily to all Zed-supported platforms

### Dependencies

- `github.com/tliron/glsp` — lightweight Go LSP framework (alternative: `go.lsp.dev/protocol`)
- Standard library only for file I/O and JSON

### LSP Capabilities to Declare

During `initialize`, declare exactly what zed-wakatime does:

```go
capabilities := protocol.ServerCapabilities{
    TextDocumentSync: &protocol.TextDocumentSyncOptions{
        OpenClose: boolPtr(true),
        Change:    protocol.TextDocumentSyncKindIncremental,
        Save: &protocol.SaveOptions{
            IncludeText: boolPtr(false),
        },
    },
}
```

### Event Handlers

#### didOpen → heartbeat

```go
func (s *Server) DidOpen(params *protocol.DidOpenTextDocumentParams) {
    s.sendHeartbeat(Event{
        URI:      params.TextDocument.URI,
        Language: params.TextDocument.LanguageID,
    })
}
```

#### didChange → heartbeat (deduped)

```go
func (s *Server) DidChange(params *protocol.DidChangeTextDocumentParams) {
    s.sendHeartbeat(Event{
        URI: params.TextDocument.URI,
        // language not available in didChange, use cached value from didOpen
    })
}
```

#### didSave → heartbeat (always fires, bypasses dedup)

```go
func (s *Server) DidSave(params *protocol.DidSaveTextDocumentParams) {
    s.sendHeartbeat(Event{
        URI:     params.TextDocument.URI,
        IsWrite: true,
    })
}
```

### Dedup / Heartbeat Logic

Mirrors the VS Code extension's 30s interval and zed-wakatime's file-change check:

```go
const heartbeatInterval = 30 * time.Second

type state struct {
    mu       sync.Mutex
    lastURI  string
    lastTime time.Time
}

func (s *Server) sendHeartbeat(event Event) {
    s.state.mu.Lock()
    defer s.state.mu.Unlock()

    now := time.Now()

    // Skip if same file, within interval, and not a save
    if event.URI == s.state.lastURI &&
        now.Sub(s.state.lastTime) < heartbeatInterval &&
        !event.IsWrite {
        return
    }

    s.writeEvent(now, event)
    s.state.lastURI = event.URI
    s.state.lastTime = now
}
```

### File Writing

```go
func (s *Server) writeEvent(now time.Time, event Event) {
    dir := filepath.Join(os.Getenv("HOME"), ".devtime")
    os.MkdirAll(dir, 0755)

    filename := fmt.Sprintf("events-%s.jsonl", now.Format("2006-01"))
    path := filepath.Join(dir, filename)

    lang := event.Language
    if lang == "" {
        lang = s.cachedLanguage(event.URI)
    }

    entry := map[string]string{
        "ts":      localISOString(now),
        "event":   "heartbeat",
        "project": s.projectName,
        "lang":    normalizeLang(lang),
        "editor":  "zed",
    }

    data, _ := json.Marshal(entry)
    f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        return
    }
    defer f.Close()
    f.Write(append(data, '\n'))
}
```

### Language Normalization

Port from VS Code extension:

```go
var langNormalize = map[string]string{
    "go.mod":  "go",
    "go.sum":  "go",
    "go.work": "go",
    "go.asm":  "go",
    "gotmpl":  "go",
}

func normalizeLang(id string) string {
    if n, ok := langNormalize[id]; ok {
        return n
    }
    return id
}
```

Note: Zed sends different language IDs than VS Code. Map accordingly — e.g., Zed sends `"Go"` (capitalized) vs VS Code's `"go"`. The LS should lowercase or normalize.

### Timestamp Format

Must match the VS Code extension's `localISOString()`:

```go
func localISOString(t time.Time) string {
    return t.Format("2006-01-02T15:04:05-07:00")
}
```

### CLI Args

The extension will launch devtime-ls with:

```
devtime-ls --project-folder /path/to/project
```

The LS extracts the project name from the folder path (last segment), same as the VS Code extension uses `workspaceFolders[0].name`.

### Cross-compilation Targets

Build for all Zed-supported platforms:

| OS      | Arch  | Output                                  |
| ------- | ----- | --------------------------------------- |
| macOS   | arm64 | `devtime-ls-aarch64-apple-darwin`       |
| macOS   | amd64 | `devtime-ls-x86_64-apple-darwin`        |
| Linux   | arm64 | `devtime-ls-aarch64-unknown-linux-gnu`  |
| Linux   | amd64 | `devtime-ls-x86_64-unknown-linux-gnu`   |
| Windows | amd64 | `devtime-ls-x86_64-pc-windows-msvc.exe` |

Zip each into `{target}.zip` and attach to a GitHub release.

---

## Part 2: Zed Extension (Rust WASM Shim)

### File Structure

```
zed-devtime/
├── extension.toml
├── Cargo.toml
├── LICENSE
├── src/
│   └── lib.rs
```

Note: unlike zed-wakatime, the LS lives in a separate repo (or the main devtime repo). The extension only downloads the pre-built binary.

### extension.toml

```toml
id = "devtime"
name = "devtime"
description = "Local-first coding time tracking."
version = "0.0.1"
schema_version = 1
authors = ["Arnaud <your@email.com>"]
repository = "https://github.com/youruser/zed-devtime"

[language_servers.devtime]
name = "devtime"
languages = [
    # Same massive list as wakatime — register for every language
    # so the LS activates regardless of what file is open.
    "C",
    "C++",
    "CSS",
    "Dart",
    "Go",
    "Go Mod",
    "Go Work",
    "HTML",
    "JavaScript",
    "JSON",
    "JSONC",
    "Lua",
    "Markdown",
    "Python",
    "Rust",
    "Shell Script",
    "Swift",
    "TOML",
    "TSX",
    "TypeScript",
    "YAML",
    # ... add the full list from wakatime's extension.toml
]
```

### Cargo.toml

```toml
[package]
name = "zed-devtime"
version = "0.0.1"
edition = "2021"

[dependencies]
zed_extension_api = "0.7.0"

[lib]
name = "zed_devtime"
crate-type = ["cdylib"]
```

### src/lib.rs

Simplified from zed-wakatime (no wakatime-cli download needed):

```rust
use std::{fs, path::PathBuf};
use zed_extension_api::{self as zed, Command, LanguageServerId, Result, Worktree};

struct DevtimeExtension {
    cached_binary_path: Option<PathBuf>,
}

impl DevtimeExtension {
    fn target_triple(&self) -> Result<String, String> {
        let (platform, arch) = zed::current_platform();
        let arch = match arch {
            zed::Architecture::Aarch64 => "aarch64",
            zed::Architecture::X8664 => "x86_64",
            _ => return Err(format!("unsupported architecture: {arch:?}")),
        };
        let os = match platform {
            zed::Os::Mac => "apple-darwin",
            zed::Os::Linux => "unknown-linux-gnu",
            zed::Os::Windows => "pc-windows-msvc",
            _ => return Err("unsupported platform".to_string()),
        };
        Ok(format!("devtime-ls-{arch}-{os}"))
    }

    fn binary_path(&mut self, id: &LanguageServerId) -> Result<PathBuf> {
        if let Some(path) = &self.cached_binary_path {
            if fs::metadata(path).is_ok_and(|s| s.is_file()) {
                return Ok(path.clone());
            }
        }

        zed::set_language_server_installation_status(
            id,
            &zed::LanguageServerInstallationStatus::CheckingForUpdate,
        );

        let release = zed::latest_github_release(
            "youruser/devtime",  // or wherever releases are hosted
            zed::GithubReleaseOptions {
                require_assets: true,
                pre_release: false,
            },
        )?;

        let triple = self.target_triple()?;
        let asset_name = format!("{triple}.zip");
        let asset = release
            .assets
            .iter()
            .find(|a| a.name == asset_name)
            .ok_or_else(|| format!("no asset found: {asset_name}"))?;

        let version_dir = format!("devtime-ls-{}", release.version);
        let binary_name = match zed::current_platform() {
            (zed::Os::Windows, _) => "devtime-ls.exe",
            _ => "devtime-ls",
        };
        let binary_path = std::path::Path::new(&version_dir).join(binary_name);

        if !fs::metadata(&binary_path).is_ok_and(|s| s.is_file()) {
            zed::set_language_server_installation_status(
                id,
                &zed::LanguageServerInstallationStatus::Downloading,
            );
            zed::download_file(
                &asset.download_url,
                &version_dir,
                zed::DownloadedFileType::Zip,
            )
            .map_err(|e| format!("download failed: {e}"))?;

            // Clean old versions
            if let Ok(entries) = fs::read_dir(".") {
                for entry in entries.flatten() {
                    if let Some(name) = entry.file_name().to_str() {
                        if name.starts_with("devtime-ls-") && name != version_dir {
                            fs::remove_dir_all(entry.path()).ok();
                        }
                    }
                }
            }
        }

        zed::make_file_executable(binary_path.to_str().unwrap())?;
        self.cached_binary_path = Some(binary_path.clone());
        Ok(binary_path)
    }
}

impl zed::Extension for DevtimeExtension {
    fn new() -> Self {
        Self { cached_binary_path: None }
    }

    fn language_server_command(
        &mut self,
        id: &LanguageServerId,
        worktree: &Worktree,
    ) -> Result<Command> {
        let binary = self.binary_path(id)?;
        let project_folder = worktree.root_path();

        let mut args = vec![];
        if !project_folder.is_empty() {
            args.push("--project-folder".to_string());
            args.push(project_folder.to_string());
        }

        Ok(Command {
            command: binary.to_str().unwrap().to_owned(),
            args,
            env: worktree.shell_env(),
        })
    }
}

zed::register_extension!(DevtimeExtension);
```

---

## Build & Release Pipeline

### devtime-ls (Go)

```makefile
PLATFORMS = darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/amd64

release:
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; \
		arch=$${platform#*/}; \
		triple=$$(./target-triple.sh $$os $$arch); \
		output="devtime-ls"; \
		if [ "$$os" = "windows" ]; then output="devtime-ls.exe"; fi; \
		GOOS=$$os GOARCH=$$arch go build -o $$output ./cmd/devtime-ls; \
		zip "$${triple}.zip" $$output; \
		rm $$output; \
	done
```

Attach all `.zip` files to a GitHub release tagged `vX.Y.Z`.

### Zed Extension

1. Push extension code to `github.com/youruser/zed-devtime`
2. Fork `zed-industries/extensions`
3. Add submodule: `git submodule add https://github.com/youruser/zed-devtime extensions/devtime`
4. Add to `extensions.toml`:
   ```toml
   [devtime]
   submodule = "extensions/devtime"
   version = "0.0.1"
   ```
5. Open PR, get merged → published to Zed extension registry

---

## Implementation Order

### Phase 1: devtime-ls (Go)

1. [ ] Scaffold Go project with LSP server (glsp or go.lsp.dev)
2. [ ] Implement `initialize` with TextDocumentSync capabilities
3. [ ] Implement `didOpen` / `didChange` / `didSave` handlers
4. [ ] Add heartbeat dedup logic (30s interval)
5. [ ] Add JSONL file writer matching devtime format
6. [ ] Add language normalization (Zed language IDs → devtime format)
7. [ ] Add `--project-folder` CLI flag
8. [ ] Test locally: `echo '...' | devtime-ls --project-folder /tmp/test`
9. [ ] Cross-compile and create GitHub release with zip assets

### Phase 2: Zed Extension (Rust)

1. [ ] Create extension scaffold with `extension.toml` + `Cargo.toml`
2. [ ] Implement `language_server_command` to download and launch devtime-ls
3. [ ] Register for all languages in `extension.toml`
4. [ ] Test as dev extension: `zed: install dev extension`
5. [ ] Verify JSONL output in `~/.devtime/`

### Phase 3: Publish

1. [ ] Add LICENSE (MIT/BSD)
2. [ ] Submit PR to zed-industries/extensions
3. [ ] Set up GitHub Actions for automated cross-compilation on tag

---

## Open Questions

- **Language ID mapping**: Zed sends capitalized language names (`"Go"`, `"Rust"`, `"TypeScript"`). VS Code sends lowercase (`"go"`, `"rust"`, `"typescript"`). Should devtime-ls lowercase everything for consistency, or store as-is?
- **devtime-ls location**: Separate repo, or a subcommand of the main devtime CLI (`devtime ls`)? Subcommand would mean a single binary but a larger download.
- **Multiple workspaces**: If someone opens multiple Zed windows with different projects, Zed spawns a separate LS instance per workspace. Each will write independently, which should be fine since they append to the same monthly file. Worth confirming no file locking issues.
- **WakaTime import**: If you're also importing WakaTime data into devtime, having both extensions running simultaneously means double-counting. Consider a config option or dedup in the dashboard.

# AGENTS.md

## Build & Run

```bash
go build ./cmd/shim                    # default: Linux
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o shim.exe ./cmd/shim   # Windows
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o shim-linux ./cmd/shim   # Linux from Windows

# Diagnostics
go build ./cmd/joyprobe && ./joyprobe          # axis/button probe
go build ./cmd/joyprobe-raw && ./joyprobe-raw   # /dev/input/js* (Linux only)
go build ./cmd/viscaprobe && ./viscaprobe       # camera VISCA probe
go build ./cmd/deckprobe && ./deckprobe         # Stream Deck probe
```

## Run (most common flags)

```bash
./shim --vmix 10.2.2.195                      # multi-camera + vMix tally/discovery
./shim --camera 10.2.2.212                    # single camera, no vMix
./shim --vmix 10.2.2.195 --streamdeck         # enable PreSonus Stream Deck XL input
./shim --config configs/xbox.json --vmix ...   # custom controller
./shim --focus-range 200                      # narrower focus window (default 300)
./shim --fade-ms 500                          # shorter fade transitions (default 1000)
./shim --dump-config                          # print default JSON and exit
```

## Architecture (how to read the code)

```
cmd/shim/main.go          # entry point, CLI parsing, bubbletea TUI, main control loop
internal/router/router.go # preview/program camera tracking, override logic
internal/visca/*.go       # VISCA TCP connections + all camera commands
internal/vmix/*.go        # HTTP discovery + TCP tally/commands
internal/input/joystick*.go # platform-specific joystick read
internal/streamdeck/*     # Stream Deck USB HID input + LCD feedback
internal/config/config.go # controller JSON config loading
internal/proclaim/*       # Proclaim slide control
```

**Concurrency model**: tally goroutine + joystick goroutine(s) + Stream Deck goroutine send on channels. The main goroutine (bubbletea TUI loop) owns all VISCA connections and the vMix commander. No mutexes — channel-based.

## Controller config

- JSON files live in `configs/`. Default is built-in Logitech Extreme 3D Pro.
- Override with `--config path/to/controller.json`.
- The Windows-verified config in `controller.json` differs from the built-in: on Windows the Extreme 3D Pro maps axes differently (focus=index 2, zoom=index 3). Use `--dump-config` to see defaults, then `go build ./cmd/joyprobe && ./joyprobe` to discover real axis indices.
- `configs/xbox.json` and `configs/extreme3dpro.json` are community-contributed.

## Focus system (know this before editing focus logic)

- Throttle slider has ~256 positions. Mapping to the full 4352-position camera focus range is too coarse.
- Instead: slider controls a ±N window (default 300) around an **anchor**. Anchor is auto-set on startup, AF release, and preset recall (with 2s settling delay).
- Focus queries to the camera only run when all VISCA speeds are zero (camera idle). See `internal/visca/connection.go` for the idle-check logic.
- Focus mode must be set (manual `...38 03...`) before sending absolute focus positions.

## Protocol quirks

- VISCA over IP: TCP 5678, UDP 1259. Network commands still use `81` prefix, address bits ignored per camera IP.
- Focus range probed: `0x0080` to `0x1180` usable. Hard clamp above `0x1200`.
- vMix TCP API uses spaces between function and params (`FUNCTION Cut`), not `&` (that's the HTTP API).
- Stream Deck protocol: USB HID, requires `usbhid` Go module. No CGO needed.

## Files that reference more detail

- `CLAUDE.md` — full VISCA command table, hardware specs, camera capabilities
- `STREAMDECK.md` — Stream Deck XL button layout reference
- `docs/camera_capabilities.md` — camera feature probe results
- `probe_pygame.py` — Python axis-resolution probe; superseded by the Go shim.

## Oh-My-OpenCode skillset

[Oh-My-OpenAgent](https://github.com/code-yeongyu/oh-my-openagent) is an OpenCode plugin (`npm i oh-my-opencode`) that adds multi-model orchestration. Install it to get:

- **Discipline agents** — Sisyphus (orchestrator, auto-delegates to Hephaestus/Oracle/Librarian/Explore)
- **Hash-anchored edits** — `LINE#ID` content hashes validate changes before applying; eliminates stale-line errors
- **LSP + AST-Grep** — IDE-grade rename, diagnostics, cross-references, AST-aware rewrites
- **Background agents** — parallel specialists, context stays lean
- **Built-in MCPs** — web search, docs, GitHub code search
- **Skills** — playwright (browser), git-master (atomic commits/rebase), frontend-ui-ux
- **`/init-deep`** — auto-generates hierarchical `AGENTS.md` files

Config files: `.opencode/oh-my-opencode.json[c]` or user-level `~/.config/opencode/oh-my-opencode.json[c]`.

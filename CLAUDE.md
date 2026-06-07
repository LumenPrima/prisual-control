# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Multi-camera PTZ shim in Go: maps a controller (default Logitech Extreme 3D Pro joystick; configurable) to Prisual TEN-20N Pro NDI HX3 PTZ cameras via VISCA over IP, with vMix integration for tally, transitions, and camera discovery. Built for live streaming and video production.

It also drives:
- An **Elgato Stream Deck XL** as a primary control surface (input switching, transitions, presets, PTZ d-pad, focus). See `STREAMDECK.md`.
- A **Python auto-tracking pipeline** (`tracker/track.py`) that does YOLO-pose person tracking and sends aim points over UDP; the shim runs a PD pan/tilt controller to keep the subject framed.
- **Proclaim** (Faithlife Proclaim presentation software) for next/previous slide control.

vMix only supports jog-style focus commands (near/far/stop) — no absolute positioning. This project bypasses vMix to talk directly to the cameras via VISCA TCP.

> **Camera IPs**: CLAUDE.md examples below use the historical `10.2.2.x` addresses. The live cameras are now at `192.168.1.50` / `192.168.1.51` — substitute as needed.

## Build & Run

```bash
# Build (Linux/Windows: no CGO, macOS: needs Xcode CLI tools)
go build ./cmd/shim

# Run
./shim --vmix 10.2.2.195                       # multi-camera with vMix discovery + tally
./shim --camera 10.2.2.212                     # single-camera fallback (no vMix)
./shim --vmix 10.2.2.195 --fade-ms 500         # faster fades
./shim --vmix 10.2.2.195 --streamdeck          # enable Stream Deck XL
./shim --config controller.json --vmix ...     # custom controller mapping
./shim --dump-config                           # print default controller JSON and exit
./shim --vmix ... --proclaim 10.2.2.50         # enable Proclaim slide control
./shim --vmix ... --tracker-port 9001          # enable auto-track UDP listener (PD controller)

# Cross-compile (no toolchain needed for Linux/Windows)
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o shim.exe ./cmd/shim
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o shim-linux ./cmd/shim

# Diagnostic tools
go build -o joyprobe ./cmd/joyprobe         # axis/button resolution probe (native input)
go build -o joyprobe-raw ./cmd/joyprobe-raw # raw /dev/input/js* probe (Linux only)
go build -o viscaprobe ./cmd/viscaprobe     # VISCA camera probe (focus range, inquiries)
go build -o deckprobe ./cmd/deckprobe       # Stream Deck HID probe

# Python auto-tracker (separate process; needs CUDA torch — see tracker/requirements.txt)
python tracker/track.py --src rtsp://192.168.1.50/1 --cam 192.168.1.50 --track-out 127.0.0.1:9001
```

### CLI flags
| Flag | Default | Purpose |
|---|---|---|
| `--vmix` | "" | vMix host IP (enables discovery + tally) |
| `--camera` | "" | single camera IP (fallback if no vMix) |
| `--visca-port` | 5678 | VISCA TCP port |
| `--fade-ms` | 1000 | fade transition duration (ms) |
| `--config` | "" | controller config JSON (default: built-in Extreme 3D Pro) |
| `--dump-config` | false | print default controller JSON and exit |
| `--streamdeck` | false | enable Stream Deck support |
| `--proclaim` | "" | Proclaim host IP (enables slide control) |
| `--proclaim-pass` | "proclaim" | Proclaim network password |
| `--tracker-port` | 0 | UDP port for auto-tracker packets (0 disables) |
| `--tracker-kp` | 10.0 | proportional gain for auto-track pan/tilt |
| `--tracker-kd` | 1.5 | derivative gain (damps overshoot; 0 disables) |
| `--tracker-deadband` | 0.08 | normalized error below which auto-track stops |
| `--tracker-stale-ms` | 500 | auto-track stops if no fresh error within this window |

> Note: `--focus-range` no longer exists. The anchor-relative absolute-focus system was replaced by jog-based focus (see Key Design Decisions).

## Architecture

```
                    ┌────────────────────┐      ┌──────────────────┐
                    │  vMix (10.2.2.195) │      │ Proclaim         │
                    │  HTTP :8088 (XML)  │      │ HTTP :52195      │
                    │  TCP  :8099 (tally │      │ (slide control)  │
                    │         + commands)│      └────────▲─────────┘
                    └────┬──────────┬────┘               │
                  discovery      tally goroutine     next/prev slide
                  (startup)      (channel → router)       │
                         │              │                 │
┌──────────┐      ┌──────▼──────────────▼─────────────────┴──┐      ┌──────────────────┐
│ Joystick │─────▶│              main loop                   │─────▶│ visca.Camera A   │
│ (native) │      │                                          │─────▶│ visca.Camera B   │
├──────────┤      │  CameraRouter (preview/program)          │─────▶│ visca.Camera C…  │
│ Stream   │─────▶│  Bubbletea TUI                           │◀─────│ (per-cam worker, │
│ Deck XL  │◀─────│  PD auto-track controller                │ reply │  reconnecting,   │
│ (usbhid) │      │  Framing editor                          │ chan  │  VISCA TCP)      │
├──────────┤      └──────────────────▲───────────────────────┘      └──────────────────┘
│ Python   │  UDP aim points (JSON)  │
│ tracker  │─────────────────────────┘
│ (YOLO)   │   internal/tracker.Listen
└──────────┘
```

**Concurrency**: tally + joystick + Stream Deck + tracker-UDP goroutines send on channels; the main goroutine (bubbletea) owns the routing/UI state and the vMix commander. Each camera has its own `visca.Camera` worker goroutine that serializes all I/O to that camera and auto-reconnects — a wedged camera blocks only its own worker, not the UI. Inquiry results flow back on a shared `replies` channel; Stream Deck LCD feedback flows back on its own channel. No mutexes in the control loop.

## File Structure

```
cmd/shim/main.go              # Entry point, CLI flags, bubbletea TUI, control loop, auto-track + framing
cmd/joyprobe/main.go          # Joystick axis/button resolution diagnostic tool
cmd/joyprobe-raw/main.go      # Raw /dev/input/js* diagnostic (Linux only)
cmd/viscaprobe/main.go        # VISCA camera probe (focus range sweep, inquiries)
cmd/deckprobe/main.go         # Stream Deck HID key/dial probe
internal/visca/connection.go   # VISCA TCP connection (non-blocking drain, fire-and-forget)
internal/visca/camera.go       # Per-camera worker goroutine: serializes I/O, auto-reconnects, Sender iface
internal/visca/commands.go     # All VISCA commands (focus, zoom, pan/tilt, presets, shutter, gain, tally, inquiries)
internal/vmix/discovery.go     # HTTP API camera discovery (parse XML, extract IPs)
internal/vmix/tally.go         # TCP tally subscription (goroutine, auto-reconnect)
internal/vmix/commands.go      # TCP command sender (cut, fade, preview, transitions)
internal/input/joystick.go     # Platform-independent types and math (JoystickState, AxisToSpeed, etc.)
internal/input/joystick_linux.go   # Linux: reads /dev/input/js* directly
internal/input/joystick_windows.go # Windows: winmm.dll joyGetPosEx syscall
internal/input/joystick_darwin.go  # macOS: IOKit HID framework via cgo
internal/input/ioctl_linux.go      # Linux ioctl helper for joystick name
internal/config/config.go     # Controller config (axis/button → action mapping); JSON load/save, defaults
internal/streamdeck/streamdeck.go  # Stream Deck USB HID input + LCD feedback (pure Go, no CGO)
internal/streamdeck/font.go        # 5x7 bitmap font renderer for Stream Deck LCD keys
internal/proclaim/proclaim.go # Proclaim HTTP client (:52195) — auth + next/prev slide
internal/tracker/listener.go  # UDP listener: parses auto-tracker aim-point packets into Event channel
internal/router/router.go     # Camera routing (preview/program tracking, override)
tracker/track.py              # Python YOLO-pose tracker: per-track aim point, ReID, UDP aim-point output
configs/*.json                # Community controller configs (extreme3dpro, xbox)
```

## Camera Control Protocols

### VISCA over IP (primary — supports absolute focus)
- TCP port 5678, UDP port 1259
- All VISCA commands confirmed working over both serial and network (version PS-20211007)
- When sending over network, address bits are ignored (each camera has a unique IP), but commands still use `81` prefix

**Focus commands (all confirmed OK):**
| Function | Command | Notes |
|---|---|---|
| Manual Focus mode | `81 01 04 38 03 FF` | **Must set before direct focus** |
| Auto Focus mode | `81 01 04 38 02 FF` | |
| Focus Direct (absolute) | `81 01 04 48 0p 0q 0r 0s FF` | pqrs = focus position |
| Focus Inquiry | `81 09 04 48 FF` | Returns `y0 50 0p 0q 0r 0s FF` |
| Pan/Tilt Inquiry | `81 09 06 12 FF` | Returns pan (4 nibbles) + tilt (4 nibbles) |
| Focus Far (variable) | `81 01 04 08 2p FF` | p = speed 0-7 |
| Focus Near (variable) | `81 01 04 08 3p FF` | p = speed 0-7 |
| Focus Stop | `81 01 04 08 00 FF` | |

**Other commands used by the shim** (see `internal/visca/commands.go`; inquiries are exposed per-camera in `camera.go`):
- Zoom Direct / Zoom Variable, Pan/Tilt Variable, Preset Save / Recall
- Shutter Direct + Inquiry, Gain Direct + Inquiry
- Zoom Inquiry, Focus Mode Inquiry, Pan/Tilt Inquiry
- The shim round-robins idle-time inquiries (pan/tilt → zoom → shutter → gain → focus mode), one per second, only while the active camera is stopped.

**Confirmed focus range (probed on 10.2.2.212):**
- Usable range: `0x0080` to `0x1180` (4352 positions)
- Hard clamp at `0x11E4` — anything above `0x1200` is ignored
- Motor speed: ~470 positions/sec — full traverse takes ~9.5s

### vMix TCP API (port 8099)
- Text-based: `FUNCTION FunctionName Param=Value\r\n`
- `FUNCTION Cut` — instant cut to preview
- `FUNCTION Fade Duration=1000` — fade transition
- `FUNCTION PreviewInput Input=N` — set preview
- `SUBSCRIBE TALLY\r\n` — persistent tally subscription
- Note: TCP API uses **spaces** between function and parameters (not `&` — that's the HTTP API)

### CGI/HTTP API (no absolute focus)
- `http://<camera-ip>/cgi-bin/ptzctrl.cgi?ptzcmd&...`
- Focus only has jog commands: `focusin`, `focusout`, `focusstop`

## Key Design Decisions

### Jog-based focus (anchor-relative focus removed)
Earlier versions mapped the throttle slider to a **±N absolute-focus window** around an auto-set anchor (`FocusDirect`, `--focus-range`). That system has been removed. Focus is now:
- **Hold-for-AF**: trigger button (`af_hold`) runs autofocus while held, returns to manual focus on release. The `af_latch` button latches AF on.
- **Manual jog**: Stream Deck FOCUS FAR / FOCUS NEAR keys jog focus near/far while held (`FocusFar`/`FocusNear`/`FocusStop`), at `maxFocusSpeed`. Jogging cancels latched AF.
- **Zoom auto-AF**: starting a zoom (joystick or deck) flips to AF to prevent drift, then restores manual focus when the zoom stops.

`internal/visca/commands.go` still implements `FocusDirect` and the absolute-focus inquiries — they are used by `viscaprobe`, not the shim control loop.

### Per-camera worker goroutines (`visca.Camera`)
Each camera is a `visca.Camera` wrapping a `Connection` in its own goroutine (`internal/visca/camera.go`). The main loop calls `Send` (fire-and-forget, queued) and `Inquire*` (results posted to a shared `replies` channel). A wedged or offline camera blocks only its own worker; the worker counts I/O failures and rebuilds the connection after `failuresBeforeReconnect`, rate-limited by `minReconnectInterval`. Both `*Connection` and `*Camera` satisfy the `Sender` interface, so command helpers work with either.

### Auto-tracking (PD controller)
The Python tracker (`tracker/track.py`, YOLO11m-pose) emits per-track aim points in normalized frame coords over UDP. `internal/tracker.Listen` parses them into `Event`s. The shim keeps a per-camera framing target (per aim mode: shoulders/nose/torso) and computes error = aim − target, then runs a PD controller (`trackerSpeed`, gains `--tracker-kp`/`--tracker-kd`, `--tracker-deadband`) to drive pan/tilt, with zoom-adaptive speed scaling. Auto-track is toggled per role from the TUI: `t` = current **program** cam, `p` = current **preview** cam — so a cut to a pre-armed cam lands already framed. Auto-tracked cams bypass `switchMute`; tracking stops if no fresh sample arrives within `--tracker-stale-ms`.

### Controller config (JSON-driven mapping)
Axis/button → action mapping lives in `internal/config`. The built-in default targets the Logitech Extreme 3D Pro; `--config` loads a JSON override (missing fields fall back to defaults), `--dump-config` prints the default. The in-TUI **mapping mode** (`m` key) walks the operator through each axis/button and saves the result (default `controller.json`). Note: axis indices differ across platforms — on Windows the Extreme 3D Pro reports focus/zoom on different indices than Linux, so use `joyprobe` to discover real indices.

### Native joystick input (no SDL2)
SDL2 was removed because it requires CGO, complicating cross-compilation and adding a shared library dependency. Platform-specific implementations read joystick hardware directly:
- Linux: `/dev/input/js*` (8-byte event structs)
- Windows: `winmm.dll` `joyGetPosEx` (syscall, no DLL to bundle)
- macOS: IOKit HID framework (cgo with system framework only)

### Non-blocking VISCA
VISCA connections drain pending responses before each send (fire-and-forget). Inquiries use `SendRecv` with a 50ms read delay. Position queries only run when the camera is idle (all speeds zero) to avoid blocking during active control.

## Hardware Notes

### Logitech Extreme 3D Pro
- 4 axes: X (stick LR), Y (stick FB), twist (Rz), throttle slider
- 12 buttons, 1 POV hat
- **8-bit throttle resolution** (~256 discrete positions, NOT 4096 as previously documented)
- Stick X/Y: 10-bit (~1024 positions). Twist: 8-bit (~256 positions)
- Zero jitter at rest — ADC is solid
- Expo curve 2.5 on spring-return axes for fine control

**Default button mapping** (built-in config; indices are config-driven via `internal/config`, not hardcoded SDL constants — these are the default-config values):
| Button | Index | Action (config key) |
|---|---|---|
| Trigger | 0 | Hold for AF, release for MF (`af_hold`) |
| Thumb | 1 | Preset save modifier / AF latch (`af_latch`, `preset_save`) |
| Top-left | 2 | Hold for program camera override (`program_override`) |
| Top | 3 | Fade transition — vMix (`fade`) |
| Top | 4 | Cut transition — vMix (`cut`) |
| Top | 5 | Cycle preview to next input (`cycle_preview`) |
| Base 7-12 | 6-11 | Camera presets 1-6 (`presets`) |

POV hat: up/down = shutter ±, left/right = gain ±.

**TUI keys** (keyboard, in the bubbletea UI):
| Key | Action |
|---|---|
| `q` / `ctrl+c` | quit (stops all motion) |
| `m` | enter controller mapping mode |
| `t` | toggle auto-track on current **program** cam |
| `p` | toggle auto-track on current **preview** cam |
| `f` / `F` | open framing editor (requires `--tracker-port`) — arrows nudge target, Tab cycles aim mode, R resets |

### Elgato Stream Deck XL
- 32-key USB HID surface, pure-Go driver (`internal/streamdeck`, no CGO). LCD keys rendered with a 5x7 bitmap font.
- Layout and behavior documented in `STREAMDECK.md`: input/preview select with tally colors, CUT/FADE, preset recall + save (SET), PTZ d-pad (zoom-adaptive speed), FOCUS hold-for-AF + FAR/NEAR jog, DRIVE LIVE toggle, and (with `--proclaim`) slide next/prev.

### PreSonus ATOM
- 4 knobs: CC 14-17, **7-bit absolute only (0-127)**, infinite encoder but absolute CC — unusable at extremes
- Not currently used by the Go shim (Python POC only)

## Camera Details

- Model: Prisual TEN-20N Pro (NDI HX3)
- OEM/rebranded Chinese PTZ platform
- VISCA command version: PS-20211007
- vMix discovers NDI cameras with IP in title: `NDI HD CAMERA (NDI HX2,10.2.2.212)`

## Reference Documents

- `AGENTS.md` — condensed build/run/architecture quick reference (overlaps this file)
- `README.md` — user-facing setup and controls
- `STREAMDECK.md` — Stream Deck XL button layout and behavior
- `docs/camera_capabilities.md` — camera feature probe results (streaming, NDI, firmware)
- `tracker/requirements.txt` — Python tracker deps (CUDA torch, ultralytics, boxmot, cyndilib)
- `docs/HTTPCGIList.pdf` — Official Prisual HTTP-CGI control sheet
- `docs/Visca_Command_V2.1.pdf` — Full VISCA command set
- `docs/Prisual_4K_and_1080P_series_camera_visca_commands_new(Visca-control command).csv` — VISCA command spreadsheet with test results

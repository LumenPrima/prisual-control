# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Multi-camera PTZ shim in Go: maps a Logitech Extreme 3D Pro joystick to Prisual TEN-20N Pro NDI HX3 PTZ cameras via VISCA over IP, with vMix integration for tally, transitions, and camera discovery. Used in a church live streaming setup.

vMix only supports jog-style focus commands (near/far/stop) — no absolute positioning. This project bypasses vMix to talk directly to the cameras via VISCA TCP.

## Build & Run

```bash
# Build (Linux/Windows: no CGO, macOS: needs Xcode CLI tools)
go build ./cmd/shim

# Run
./shim --vmix 10.2.2.195                    # multi-camera with vMix discovery
./shim --camera 10.2.2.212                  # single-camera fallback
./shim --vmix 10.2.2.195 --focus-range 200  # narrower focus window
./shim --vmix 10.2.2.195 --fade-ms 500      # faster fades
./shim --vmix 10.2.2.195 --streamdeck       # enable Stream Deck

# Cross-compile (no toolchain needed for Linux/Windows)
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o shim.exe ./cmd/shim
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o shim-linux ./cmd/shim

# Diagnostic tools
go build -o joyprobe ./cmd/joyprobe         # axis resolution probe (uses native input)
go build -o joyprobe-raw ./cmd/joyprobe-raw # raw /dev/input/js* probe (Linux only)
```

## Architecture

```
                    ┌────────────────────┐
                    │  vMix (10.2.2.195) │
                    │  HTTP :8088 (XML)  │
                    │  TCP  :8099 (tally │
                    │         + commands)│
                    └────┬──────────┬────┘
                  discovery      tally goroutine
                  (startup)      (channel → router)
                         │              │
┌──────────┐      ┌──────▼──────────────▼──────┐      ┌──────────────┐
│ Joystick │─────▶│       main loop            │─────▶│ Camera A     │
│ (native) │      │                            │─────▶│ Camera B     │
├──────────┤      │  CameraRouter              │─────▶│ Camera C ... │
│ Stream   │─────▶│  Bubbletea TUI             │      │ (VISCA TCP)  │
│ Deck     │◀─────│                            │      └──────────────┘
│ (usbhid) │      └────────────────────────────┘
└──────────┘
```

**Concurrency**: tally + joystick + Stream Deck goroutines send on channels, main goroutine (bubbletea) owns all VISCA connections and vMix commander — no mutexes needed. Stream Deck feedback flows back via a separate channel (LCD updates).

## File Structure

```
cmd/shim/main.go              # Entry point, CLI flags, bubbletea TUI, control loop
cmd/joyprobe/main.go          # Joystick axis resolution diagnostic tool
cmd/joyprobe-raw/main.go      # Raw /dev/input/js* diagnostic (Linux only)
internal/visca/connection.go   # VISCA TCP connection (non-blocking drain, fire-and-forget)
internal/visca/commands.go     # All VISCA commands (focus, zoom, pan/tilt, presets, tally, inquiries)
internal/vmix/discovery.go     # HTTP API camera discovery (parse XML, extract IPs)
internal/vmix/tally.go         # TCP tally subscription (goroutine, auto-reconnect)
internal/vmix/commands.go      # TCP command sender (cut, fade, preview, transitions)
internal/input/joystick.go     # Platform-independent types and math (JoystickState, AxisToSpeed, etc.)
internal/input/joystick_linux.go   # Linux: reads /dev/input/js* directly
internal/input/joystick_windows.go # Windows: winmm.dll joyGetPosEx syscall
internal/input/joystick_darwin.go  # macOS: IOKit HID framework via cgo
internal/input/ioctl_linux.go      # Linux ioctl helper for joystick name
internal/streamdeck/streamdeck.go  # Stream Deck USB HID input + LCD feedback (pure Go, no CGO)
internal/streamdeck/font.go        # 5x7 bitmap font renderer for Stream Deck LCD keys
internal/router/router.go     # Camera routing (preview/program tracking, override)
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

### Anchor-relative focus
The joystick throttle slider has only ~256 discrete positions (8-bit hardware). Mapping the full 4352-position focus range gives ~17 positions per step — too coarse for critical focus.

Instead, the throttle controls a **±300 position window** around an anchor point. Each step = ~2.3 focus positions. The anchor is set automatically on:
- Startup (queries camera's current focus)
- AF release (queries where AF settled)
- Preset recall (queries after 2s settling delay)

No centering needed — wherever the slider is when focus is anchored becomes "zero."

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

**Button mapping:**
| Button | SDL Index | Function |
|---|---|---|
| Trigger | 0 | Hold for AF, release for MF |
| Thumb | 1 | Preset save modifier / AF latch |
| Top-left | 2 | Hold for program camera override |
| Top | 3 | Fade transition (vMix) |
| Top | 4 | Cut transition (vMix) |
| Top | 5 | Cycle preview to next input |
| Base 7-12 | 6-11 | Camera presets 1-6 |

### PreSonus ATOM
- 4 knobs: CC 14-17, **7-bit absolute only (0-127)**, infinite encoder but absolute CC — unusable at extremes
- Not currently used by the Go shim (Python POC only)

## Camera Details

- Model: Prisual TEN-20N Pro (NDI HX3)
- OEM/rebranded Chinese PTZ platform
- VISCA command version: PS-20211007
- vMix discovers NDI cameras with IP in title: `NDI HD CAMERA (NDI HX2,10.2.2.212)`

## Reference Documents

- `HTTPCGIList.pdf` — Official Prisual HTTP-CGI control sheet
- `Visca_Command_V2.1.pdf` — Full VISCA command set
- `Prisual_4K_and_1080P_series_camera_visca_commands_new(Visca-control command).csv` — VISCA command spreadsheet with test results

# Prisual Control

Multi-camera PTZ control shim for live streaming and video production. Maps any USB joystick/gamepad to Prisual TEN-20N Pro PTZ cameras via VISCA, with vMix integration for switching, tally, and camera discovery.

## Why

vMix only supports jog-style focus commands (near/far/stop) — no absolute positioning. This shim bypasses vMix and talks directly to cameras via VISCA TCP, giving precise joystick control over pan, tilt, zoom, and focus, while still integrating with vMix for video switching and tally.

## Quick Start

Download a binary from [Releases](https://github.com/LumenPrima/prisual-control/releases), or build from source:

```bash
go build ./cmd/shim
```

Run with vMix (auto-discovers cameras):
```bash
./shim --vmix 10.2.2.195
```

Or single camera without vMix:
```bash
./shim --camera 10.2.2.212
```

With a custom controller config:
```bash
./shim --config configs/xbox.json --vmix 10.2.2.195
```

## Controller Configuration

Any USB joystick or gamepad can be used. The shim uses a JSON config file to map physical axes and buttons to logical actions.

**Default**: Logitech Extreme 3D Pro (no `--config` needed).

**To set up a new controller:**

1. Probe the controller to see axes and buttons:
   ```bash
   go build -o joyprobe ./cmd/joyprobe && ./joyprobe
   ```

2. Generate a starter config:
   ```bash
   ./shim --dump-config > configs/mycontroller.json
   ```

3. Edit the JSON — remap axis indices, adjust deadzones/expo, assign buttons:
   ```json
   {
     "name": "My Controller",
     "axes": {
       "pan":   { "index": 0, "deadzone": 0.10, "expo": 2.5, "inverted": false },
       "tilt":  { "index": 1, "deadzone": 0.10, "expo": 2.5, "inverted": true },
       "zoom":  { "index": 2, "deadzone": 0.15, "expo": 2.5, "inverted": false }
     },
     "buttons": {
       "af_hold": 0, "af_latch": 1, "program_override": 2,
       "fade": 3, "cut": 4, "cycle_preview": 5,
       "preset_save": 1, "presets": [6, 7, 8, 9, 10, 11]
     }
   }
   ```

4. Run with the config:
   ```bash
   ./shim --config configs/mycontroller.json --vmix 10.2.2.195
   ```

Set any button to `-1` to leave it unmapped. Example configs included for the Extreme 3D Pro and Xbox controller.

## Controls (Default: Logitech Extreme 3D Pro)

| Control | Function |
|---|---|
| Stick X/Y | Pan/Tilt speed (expo curve, spring-return) |
| Twist | Zoom speed (spring-return) |
| Stream Deck FOCUS FAR / FOCUS NEAR | Manual focus jog while held |
| Trigger (hold) | Auto-focus while held |
| Thumb + Trigger | Latch auto-focus (stays on after trigger release) |
| Top-left (hold) | Override: control program camera instead of preview |
| Top buttons 4/5/6 | Fade / Cut / Cycle preview (vMix) |
| Base buttons 7-12 | Camera presets 1-6 (thumb + base = save) |

### Focus System

Manual focus is currently Stream Deck driven:

- **FOCUS FAR / FOCUS NEAR**: hold to jog focus, release to stop
- Focus jog switches the camera to manual focus and cancels latched AF
- The joystick throttle is intentionally ignored for now

### Auto-Focus Modes

- **Hold-for-AF**: Hold trigger → AF active. Release → back to manual.
- **Latched AF**: While holding trigger, press thumb → AF stays on after trigger release. Unlatch by pressing trigger again or using manual focus jog.

## TUI

```
╭──────────────────────────────────────────────────────────────╮
│ PRISUAL Camera Shim                                          │
│                                                              │
│ CAMERAS                                                      │
│  ● 1 10.2.2.212    PGM                                      │
│  ● 2 10.2.2.215    PVW ◀                                    │
│  ○ 3 10.2.2.218     ·                                       │
│                                                              │
│ AXES  Logitech Extreme 3D [Logitech Extreme 3D Pro]          │
│    Pan ──────────────┼──────◆───────   +1234                 │
│   Tilt ────────◆─────┼───────────────   -456                 │
│   Zoom ████████████░░░░░░░░░░░░░░░░░░  0x1234                │
│                                                              │
│ FOCUS                                                        │
│        ──────────────────────────────  MANUAL deck hold      │
│                                                              │
│  ● vMix 10.2.2.195  cmds 1247                               │
│                                                              │
│  › Anchored @ 0x0830                                         │
│                                                              │
│  q quit  trigger AF  thumb+trigger latch  base 7-12 presets  │
│  btn4 fade  btn5 cut  btn6 next preview                      │
╰──────────────────────────────────────────────────────────────╯
```

## Flags

| Flag | Default | Description |
|---|---|---|
| `--vmix` | | vMix host IP (enables discovery + tally + transitions) |
| `--camera` | | Single camera IP (fallback if no vMix) |
| `--config` | | Controller config JSON file (default: built-in Extreme 3D Pro) |
| `--dump-config` | | Print default controller config JSON and exit |
| `--visca-port` | 5678 | VISCA TCP port |
| `--fade-ms` | 1000 | Fade transition duration in milliseconds |

## Building

No external dependencies on Linux or Windows. Single static binary.

```bash
# Native build
go build ./cmd/shim

# Cross-compile
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o shim.exe ./cmd/shim
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o shim-linux ./cmd/shim

# macOS (must build on a Mac, needs Xcode CLI tools for IOKit)
go build ./cmd/shim
```

## Hardware

- **Cameras**: Prisual TEN-20N Pro (NDI HX2) — VISCA over IP on TCP port 5678
- **Controllers**: Any USB joystick/gamepad (tested: Logitech Extreme 3D Pro)
- **Switcher**: vMix on Windows — HTTP API for discovery, TCP API for tally + commands

## Camera Documentation

See [`docs/camera_capabilities.md`](docs/camera_capabilities.md) for a comprehensive probe of the Prisual camera's IP capabilities including:
- All streaming modes (H.264/H.265, NDI HX2, RTSP, SRT)
- Full CGI API endpoint list (documented and undocumented)
- Image/exposure/white balance settings
- VISCA inquiry results
- NDI Full/HX2 toggle
- Firmware update info

## Diagnostic Tools

```bash
# Test joystick axis resolution (uses native platform input)
go build -o joyprobe ./cmd/joyprobe && ./joyprobe

# Test raw Linux joystick device
go build -o joyprobe-raw ./cmd/joyprobe-raw && ./joyprobe-raw
```

## Python POCs

The `poc_*.py` files are standalone proof-of-concept scripts used during initial development:
- `poc_joystick_focus.py` — Single-camera joystick control via pygame
- `poc_atom_focus.py` — ATOM MIDI knob to focus/zoom
- `probe_pygame.py` — Axis resolution measurement

These require `pygame`, `mido`, and `python-rtmidi`. The Go shim supersedes them.

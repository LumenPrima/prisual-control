# Prisual Control

Multi-camera PTZ control shim for church live streaming. Maps a Logitech Extreme 3D Pro joystick to Prisual TEN-20N Pro PTZ cameras via VISCA, with vMix integration for switching, tally, and camera discovery.

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

## Controls

### Joystick (Logitech Extreme 3D Pro)

| Control | Function |
|---|---|
| Stick X/Y | Pan/Tilt speed (expo curve, spring-return) |
| Twist | Zoom speed (spring-return) |
| Throttle slider | Fine focus adjustment (anchor-relative, ±300 positions) |
| Trigger (hold) | Auto-focus while held |
| Thumb + Trigger | Latch auto-focus (stays on after trigger release) |
| Top-left (hold) | Override: control program camera instead of preview |
| Top buttons 4/5/6 | Fade / Cut / Cycle preview (vMix) |
| Base buttons 7-12 | Camera presets 1-6 (thumb + base = save) |

### Focus System

The throttle slider uses **anchor-relative** control for fine focus:

- **Presets and AF set the anchor** — wherever the slider is at that moment becomes "zero"
- **Moving the slider adjusts focus** relative to the anchor (±300 positions by default)
- **~2.3 focus positions per step** — enough for critical focus work
- **No centering needed** — the slider doesn't need to be at any particular position

Re-anchoring happens automatically on preset recall, AF release, and startup.

### Auto-Focus Modes

- **Hold-for-AF**: Hold trigger → AF active. Release → back to manual, focus anchored where AF settled.
- **Latched AF**: While holding trigger, press thumb → AF stays on after trigger release. Unlatch by pressing trigger again or moving the slider.

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
│ AXES  Logitech Extreme 3D                                    │
│    Pan ──────────────┼──────◆───────   +1234                 │
│   Tilt ────────◆─────┼───────────────   -456                 │
│   Zoom ████████████░░░░░░░░░░░░░░░░░░  0x1234                │
│                                                              │
│ FOCUS                                                        │
│        ──────────────┼─◆────────────  0x0830 ±300            │
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
| `--visca-port` | 5678 | VISCA TCP port |
| `--expo` | 2.5 | Expo curve for spring-return axes |
| `--focus-hz` | 10 | Focus command send rate |
| `--focus-range` | 300 | Focus half-range (full slider = ±N positions) |
| `--fade-ms` | 1000 | Fade transition duration in milliseconds |
| `--invert-tilt` | true | Invert tilt axis (stick forward = tilt down) |

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

- **Cameras**: Prisual TEN-20N Pro (NDI HX3) — VISCA over IP on TCP port 5678
- **Joystick**: Logitech Extreme 3D Pro — 4 axes (8-10 bit), 12 buttons, 1 POV hat
- **Switcher**: vMix on Windows — HTTP API for discovery, TCP API for tally + commands

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

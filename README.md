# Prisual Control

Multi-camera PTZ control for live streaming and video production. Run a whole multi-camera show from a **Stream Deck** — input switching, transitions, camera presets, pan/tilt/zoom, and focus — with a USB joystick on hand for fine camera moves. Talks to Prisual TEN-20N Pro PTZ cameras directly over VISCA and integrates with vMix for switching, tally, and camera discovery.

## Why

vMix only supports jog-style focus commands (near/far/stop) — no absolute positioning. This shim bypasses vMix and talks directly to the cameras via VISCA TCP, giving precise control over pan, tilt, zoom, and focus, while still integrating with vMix for video switching and tally.

In real productions the **Stream Deck is the primary surface** — it drives switching, transitions, presets, and camera movement on its own. The joystick is an optional complement for smooth, fine framing.

## Quick Start

Download a binary from [Releases](https://github.com/LumenPrima/prisual-control/releases), or build from source:

```bash
go build ./cmd/shim
```

Run with vMix and a Stream Deck (auto-discovers cameras and the deck):
```bash
./shim --vmix 10.2.2.195 --streamdeck
```

Single camera, no vMix:
```bash
./shim --camera 10.2.2.212 --streamdeck
```

Add a joystick with a custom controller config:
```bash
./shim --vmix 10.2.2.195 --streamdeck --config configs/xbox.json
```

## Stream Deck

The main control surface for a live show. Enable with `--streamdeck`; the device is auto-discovered over USB. Supported: **Stream Deck XL / XL V2** (32 keys) and **Stream Deck MK.2 / V2** (15 keys) — the layout adapts to the key count. Every key is a live LCD button with tally-aware color feedback.

From the deck you can:

- **Switch & transition** — put any vMix input on preview, then **CUT** or **FADE** to air. Keys show tally at a glance: red = live, green = preview.
- **Camera presets** — recall saved positions (**P1–P8**); the active preset lights blue. Press **SET**, then a preset key, to save the current position.
- **Move cameras** — a right-side D-pad (**UP / DOWN / LEFT / RIGHT**, **Z IN / Z OUT**, **HOME**) with zoom-adaptive speed that eases off as you zoom in.
- **Focus** — **FOCUS** autofocuses while held (key turns pink, then returns to manual); **FOCUS FAR / NEAR** jog manual focus.
- **Drive Live** — toggle control onto the program camera instead of preview (turns red as a warning).
- **Lower Third** — toggle the lower-third overlay.
- **Slides** — **SLIDE < / >** drive Proclaim remotely (with `--proclaim`).
- **Stream** — **LIVE** double-taps to start/stop streaming, with a confirm tap for safety.

Full XL key map and color reference: [`STREAMDECK.md`](STREAMDECK.md).

## Joystick (optional)

For smooth, fine camera moves, any USB joystick or gamepad can drive pan/tilt/zoom. The shim maps physical axes and buttons to logical actions via a JSON config.

**Default**: Logitech Extreme 3D Pro (no `--config` needed).

| Control | Function |
|---|---|
| Stick X/Y | Pan/Tilt speed (expo curve, spring-return) |
| Twist | Zoom speed (spring-return) |
| Trigger (hold) | Auto-focus while held |
| Thumb + Trigger | Latch auto-focus (stays on after trigger release) |
| Top-left (hold) | Override: control program camera instead of preview |
| Top buttons 4/5/6 | Fade / Cut / Cycle preview (vMix) |
| Base buttons 7–12 | Camera presets 1–6 (thumb + base = save) |

**Auto-focus modes:**

- **Hold-for-AF**: hold trigger → AF active; release → back to manual.
- **Latched AF**: while holding trigger, press thumb → AF stays on after the trigger releases. Unlatch by pressing the trigger again, or by jogging manual focus on the deck.

### Setting up a different controller

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
4. Run with the config (`--config configs/mycontroller.json`). Set any button to `-1` to leave it unmapped. Example configs for the Extreme 3D Pro and Xbox controller are included.

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
| `--streamdeck` | `false` | Enable Stream Deck support (auto-discovers the device) |
| `--proclaim` | | Proclaim host IP (enables slide control) |
| `--proclaim-pass` | `proclaim` | Proclaim network password |
| `--config` | | Controller config JSON file (default: built-in Extreme 3D Pro) |
| `--dump-config` | | Print default controller config JSON and exit |
| `--visca-port` | `5678` | VISCA TCP port |
| `--fade-ms` | `1000` | Fade transition duration in milliseconds |

## Building

No external dependencies on Linux or Windows — a single static binary.

```bash
# Native build
go build ./cmd/shim

# Cross-compile
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o shim.exe ./cmd/shim
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o shim-linux ./cmd/shim

# macOS (must build on a Mac — joystick input uses IOKit, needs Xcode CLI tools)
go build ./cmd/shim
```

## Hardware

- **Cameras**: Prisual TEN-20N Pro (NDI HX2) — VISCA over IP on TCP port 5678
- **Control surface**: Elgato Stream Deck XL / XL V2 (32 keys) or MK.2 / V2 (15 keys)
- **Controllers**: any USB joystick/gamepad (tested: Logitech Extreme 3D Pro)
- **Switcher**: vMix on Windows — HTTP API for discovery, TCP API for tally + commands
- **Slides** (optional): Faithlife Proclaim, controlled over its local HTTP API

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
# Stream Deck: identify the device and watch key presses
go build -o deckprobe ./cmd/deckprobe && ./deckprobe

# VISCA: send raw commands to a camera and inspect replies
go build -o viscaprobe ./cmd/viscaprobe && ./viscaprobe

# Joystick axis resolution (native platform input)
go build -o joyprobe ./cmd/joyprobe && ./joyprobe

# Raw Linux joystick device
go build -o joyprobe-raw ./cmd/joyprobe-raw && ./joyprobe-raw
```

## Python Probes

- `probe_pygame.py` — axis-resolution measurement (requires `pygame`)

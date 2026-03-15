# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Map a PreSonus ATOM Pad (MIDI note velocity, 0–127) to absolute focus position on Prisual TEN-20N Pro NDI HX3 PTZ cameras used in a church live streaming setup controlled via vMix on Windows.

vMix only supports jog-style focus commands (near/far/stop) — no absolute positioning. This project bypasses vMix to talk directly to the cameras.

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
| Focus Far (variable) | `81 01 04 08 2p FF` | p = speed 0-7 |
| Focus Near (variable) | `81 01 04 08 3p FF` | p = speed 0-7 |
| Focus Stop | `81 01 04 08 00 FF` | |
| Lens Block Inquiry | `81 09 7E 7E 00 FF` | Returns zoom pos, focus pos, and focus mode (bit0: 1=auto, 0=manual) in one response |

**VISCA response format:**
- ACK: `z0 4y FF` (y = socket number)
- Completion: `z0 5y FF`
- Syntax Error: `z0 60 02 FF`
- Command Buffer Full: `z0 60 03 FF`
- Command Not Executable: `z0 6y 41 FF` (e.g., manual focus cmd during auto focus)

**Other useful VISCA commands:**
- Zoom Direct: `81 01 04 47 0p 0q 0r 0s FF` (max position `4000`)
- Pan/Tilt Absolute: `81 01 06 02 VV WW 0Y 0Y 0Y 0Y 0Z 0Z 0Z 0Z FF`
- Preset Set: `81 01 04 3F 01 pp FF` (pp = 0-254)
- Preset Recall: `81 01 04 3F 02 pp FF`

### CGI/HTTP API (no absolute focus)
- Base URL: `http://<camera-ip>/cgi-bin/ptzctrl.cgi?ptzcmd&...`
- Has absolute positioning for zoom (`zoomto&<speed>&<position>`) and pan/tilt (`abs` mode) but **not focus**
- Focus only has jog commands: `focusin`, `focusout`, `focusstop` (speeds 1-7)
- Focus lock: `param.cgi?ptzcmd&lock_mfocus` / `unlock_mfocus`
- Image settings: `param.cgi?post_image_value&<mode>&<level>` (bright/saturation/contrast/sharpness/hue, level 0-14)
- Inquiry endpoints: `param.cgi?get_device_conf`, `get_media_video`, `get_network_conf`, `get_serial_number`
- Snapshot: `http://<camera-ip>/snapshot.jpg`

**Confirmed focus range (probed on 10.2.2.212):**
- Usable range: `0x0080` to `0x1180` — exact 1:1 positioning, zero error
- Hard clamp at `0x11E4` — anything above `0x1200` is ignored
- `0x0000` gets clamped up to ~`0x01BD`
- **4480 usable discrete positions**
- **Motor speed: ~470 positions/sec** — full traverse takes ~9.5s regardless of command type (Focus Direct and variable speed 7 are identical)
- Focus motor speed is a hardware limit, not configurable via VISCA

## Target Architecture

```
Control surface (joystick / MIDI / gamepad / Stream Deck)
    → Python shim (input abstraction layer)
    → Maps axes/buttons to camera commands or vMix API calls
    → VISCA TCP to camera IP:5678 (focus, zoom, pan/tilt, presets)
    → vMix TCP API port 8099 (switching, transitions, overlays)
```

## Hardware Notes

### Logitech Extreme 3D Pro (confirmed working)
- 4 axes: X (stick LR), Y (stick FB), 2 (twist), 3 (throttle slider)
- 12 buttons, 1 POV hat
- **Zero jitter at rest** — ADC is rock solid, no filtering needed
- Stick X/Y (axes 0/1) → pan/tilt speed (spring-return jog, max pan 0x18, tilt 0x14)
- Twist (axis 2) → zoom speed/jog (spring-return, speeds ±1-7)
- Throttle slider (axis 3) → absolute focus (stays where you leave it, full 4096-step resolution)
- Buttons 0-5: trigger, thumb, 4 top buttons. Buttons 6-11: 6 base buttons (used for presets)
- **Expo curve 2.5 on all spring-return axes** — dramatically improves fine control, better than dedicated PTZ sticks
- VISCA send rate: 10 Hz works well for focus, speed commands only sent on change
- Camera presets (VISCA slots 200+) confirmed to save/recall pan, tilt, zoom, AND focus

### PreSonus ATOM (confirmed working)
- 4 knobs: CC 14-17, **7-bit absolute only (0-127)**, not configurable
- Shift+knobs: CC 18-21 (second layer)
- 16 pads: channel 9, velocity-sensitive + aftertouch
- Buttons: CC 86 (Setup), CC 87, CC 104, etc. — momentary 127/0
- Advanced Setup Mode (Shift+Setup): can only change MIDI channels, nothing else

## Key Dependencies

- `pygame` for USB joystick/gamepad input
- `mido` + `python-rtmidi` for MIDI input
- Raw TCP sockets for VISCA communication (no external library needed)
- `requests` for any CGI/HTTP calls

## Camera Details

- Model: Prisual TEN-20N Pro (NDI HX3)
- Likely OEM/rebranded Chinese PTZ platform (Zowietek, Smtav, or similar)
- Related brands with better docs: PTZOptics, BZBGEAR, Minrray, Avkans
- Web UI at camera IP
- VISCA command version: PS-20211007
- Version inquiry: `81 09 00 02 FF` returns hardware/ARM/FPGA versions and camera model type (01=C, 02=M, 03=S)

## Reference Documents

- `start_here.md` — Project goals, investigation plan, and architecture overview
- `HTTPCGIList.pdf` — Official Prisual HTTP-CGI control sheet (3 pages)
- `Visca_Command_V2.1.pdf` — Full VISCA command set with control commands, query commands, and return codes (8 pages)
- `Prisual_4K_and_1080P_series_camera_visca_commands_new(Visca-control command).csv` — VISCA command spreadsheet with test results for serial and network

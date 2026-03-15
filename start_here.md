# Prisual PTZ Camera — Absolute Focus Control via MIDI

## Goal

Map a **PreSonus ATOM Pad** (MIDI note velocity, 0–127) to **absolute focus position** on **Prisual TEN-20N Pro** NDI HX3 PTZ cameras, for use in a church live streaming production controlled via **vMix**.

## What We've Established

### vMix PTZ Focus Limitations

vMix only exposes these focus-related shortcut functions:

- `PTZFocusAuto`
- `PTZFocusFar`
- `PTZFocusNear`
- `PTZFocusManual`
- `PTZFocusStop`

These are all jog/trigger commands — **none accept a position value**. vMix cannot do absolute focus positioning natively. We need to bypass vMix and talk to the cameras directly.

### Prisual HTTP-CGI API (Confirmed from Official Docs)

The Prisual HTTP-CGI API supports:

**Focus (jog only, no absolute positioning):**
```
GET http://<camera-ip>/cgi-bin/ptzctrl.cgi?ptzcmd&focusin&<speed>    # speed 1-7
GET http://<camera-ip>/cgi-bin/ptzctrl.cgi?ptzcmd&focusout&<speed>   # speed 1-7
GET http://<camera-ip>/cgi-bin/ptzctrl.cgi?ptzcmd&focusstop
```

**Focus lock:**
```
GET http://<camera-ip>/cgi-bin/param.cgi?ptzcmd&lock_mfocus
GET http://<camera-ip>/cgi-bin/param.cgi?ptzcmd&unlock_mfocus
```

**Zoom DOES have absolute positioning:**
```
GET http://<camera-ip>/cgi-bin/ptzctrl.cgi?ptzcmd&zoomto&<speed>&<position>
# speed: 0-7, position: 0000 (wide) to 4000 (tele) in hex
```

**Pan/Tilt also has absolute positioning:**
```
GET http://<camera-ip>/cgi-bin/ptzctrl.cgi?ptzcmd&abs&<pan_speed>&<tilt_speed>&<pan_pos>&<tilt_pos>
```

**Key gap: No `focusto` or equivalent absolute focus command exists in the CGI API.**

### Camera Details

- Model: Prisual TEN-20N Pro (NDI HX3)
- Protocols supported: VISCA over IP, VISCA serial (RS232/RS485), ONVIF, CGI/HTTP, NDI
- These cameras are likely OEM/rebranded from a common Chinese PTZ platform (possibly Zowietek, Smtav, or similar)
- Web UI is accessible at the camera's IP address
- Standard VISCA-over-IP port is typically 5678 (TCP) or 1259 (UDP)

## What Needs To Be Investigated

### 1. Probe VISCA over IP for Absolute Focus

The CGI API is often a limited subset of what the firmware supports. Standard VISCA protocol includes a `CAM_Focus Direct` command for absolute positioning. This needs to be tested.

**VISCA commands to test (send as raw hex bytes over TCP to camera IP, port 5678):**

Set manual focus mode first:
```
TX: 81 01 04 38 03 FF
Expected RX: 90 41 FF 90 51 FF (ACK + Completion)
```

Focus position inquiry:
```
TX: 81 09 04 48 FF
Expected RX: 90 50 0p 0q 0r 0s FF (where pqrs = current focus position)
```

Focus direct (absolute position):
```
TX: 81 01 04 48 0p 0q 0r 0s FF
(pqrs = focus position, typically 0x0000 = infinity/far, up to ~0x1000 = near)
Expected RX: 90 41 FF 90 51 FF (ACK + Completion)
```

Focus near variable speed:
```
TX: 81 01 04 08 3p FF (p = speed 1-7, focuses near)
TX: 81 01 04 08 2p FF (p = speed 1-7, focuses far)
TX: 81 01 04 08 00 FF (focus stop)
```

**Write a Python script using raw sockets to:**
1. Connect TCP to camera on port 5678
2. Send the focus inquiry command
3. Parse and display the response
4. If inquiry works, test setting absolute focus to a few different values
5. Also try UDP on port 1259 if TCP doesn't respond

### 2. Probe for Undocumented CGI Endpoints

The official CGI docs don't show a `focusto` command, but it may exist undocumented since `zoomto` exists with the same pattern.

**Test these URLs against the camera:**
```
http://<camera-ip>/cgi-bin/ptzctrl.cgi?ptzcmd&focusto&<speed>&<position>
http://<camera-ip>/cgi-bin/ptzctrl.cgi?ptzcmd&focusset&<position>
http://<camera-ip>/cgi-bin/ptzctrl.cgi?ptzcmd&focusdirect&<speed>&<position>
http://<camera-ip>/cgi-bin/ptzctrl.cgi?ptzcmd&focus_abs&<speed>&<position>
```

Try with speed=1 and position values like 0000, 0800, 1000 (hex strings).

Also probe for a general parameter getter that might reveal more commands:
```
http://<camera-ip>/cgi-bin/param.cgi?get_ptz_conf
http://<camera-ip>/cgi-bin/param.cgi?get_focus_conf
http://<camera-ip>/cgi-bin/param.cgi?get_pt_conf
```

### 3. Search Online for OEM Documentation

The Prisual cameras share firmware with many other brands. Search for:

- The firmware version string (visible in the camera's web UI or via `get_device_conf`)
- Common OEM PTZ camera CGI command lists that include absolute focus
- Zowietek, Smtav, Minrray, Avkans, BZBGEAR PTZ HTTP API documentation — these often use the same firmware
- "ptzctrl.cgi focusto" or "cgi-bin ptzctrl.cgi focus absolute"
- PTZOptics HTTP-CGI focus commands (PTZOptics uses a similar platform and has better documentation)
- Any VISCA command list specific to cameras using the Hisilicon/Ambarella SoC platform common in Chinese PTZ cameras

### 4. Check ONVIF Capabilities

Use ONVIF to query what the camera advertises it supports:

```python
# pip install onvif-zeep
from onvif import ONVIFCamera

cam = ONVIFCamera('<camera-ip>', 80, 'admin', 'password')
ptz_service = cam.create_ptz_service()
media_service = cam.create_media_service()

# Get profiles
profiles = media_service.GetProfiles()
profile_token = profiles[0].token

# Get PTZ configuration to see if absolute focus is supported
ptz_config = ptz_service.GetConfiguration({'PTZConfigurationToken': profiles[0].PTZConfiguration.token})

# Check node for supported operations
nodes = ptz_service.GetNodes()
for node in nodes:
    print(f"Node: {node.Name}")
    print(f"  Supported PTZ Spaces: {node.SupportedPTZSpaces}")
```

ONVIF `AbsoluteMove` can include an optional focus parameter — check if the camera's ONVIF profile advertises focus space support.

### 5. NDI PTZ Control

NDI itself has PTZ control capabilities. The NDI SDK includes functions for controlling PTZ cameras over the NDI connection. Check if NDI's PTZ control API supports absolute focus — if so, this might be the cleanest path since the cameras are already connected via NDI to vMix.

Search for:
- NDI SDK PTZ focus control
- `NDIlib_recv_ptz_focus` or similar function names
- Whether vMix exposes NDI PTZ focus through its scripting/API layer

## Target Architecture (Once Absolute Focus Method is Found)

```
PreSonus ATOM Pad
    │ (MIDI note_on, velocity 0-127)
    ▼
Python Script (mido library)
    │ (maps velocity to focus position value)
    ▼
Camera Direct Control
    (VISCA TCP / CGI HTTP / ONVIF / NDI — whichever works)
    │
    ▼
Prisual TEN-20N Pro
    (absolute focus position set)
```

## Fallback: Jog Emulation

If no absolute focus method exists anywhere, the fallback is jog emulation using the CGI API:

1. ATOM pad velocity determines direction and duration of a focus jog
2. Two pads: one for focus near, one for focus far
3. Velocity maps to jog duration (higher velocity = longer movement)
4. Script sends `focusin`/`focusout` then `focusstop` after calculated delay

This is less precise but workable. Could also map specific pads to focus presets by jogging to known positions from a home point.

## Environment Notes

- Production machine runs vMix on Windows
- Python with `mido` and `python-rtmidi` available for MIDI input
- Cameras are on the local network, accessible by IP
- All testing can be done with curl, Packet Sender, or Python scripts

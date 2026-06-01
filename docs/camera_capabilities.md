# Prisual TEN-20N Pro — IP Capabilities Probe

Probed 2026-03-15 on camera at 10.2.2.212.

## Device Info

```
Firmware: X8.03.15
Model:    F2. V
Serial:   vf295a0b409
RTSP:     ZLMediaKit (git hash:413be5c58, build Sep 27 2024)
NDI:      v5.1.1, channel "NDI HX2"
```

## Open Ports

| Port | Service | Notes |
|---|---|---|
| 80 | HTTP (web UI) | Digest auth, admin:admin |
| 81 | ONVIF SOAP | Device, Media, PTZ, Imaging services |
| 554 | RTSP | Two streams, no auth required (rtsp_auth_en=0) |
| 3100 | Unknown | TCP open, non-HTTP |
| 5555, 5556 | NDI data | NDI HX2 transport |
| 5678 | VISCA TCP | PTZ control |
| 5960, 5962 | NDI | NDI-related |
| 7775, 7776 | Unknown | TCP open |
| 8000 | HTTP alt | Empty response |

## Video Streams

### RTSP Stream 1 (Primary)
```
URL:      rtsp://10.2.2.212:554/1
Codec:    H.264 High Profile
Size:     1920x1080
FPS:      60
Bitrate:  62000 kbps (CBR)
GOP:      20
Audio:    AAC LC, 48kHz, stereo
```

### RTSP Stream 2 (Secondary)
```
URL:      rtsp://10.2.2.212:554/2
Codec:    H.264 High Profile
Size:     640x360
FPS:      30
Bitrate:  3000 kbps (CBR)
GOP:      30
Audio:    AAC LC, 48kHz, stereo
```

### NDI
```
NDI version: v5.1.1
Mode:        NDI HX2 (H.264 or H.265 selectable)
Channel:     "NDI HX2"
Full NDI:    Toggleable (requires reboot)
License:     Active
Discovery:   Disabled (uses manual registration)
Multicast:   Disabled
```

**NDI mode options** (changeable live, no reboot):
- `264P60` — H.264 at 60fps (default)
- `264P50` — H.264 at 50fps
- `265P60` — H.265 at 60fps
- `265P50` — H.265 at 50fps

**Full NDI toggle** (requires reboot):
- Enable: `curl "http://camera/cgi-bin/param.cgi?post_ndi_info&Full_NDI=1"`
- Disable: `curl "http://camera/cgi-bin/param.cgi?post_ndi_info&Full_NDI=false"`
- **IMPORTANT**: Use `false` to disable, NOT `0` — the firmware treats `0` as truthy (bug)
- When enabled, camera advertises full NDI (SpeedHQ) capability to receivers
- Actual codec negotiation happens between camera and NDI receiver (vMix)

**NDI vs advertised specs**: Amazon listing claims NDI 6 / HX3 support, but camera ships with NDI 5.1.1 / HX2. Newer firmware (V8.03.13 or later) on Prisual downloads page claims NDI 6.1.1 / HX3 support — firmware update may be needed.

**Measured bandwidth**: ~62 Mbps actual for 1080p60 H.264 over RTSP (matches configured `bps_1=62000`).

### Available Codecs
- H.264 (Main Profile and High Profile confirmed)
- **H.265/HEVC supported** — for both RTSP streams and NDI
- SRT available but not connected (srt_en=1)
- RTMP available but disabled
- Multicast available but disabled

## ONVIF Services

All four core services are present on port 81:

| Service | Endpoint | Status |
|---|---|---|
| Device | `/onvif/device_service` | Working |
| Media | `/onvif/media` | Working |
| PTZ | `/onvif/ptz` | Working |
| Imaging | `/onvif/imaging` | Working |

**Note**: `onvif_en="0"` in network config, but services respond. May need to be enabled for full functionality or auto-discovery.

ONVIF version: 2.0

## Image/Exposure Settings (CGI `get_image_conf`)

All controllable via `post_image_conf` or `post_image_value` CGI endpoints.

### Exposure
| Setting | Current | Range/Notes |
|---|---|---|
| `exposure_mode` | 0 | 0=Auto, others=manual modes |
| `expcomp_mode` | 1 | Exposure compensation on/off |
| `expcomp_level` | -2 | Exposure compensation level |
| `shutter` | 0 | 0=auto. VISCA inquiry returns 0x0001 |
| `iris` | 11 | Iris/aperture value. VISCA: 0x000C |
| `manual_gain` | 3 | Manual gain setting |
| `gain_limit` | 6 | Max auto gain |
| `antiflicker` | 2 | 0=Off, 1=50Hz, 2=60Hz |
| `backlight` | 0 | Backlight compensation |
| `drc_mode` | 0 | Dynamic range compression mode |
| `drc` | 5 | DRC strength |
| `metering_mode` | 2 | 0=Average, 1=Center, 2=Smart |

### White Balance
| Setting | Current | Notes |
|---|---|---|
| `wb_mode` | 0 | 0=Auto. VISCA confirms: 0x00 |
| `rgain` | 90 | Red gain (manual WB) |
| `bgain` | 80 | Blue gain (manual WB) |
| `rgain_tuning` | 0 | Fine R adjustment |
| `bgain_tuning` | 0 | Fine B adjustment |
| `temperature` | 65 | Color temperature |
| `awb_sens` | 2 | AWB sensitivity |

### Image Processing
| Setting | Current | Notes |
|---|---|---|
| `bright` | 50 | Brightness (0-100) |
| `saturation` | 100 | Saturation (0-100) |
| `luminance` | 50 | Luminance (0-100) |
| `contrast` | 50 | Contrast (0-100) |
| `sharpness` | 24 | Sharpness level |
| `sharpness_mode` | 0 | 0=auto, 1=manual |
| `hue` | 50 | Hue (0-100) |
| `gamma_mode` | 1 | Gamma curve selection |
| `gamma` | 5 | Gamma value |
| `style` | 0 | Picture style/profile |
| `ldc` | 0 | Lens distortion correction |

### Noise Reduction
| Setting | Current | Notes |
|---|---|---|
| `noise2D_mode` | 1 | 2D NR mode (0=off, 1=auto) |
| `noise2D` | 10 | 2D NR strength |
| `noise3D_mode` | 0 | 3D NR mode |
| `noise3D` | 9 | 3D NR strength |

### Focus Settings
| Setting | Current | Notes |
|---|---|---|
| `focus_zone` | 1 | AF zone (0=top, 1=center, 2=bottom, 3=all) |
| `focus_sens` | 2 | AF sensitivity (0=low, 1=normal, 2=high) |
| `focus_lim` | 0 | Focus limit enabled |
| `far_limit` | 11 | Far focus limit |
| `near_limit` | 0 | Near focus limit |

### Image Orientation
| Setting | Current | Notes |
|---|---|---|
| `mirror` | 0 | Horizontal flip |
| `flip` | 0 | Vertical flip |

## Audio

```
Switch:     On (audio_switch=1)
Codec:      AAC
Sample:     48 kHz
Bitrate:    128 kbps
Input:      Line-in
Volume L/R: 8/8
```

## PTZ Speed Configuration

```
Pan speed:   23 (max 24 / 0x18)
Tilt speed:  19 (max 20 / 0x14)
Zoom speed:  7
Focus speed: 7
OSD mode:    ptz
```

## Network Configuration

| Setting | Value |
|---|---|
| DHCP | Enabled |
| IP | 10.2.2.212 |
| MAC | d4:e0:8e:04:9c:68 |
| HTTP port | 80 |
| RTSP port | 554 |
| VISCA TCP | 5678 |
| VISCA UDP | 1259 |
| Sony VISCA | 52381 |
| RTSP auth | Disabled |
| ONVIF | Disabled (but services respond) |
| RTMP | Disabled |
| SRT | Enabled (not connected) |
| Multicast | Disabled |
| FreeD | Disabled |
| GB28181 | Disabled |
| NTP | Enabled (cn.ntp.org.cn) |

## VISCA Inquiry Results

| Inquiry | Response | Decoded |
|---|---|---|
| Version | `00 56 46 d5 03 15 02` | Model "VF", firmware 03.15, type 02 (M) |
| Lens Block | `00 06 02 03 00 00 00 0e 04 0e 00 00 00` | Zoom=0x0602, Focus=0x0E4E, Mode flags |
| Pan/Tilt | `0f 0f 04 05 0f 0f 0e 0a` | Pan=0xFF45 (-187), Tilt=0xFFEA (-22) |
| Zoom | `01 04 02 04` | Position 0x1424 |
| Focus | `00 0e 04 0e` | Position 0x0E4E |
| White Balance | `00` | Mode 0 = Auto |
| Exposure Mode | `00 00` | Full Auto |
| Gain | `00 00 00 02` | Gain value 2 |
| Shutter | `00 00 00 01` | Shutter value 1 (auto) |
| Iris | `00 00 00 0c` | Iris value 12 |
| Brightness | `00 00 03 02` | Brightness 0x0302 |

## CGI API Endpoints (Discovered)

### Working GET endpoints (no auth required)
- `get_device_conf` — firmware version, model, serial
- `get_media_video` — codec, resolution, bitrate, GOP, FPS for both streams
- `get_network_conf` — all network settings, streaming URLs, RTMP/SRT/multicast config
- `get_image_conf` — full image/exposure/WB/NR settings
- `get_image_default_conf` — factory default image settings
- `get_ndi_info` — NDI version, channel name, license, multicast settings
- `get_media_audio` — audio codec, sample rate, bitrate, input/volume
- `get_speed_conf` — PTZ speed limits
- `get_system_conf` — usernames and passwords (!)
- `get_overlay_conf` — OSD overlay settings
- `get_aimode` — AI tracking mode (currently Off)
- `get_log_level` — logging level
- `get_record_info` — USB recording status
- `get_udisk_info` — USB disk info
- `get_uploadflag_conf` — firmware upload status

### POST endpoints (from web UI JS)
- `post_image_conf` — set image settings
- `post_image_value` — set individual image values
- `post_media_video` — change codec/resolution/bitrate
- `post_media_videoset` — video settings
- `post_media_audio` — audio settings
- `post_ndi_info` — NDI configuration
- `post_network_info_conf` — network settings
- `post_network_ntp_conf` — NTP settings
- `post_network_other_conf` — other network settings
- `post_overlay_conf` — OSD settings
- `post_speed_conf` — PTZ speed settings
- `post_system_conf` — user/password settings
- `post_roi_conf` — region of interest
- `post_aimode` — AI tracking mode
- `post_visca` — VISCA settings (!)
- `post_reboot` — reboot camera
- `post_devinfo_conf` — device info settings
- `post_log_level` — log level
- `save_record` — recording control
- `save_time` — time/NTP settings

## Security Concerns

1. **`get_system_conf` returns plaintext usernames AND passwords without auth** — `admin:admin`, `guest:guest`
2. RTSP streams accessible without authentication (`rtsp_auth_en=0`)
3. Most CGI GET endpoints work without authentication
4. Default credentials unchanged (admin:admin)
5. ONVIF services respond even when `onvif_en=0`

## Live-Changeable Streaming Settings

All via `POST http://camera/cgi-bin/param.cgi?post_media_video` with form-encoded body. No reboot needed.

| Parameter | Current | Options | Notes |
|---|---|---|---|
| `ndi_mode` | `264P60` | `264P60`, `264P50`, `265P60`, `265P50` | NDI codec/framerate |
| `protocol_1` | `H264` | `H264`, `H265` | Stream 1 codec |
| `protocol_2` | `H264` | `H264`, `H265` | Stream 2 codec |
| `bps_1` | `62000` | any integer (kbps) | Stream 1 bitrate |
| `bps_2` | `3000` | any integer (kbps) | Stream 2 bitrate |
| `fps_1` | `60` | `30`, `60` | Stream 1 framerate |
| `fps_2` | `30` | `30`, `60` | Stream 2 framerate |
| `gop_1` | `20` | any integer | Stream 1 GOP length |
| `rcmode_1` | `CBR` | `CBR` (likely `VBR` too) | Rate control mode |
| `size_1` | `PIC_HD1080` | (not fully tested) | Stream 1 resolution |
| `size_2` | `PIC_640_360` | (not fully tested) | Stream 2 resolution |

**Note**: RTSP clients may need to reconnect after codec changes. NDI mode is independent from RTSP codec settings.

## AI Tracking

```
Current mode: Off
Endpoint:     param.cgi?post_aimode&aimode=<mode>
Query:        param.cgi?get_aimode
```

AI tracking is available but should be used sparingly — cameras have been observed locking up/rebooting when tracking is active for extended periods. Likely a thermal or memory issue on the SoC. For production, use manual PTZ with presets.

## Reboot

Requires digest auth:
```bash
curl --digest -u admin:admin -X POST -d "cmd=reboot" \
  "http://camera/cgi-bin/param.cgi?post_reboot"
```
Camera returns in ~25 seconds.

## Firmware Updates

Current: `X8.03.15` (model `F2.V`)

Newer firmware available at [prisual.us/pages/downloads](https://www.prisual.us/pages/downloads):
- `20X_F2.V_V8.03.13_158M_250711_customer.img` — for model F2.V
- Claims: "support for FULL NDI & NDI HX3" and "NDI 6.1.1"
- Upgrade tool: Windows v2.9.1 or Mac upgrade tool
- **Caution**: Version number (`V8.03.13`) appears lower than current (`X8.03.15`). Contact Prisual support before flashing to confirm it's safe and not a downgrade.

## FreeD Protocol

Camera tracking protocol for virtual production / broadcast AR. Streams real-time camera position (pan, tilt, roll, zoom, focus) over IP so graphics engines can sync virtual cameras to physical ones.

```
freedoutput_en="0"    (disabled)
freeddestip="192.168.100.99"
freedctrlport="19147"
freeddataport="19148"
```

Not relevant for live streaming, but indicates the maturity of the OEM platform.

## Undocumented Features

1. **Port 81 = ONVIF** — not documented in Prisual materials, but fully functional with Device/Media/PTZ/Imaging services
2. **`get_ndi_info`** — NDI license status, discovery settings, Full NDI toggle
3. **`get_aimode`** — AI tracking support (Off, but present)
4. **`post_visca`** — CGI endpoint to change VISCA settings remotely
5. **Sony VISCA port 52381** — secondary VISCA port (Sony protocol variant)
6. **H.265 support** — both RTSP and NDI, not mentioned in basic documentation
7. **SRT streaming** — supported but not documented in basic materials
8. **FreeD tracking output** — disabled but present in config
9. **GB28181** — Chinese national surveillance standard protocol support
10. **5G modem support** — `get_5g_setting`, `get_5g_sys` endpoints exist (for cellular models?)
11. **Full VISCA inquiry set works** — exposure mode, gain, shutter, iris, brightness, white balance all queryable and settable
12. **Full NDI toggle** — `Full_NDI` parameter enables SpeedHQ/full NDI output (requires reboot)
13. **`get_system_conf`** — returns plaintext credentials without authentication

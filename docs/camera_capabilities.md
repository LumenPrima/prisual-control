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
Mode:     NDI HX2 (H.264 based, NOT full NDI)
Profile:  264P60 (H.264, 1080p60)
Channel:  "NDI HX2"
Full NDI: 0 (not supported — HX2 only)
License:  Active
Discovery: Disabled (uses manual registration)
Multicast: Disabled
```

**Note**: Full NDI (uncompressed/SpeedHQ) is NOT available. The camera only supports NDI|HX2, which is H.264 wrapped in NDI protocol. This adds latency compared to full NDI.

### Available Codecs
- H.264 (Main Profile and High Profile confirmed)
- No H.265/HEVC evidence found
- No SRT streaming active (srt_en=1 but no active connection)
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

## Undocumented Features

1. **Port 81 = ONVIF** — not documented in Prisual materials, but fully functional with Device/Media/PTZ/Imaging services
2. **`get_ndi_info`** — NDI license status, discovery settings
3. **`get_aimode`** — AI tracking support (Off, but present)
4. **`post_visca`** — CGI endpoint to change VISCA settings remotely
5. **Sony VISCA port 52381** — secondary VISCA port (Sony protocol variant)
6. **SRT streaming** — supported but not documented in basic materials
7. **FreeD tracking output** — disabled but present in config
8. **GB28181** — Chinese national surveillance standard protocol support
9. **5G modem support** — `get_5g_setting`, `get_5g_sys` endpoints exist (for cellular models?)
10. **Full VISCA inquiry set works** — exposure mode, gain, shutter, iris, brightness, white balance all queryable

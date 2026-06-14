# Auto-Tracking Roadmap

Direction for evolving the auto-tracking pipeline from the current naive PD loop
toward broadcast-grade behavior. Distilled from a review of established practice
and four open-source PTZ trackers (June 2026).

> Status: **planning / research**. Nothing here is implemented yet. This doc is a
> design target, not a record of what the code does.

## Where we are today

- **Python** (`tracker/track.py`, YOLO11m-pose) emits normalized aim points over UDP.
- **Go shim** (`cmd/shim/main.go`, `processAutoTrack`) runs a per-camera **PD
  controller** (`--tracker-kp`/`--tracker-kd`, deadband, stale-timeout) driving
  VISCA **pan/tilt VARIABLE (speed)** — continuous jog, not absolute moves.
- Framing targets are hardcoded fractions (`AIM_Y_FRAC`).

What's missing vs. broadcast-grade: prediction/feedforward, motion smoothing,
auto-zoom / shot-sizing, lead room, and false-positive gating.

## Key findings from the review

- **Architecture split.** The two serious projects (Frigate, NOLO) use
  absolute-position **"move-and-settle"** control; we use **continuous PD jog**.
  Their *PTZ-space math* (calibration, lead, coast, auto-zoom) ports cleanly;
  their *execution model* does not, and our jog model is better for smooth
  tracking. Cherry-pick the math, not the architecture.
- **One velocity estimate, three uses.** An EMA-smoothed aim-point velocity
  `v = (x[n] - x[n-1]) / dt` powers feedforward, latency prediction, and coasting.
  Build it once, reuse it three times.
- **Our gains are underdamped.** `Kp=10, Kd=1.5` gives damping ratio ζ ≈ 0.24.
  Critical damping wants `Kd ≈ 2√Kp ≈ 6.3`. If we see overshoot/hunting, raise
  Kd toward 4–6 — one-line, testable.

### Per-project keepers

| Project | Take | Skip |
|---|---|---|
| **Frigate** ([autotracking](https://docs.frigate.video/configuration/autotracking/), `frigate/ptz/autotrack.py`) | Self-refining motor calibration (`move_time = intercept + slope·angle`, OLS, continuously refit); velocity feedforward leading by measured motor latency; size-adaptive deadband `1 − log(subj/frame)`; auto-zoom with hysteresis (in <0.95×, out >1.1×) + velocity/edge gating | Blocking move-and-settle architecture (needs ONVIF MoveStatus; no VISCA equivalent); centers dead-on (no lead room) |
| **NOLO** ([doxx/NOLO](https://github.com/doxx/NOLO), `tracking/spatial_integration.go`) | Zoom-indexed pixels-per-unit tables w/ interpolation (one gain correct at every zoom); PTZ-space velocity lead + directional lead-room bias; coast-on-lost; edge-bias cold start; BUILDING→LOCK→SUPER LOCK frame-gating state machine; smooth the input, slew-limit the output | Absolute-position IDLE-gated sends (translate to slew-limited jog instead) |
| **AVStreamAI** ([repo](https://github.com/AVStreamAI/autotrack-any-ptz-camera)) | Head aim point w/ headroom (target 20% from top); explicit stop on no-detection | Proportional-only w/ MIN_SPEED floor (defeats fine centering); per-frame command flood |
| **AutoPTZ** ([repo](https://github.com/AutoPTZ/autoptz)) | Smoothstep easing `e = n²(3−2n)` on normalized error; aim ¼-box-height above center (headroom scales with subject size); uniform stop-on-every-exit | Per-frame flood, no rate limit; control law buried in Qt widget |

### Framing rules as math (from cinematography + pose-geometry sources)

- **Headroom / eyeline:** eyes/nose on the upper-third line (`y ≈ 0.333·H`),
  migrating up as the shot tightens so the scalp crops naturally. +~5% for overscan.
- **Lead room:** bias the subject off-center *opposite* their facing/motion,
  up to ~0.167·W. Derive facing from pose:
  `facing_offset = (nose_x − shoulder_mid_x) / shoulder_width`
  (>0 turned image-right → bias subject left). Fallback to ear-visibility sign
  near profile.
- **Shot sizes** as head-height fraction of frame (auto-zoom setpoints, tune on
  footage): CU ≈ 0.55, MCU ≈ 0.35, MS ≈ 0.22, MWS ≈ 0.14, WS ≈ 0.11.

### Control techniques

- **One Euro Filter** on the aim point (cutoff adapts to speed): `min_cutoff≈1 Hz`,
  `β` tuned up until fast-motion lag is acceptable. Beats fixed-α EMA at same cost.
- **PD + feedforward:** `u = Kp·e + Kd·ė + Kff·v`, start `Kff ≈ 1`.
- **Latency prediction (~100–130 ms):** error against `aim + v·t_latency`.
- **Deadband + hysteresis:** separate engage/disengage thresholds; scale deadband
  with zoom (shrink normalized deadband as you zoom in).
- **Coast:** dead-reckon at `v_last` for ~100–300 ms before the hard stale-stop.

## Phased plan

### Phase 0 — Free wins (tiny, low-risk)
- Retune/expose damping so Kd can reach ~5; confirm whether overshoot is real. *(main.go)*
- One Euro Filter on the aim point. *(track.py)*
- Explicit zero-speed on no-detection, alongside the stale timeout. *(main.go)*

### Phase 1 — Velocity trio (one estimate, three uses)
- EMA-smoothed aim-point velocity estimate. *(track.py emits, or main.go derives)*
- PD + feedforward `u = Kp·e + Kd·ė + Kff·v`. *(main.go)*
- Latency prediction against `aim + v·t_latency`. *(main.go)*
- Coast window before the hard stale-stop. *(main.go)*

### Phase 2 — Broadcast framing
- Lead room from pose facing direction. *(track.py + framing target in main.go)*
- Dynamic headroom replacing flat `AIM_Y_FRAC`. *(track.py / framing config)*
- Size-adaptive + zoom-scaled deadband with hysteresis. *(main.go)*

### Phase 3 — Auto-zoom (biggest new subsystem)
- One-time zoom-indexed pixels-per-unit / shot-size calibration (new probe). *(new cmd)*
- Auto-zoom loop targeting head-height fraction per shot size; wide deadband,
  slow rate, hysteresis; gate zoom-in on low velocity + zero edge-touching.
  Uses VISCA absolute zoom. *(main.go)*

### Phase 4 — Robustness & polish
- Lock state machine (N consecutive valid frames before motion). *(main.go / track.py)*
- Edge-bias cold-start direction guess. *(track.py)*
- Output slew-rate limit for cinematic ramps. *(main.go)*

### Phase 5 — Longer-term / optional
- Shot-mode FSM above the PD loop (Virtual Cinematographer idea).
- ~~Virtual PTZ from a 4K wide feed~~ — **not applicable**: the cameras are 1080p,
  which doesn't leave enough pixels to crop a usable PTZ window. Mechanical VISCA
  only.

### Phase 6 — Auto-director (subject selection + switching) — separate track
See "Auto-director layer" below. Builds on the multi-cam + vMix tally rig to
replace manual subject selection and automate camera switching. Largely
independent of the framing/control work in Phases 0–4; can proceed in parallel.

## Commercial behavior bar

What polished products (PTZOptics, OBSBOT, Panasonic, BirdDog, Huddly, Jabra,
Logitech, Cisco, plus the Zoom/Teams software directors) do by default — the bar
to match. No vendor publishes compositional geometry (headroom/crop ratios); they
do publish timing/hysteresis/deadband constants.

1. **Deadzone-first motion** — don't move until the subject exits a central
   inaction box (BirdDog: 0.1/0.2 normalized). *We match (`--tracker-deadband`).*
2. **Hold a chosen shot size**, pan/tilt to maintain it; re-zoom only in an
   explicit "dynamic" mode. Everyone ships named sizes (CU/Medium/Wide etc.).
3. **Compose, don't center** — bias up for headroom, against motion for lead room;
   expose the aim point as operator-movable (Panasonic/BirdDog model).
4. **Zoom-adaptive speed.** *We match.*
5. **Smooth S-curve motion**; offer Smooth-vs-Immediate (Jabra default Smooth).
6. **One coarse responsiveness knob**, not raw gains (Sensitivity Low/Med/High;
   OBSBOT Lazy→Crazy). Map onto Kp/Kd; keep raw flags for power users. Vendors
   warn high sensitivity → instability.
7. **Two-stage lost-subject recovery** — short coast → after 5–10 s, a graceful
   action: go wide to re-acquire (OBSBOT 10 s → 1.0×; BirdDog 5 s → zoom out) or
   recall a preset/home (Panasonic, PTZOptics, AVer). **Our clearest gap — we
   just stop.**
8. **Auto re-arm + re-ID on return** (don't grab the nearest person). *Our ReID
   covers this; make re-arm automatic.*
9. **Manual pick + sticky lock**; widen for groups; detect on head-and-shoulders
   not faces (Zoom precedent — matches our aim modes).
10. **Arm-tracking-on-live, role-based** (track whatever is program/preview).
    *We match (`t`/`p` toggles).*

Net: we're at/above the bar on deadzone, zoom-adaptive speed, PD smoothing, ReID,
and role-based arming. The two things every product does that we don't:
**(a) graceful multi-second lost-subject fallback** and **(b) compositional offset**
(headroom + lead room). Both map onto code we already have (`--tracker-stale-ms`,
the framing target). Lost-subject fallback should move into Phase 2.

## Camera-native capabilities we're not exploiting

Probed from `docs/Visca_Command_V2.1.pdf`, the VISCA CSV, `docs/HTTPCGIList.pdf`,
and `docs/camera_capabilities.md`. The Prisual TEN-20N Pro supports more than the
shim uses:

- **Absolute & relative pan/tilt position** (`81 01 06 02` / `06 03`, confirmed OK)
  — we only jog. Pan range ±0x0990 (±2448 steps), tilt +0x0510 / −0x01AF.
  Opens move-and-settle as an option (e.g. snap a preview cam to a framed target).
- **ACK / Completion sockets** (`z0 4y FF` accepted, `z0 5y FF` finished) — the
  protocol tells us when a move *completes*; the shim currently drains and discards
  these (`connection.go`). True move-done detection without polling.
- **Absolute zoom** (`81 01 04 47`, range 0x0000–0x4000 = 16384 steps) — `ZoomDirect`
  exists but is never called in the control loop. No documented zoom↔FOV table;
  must be measured (calibration).
- **Pan/Tilt max-speed inquiry** (`81 09 06 11`) — would replace hardcoded 24/20 caps.
- **Lens Block inquiry** (`81 09 7E 7E 00`) — zoom+focus+mode in one round-trip.
- **Pan/Tilt limit set, Home, Reset** (`81 01 06 07 / 04 / 05`, OK) — fence off
  rigging; Home as a lost-subject fallback target.
- **JPEG snapshot** (`/snapshot.jpg`, 1920×1080) — lightweight calibration frame
  source, no RTSP/NDI decode needed.
- **FreeD pose telemetry** (UDP pan/tilt/roll/zoom/focus, disabled) — could stream
  camera pose continuously instead of VISCA position polling.
- **Native humanoid + voice AI tracking** (`81 0A 11 54 …`, `post_aimode`) exists
  but **is known to lock up / reboot the camera on long runs** — this is *why* the
  project rolled its own YOLO tracker. Documented dead end; do not revisit.
- *Confirmed NG on this firmware over network:* preset-speed adjust, Dynamic Range,
  IR control (all return `90 60 02`).

## Empirical measurement plan (Phase 0.5 — do before tuning/auto-zoom)

Characterize the actual cameras' reactivity. Most is VISCA-timing-only; only the
pixels-per-unit table and end-to-end latency need a video frame (use `/snapshot.jpg`
+ the existing OpenCV in `track.py`). Prereq: add `PanTiltAbsolute`/`PanTiltRelative`
helpers to `internal/visca/commands.go`; extend `cmd/viscaprobe`.

| Measurement | Method | Needs video? |
|---|---|---|
| Pan deg/s ×24 speeds, Tilt deg/s ×20 speeds | timed jog between `81 09 06 12` reads; steps→deg via known span + one hand-measured sweep | No |
| Zoom counts/s ×8 speeds + full traverse time | `ZoomDirect` home → `ZoomVariable(p)` → sample `81 09 04 47` @50 ms | No |
| **Zoom-indexed pixels-per-pan/tilt-step** (the calibration table Phase 3 needs) | per zoom: `ZoomDirect`, grab snapshot, move known Δstep, grab snapshot, phase-correlate shift | Yes |
| Latency: command → encoder motion | timestamp jog write vs first position change in `81 09 06 12` | No |
| Latency: command → visible motion (end-to-end) | timestamp jog vs first inter-frame change in snapshot/RTSP | Yes |

## Auto-director layer (subject selection + switching)

Replaces manual subject selection and automates which camera is live. Principle
from every system reviewed: **selection + framing run continuously; cutting is a
separate, slower, rule-governed decision; never move a PTZ that's on program.**

- **Active-speaker detection** is the highest-leverage addition. **Light-ASD /
  LR-ASD** (~94% mAP, ~1M params, ≤4.5 ms/face, 223 FPS) runs comfortably on the
  A4000 alongside YOLO-pose + vMix. Emit a talking-probability per track over the
  existing UDP channel. TalkNet is a heavier higher-accuracy fallback.
- **Candidate scoring (no audio)** — port the patent heuristic onto our keypoints:
  `poseScore = 4·nose + 2·min(eyes) + min(ears)` (confident kpts only), scaled by
  size (distance) and center-weighting; add raised-hand (wrist-y above shoulder/nose)
  for gesture nomination, and a manual-pin override.
- **Selection hysteresis** — new leader must beat current by a margin for a min
  dwell (~2–5 s); ignore sub-deadband target moves; manual pin overrides, auto-resumes.
- **Group vs single** — designate one camera as a locked wide/group shot (union
  bbox, rule-of-thirds); PTZ cams track individuals. Go tight when one speaker
  dominates; return to wide when nobody/everybody speaks (Cisco/Poly rules).
- **Director FSM → vMix** — pre-arm the best-framed off-program cam on **preview**,
  let its PTZ settle, then **cut/fade** only when timing rules pass: min shot
  ~2–4 s, max ~7–10 s, cut-on-speaker-change (>~1 s), suppress if the new speaker
  is already in shot, avoid back-to-back cuts. Subscribe to `SUBSCRIBE TALLY` so
  both director and tracker freeze reframing on the live cam.
- **Mic-array DOA** (GCC-PHAT / SRP-PHAT via pyroomacoustics, VAD-gated) as a
  fallback/tie-breaker for off-camera or occluded speakers.

Build order: (1) candidate scoring + selection w/ hysteresis using existing pose
data (replaces the manual keypress, low-risk); (2) Light-ASD over UDP (biggest
quality jump); (3) director FSM driving vMix preview/program (start conservative —
long min-shot, fades); (4) group/wide fallback + gesture nomination; (5) optional
DOA and a learned shot scorer.

## Sources

- Frigate autotracking: https://docs.frigate.video/configuration/autotracking/
- NOLO: https://github.com/doxx/NOLO
- AVStreamAI: https://github.com/AVStreamAI/autotrack-any-ptz-camera
- AutoPTZ: https://github.com/AutoPTZ/autoptz
- Headroom: https://en.wikipedia.org/wiki/Headroom_(photographic_framing)
- One Euro Filter: https://jaantollander.com/post/noise-filtering-using-one-euro-filter/
- Virtual Cinematographer (He/Cohen/Salesin, SIGGRAPH 1996): https://dl.acm.org/doi/10.1145/237170.237259
- L1 Optimal Camera Paths (Grundmann/Kwatra/Essa, CVPR 2011)
- CineMPC (Pueyo et al., 2021): https://arxiv.org/abs/2104.03634
- Light-ASD (active speaker, CVPR 2023): https://github.com/Junhua-Liao/Light-ASD · https://arxiv.org/abs/2303.04439
- LR-ASD (IJCV 2025): https://github.com/Junhua-Liao/LR-ASD
- TalkNet-ASD: https://github.com/TaoRuijie/TalkNet-ASD · https://arxiv.org/abs/2107.06592
- EditIQ (auto-editing cut rules, 2025): https://arxiv.org/abs/2502.02172
- iCam automated lecture director (Microsoft Research)
- Selection/hysteresis patents: US12335608B2, US12014562B2, US10986308B2
- pyroomacoustics DOA: https://pyroomacoustics.readthedocs.io/
- vMix TCP API: https://www.vmix.com/help26/TCPAPI.html · "don't PTZ on-air": https://forums.vmix.com/posts/t9517
- Local camera docs: docs/Visca_Command_V2.1.pdf, docs/HTTPCGIList.pdf, docs/camera_capabilities.md, the VISCA CSV

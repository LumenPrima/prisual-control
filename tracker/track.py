"""RTSP or NDI -> YOLO -> overlay window(s) with persistent-ID tracking + subject lock.

Usage (RTSP):
    python track.py 192.168.1.50 --sub
    python track.py 192.168.1.50 192.168.1.51 --sub                 # both at once
    python track.py rtsp://192.168.1.50:554/2 rtsp://192.168.1.51:554/2
    python track.py 192.168.1.50 192.168.1.51 --sub --classes person
    python track.py 192.168.1.50 --model yolov8m.pt --conf 0.4

Usage (NDI — works while vMix is also receiving NDI from the same cameras):
    python track.py --list-ndi                                      # discover sources
    python track.py --ndi "CAMERA1"                                 # substring match
    python track.py --ndi "CAMERA1" "CAMERA2" --classes person

Runtime hotkeys (any window with focus; track keys apply to ACTIVE stream):
    tab     cycle active stream (green title bar = active)
    1-9     latch onto Nth visible track in the active stream's list
            (also auto-propagates the lock to other streams via appearance match
            unless --no-cross-lock; press 'x' to retrigger manually)
    x       propagate active stream's lock to other streams (appearance match)
    f       cycle aim landmark (shoulders -> nose -> torso -> ...)
    0       release lock on active stream
    a       show ALL classes (global filter)
    n       show NONE
    p       person-only
    s       print track inventory to terminal
    q/esc   quit

When a stream has a lock, the normalized (dx, dy) error from frame center to
target box center prints to the terminal at ~5Hz — that's the signal that will
feed VISCA pan/tilt once wired to the shim.
"""

from __future__ import annotations

import argparse
import json
import os
import socket
import sys
import threading
import time
from collections import deque

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))

import cv2
import numpy as np
import torch
from ultralytics import YOLO

RTSP_PATHS_MAIN = ["/1", "/live/main", "/stream1", "/cam/realmonitor?channel=1&subtype=0", "/h264"]
RTSP_PATHS_SUB = ["/2", "/live/sub", "/stream2", "/cam/realmonitor?channel=1&subtype=1"]

TRACK_STALE_S = 30.0   # forget per-track state not updated for this long
ERR_PRINT_HZ = 5.0     # rate-limit terminal error printing per stream
CROSS_LOCK_THRESHOLD = 0.5  # min cosine similarity to auto-lock across streams (OSNet/MSMT17 typical: 0.4-0.6)

# COCO keypoint indices (yolo11 pose returns 17 in this order).
KPT_NOSE = 0
KPT_L_SHOULDER, KPT_R_SHOULDER = 5, 6
KPT_L_HIP, KPT_R_HIP = 11, 12
# Skeleton edges (pairs of keypoint indices) for overlay rendering.
SKELETON = [
    (5, 7), (7, 9),      # left arm
    (6, 8), (8, 10),     # right arm
    (5, 6),              # shoulders
    (5, 11), (6, 12),    # torso sides
    (11, 12),            # hips
    (11, 13), (13, 15),  # left leg
    (12, 14), (14, 16),  # right leg
    (0, 5), (0, 6),      # nose -> shoulders (head/neck)
]
AIM_MODES = ("shoulders", "nose", "torso")  # cycle order on 'f' press

# Per-mode target Y as a fraction of frame height from the top. The error
# signal is computed relative to this row rather than the geometric midpoint,
# so the aim landmark settles in the upper portion of frame (standard
# broadcast head-and-shoulders / rule-of-thirds framing) instead of dead
# center, which would leave too much headroom or none at all.
AIM_Y_FRAC = {
    "nose": 0.33,       # eyes near upper-third line
    "shoulders": 0.40,  # head-and-shoulders broadcast framing
    "torso": 0.50,      # body center -> frame center
}


def compute_aim(box, kpts_xy, kpts_conf, mode: str, kpt_conf_min: float) -> tuple[int, int]:
    """Return (cx, cy) aim point for one detection.

    Falls back to bbox top-quarter midpoint when the chosen landmark's
    keypoint confidence is below threshold, or when the model didn't
    produce keypoints at all.
    """
    x1, y1, x2, y2 = box
    if kpts_xy is not None and kpts_conf is not None:
        if mode == "nose":
            if kpts_conf[KPT_NOSE] >= kpt_conf_min:
                kp = kpts_xy[KPT_NOSE]
                return int(kp[0]), int(kp[1])
        elif mode == "shoulders":
            cl, cr = float(kpts_conf[KPT_L_SHOULDER]), float(kpts_conf[KPT_R_SHOULDER])
            if cl >= kpt_conf_min and cr >= kpt_conf_min:
                ll = kpts_xy[KPT_L_SHOULDER]
                rr = kpts_xy[KPT_R_SHOULDER]
                return int((float(ll[0]) + float(rr[0])) / 2), int((float(ll[1]) + float(rr[1])) / 2)
        elif mode == "torso":
            ids = [KPT_L_SHOULDER, KPT_R_SHOULDER, KPT_L_HIP, KPT_R_HIP]
            good = [(kpts_xy[i], float(kpts_conf[i])) for i in ids if float(kpts_conf[i]) >= kpt_conf_min]
            if len(good) >= 3:
                cx = sum(float(p[0]) for p, _ in good) / len(good)
                cy = sum(float(p[1]) for p, _ in good) / len(good)
                return int(cx), int(cy)
    # Fallback: top quarter of bbox — closer to where a head sits than bbox center
    cx = (x1 + x2) // 2
    cy = int(y1 + (y2 - y1) * 0.25)
    return cx, cy


class CrossReid:
    """Lazy-loaded OSNet (or other boxmot backbone) for cross-stream person ReID.

    OSNet trained on MSMT17 produces embeddings where the same person from different
    cameras lands near each other in cosine space — far better than BoT-SORT's native
    detector features (which scored ~0.1 in cross-stream tests, useless).
    """

    def __init__(self, device: str, weights: str | None = None):
        self.device = device
        self.weights = weights  # None = boxmot's default (osnet_x0_25_msmt17.pt)
        self._reid = None

    def _ensure(self) -> None:
        if self._reid is not None:
            return
        from boxmot.reid.core.reid import ReID
        print("[reid] loading ReID backbone (auto-downloads weights on first use)...", flush=True)
        self._reid = ReID(weights=self.weights, device=self.device)
        print("[reid] loaded", flush=True)

    def embed(self, frame: np.ndarray, boxes_xyxy: list) -> np.ndarray:
        """Return Nx<d> L2-normalized embeddings, one per box. Empty if no boxes."""
        boxes = np.asarray(boxes_xyxy, dtype=np.float32).reshape(-1, 4)
        if len(boxes) == 0:
            return np.empty((0, 0), dtype=np.float32)
        self._ensure()
        payload = self._reid.preprocess(frame, boxes=boxes)
        feats = self._reid.process(payload)
        return self._reid.postprocess(feats)


def candidate_urls(target: str, sub: bool) -> list[str]:
    if target.startswith("rtsp://"):
        return [target]
    paths = RTSP_PATHS_SUB if sub else RTSP_PATHS_MAIN
    return [f"rtsp://{target}:554{p}" for p in paths]


def open_stream(target: str, sub: bool) -> tuple[cv2.VideoCapture, str]:
    import os
    os.environ.setdefault("OPENCV_FFMPEG_CAPTURE_OPTIONS", "rtsp_transport;tcp")

    for url in candidate_urls(target, sub):
        print(f"[probe {target}] trying {url}", flush=True)
        cap = cv2.VideoCapture(url, cv2.CAP_FFMPEG)
        if not cap.isOpened():
            cap.release()
            continue
        ok, _ = cap.read()
        if ok:
            print(f"[probe {target}] connected: {url}", flush=True)
            return cap, url
        cap.release()
    raise SystemExit(f"could not open any RTSP URL for {target!r}")


def parse_classes(arg, name_to_id: dict[str, int]) -> set[int] | None:
    if not arg:
        return None
    out: set[int] = set()
    flat: list[str] = []
    for tok in arg:
        flat.extend(t.strip() for t in tok.split(",") if t.strip())
    for tok in flat:
        try:
            out.add(int(tok))
        except ValueError:
            tid = name_to_id.get(tok.lower())
            if tid is None:
                raise SystemExit(f"unknown class: {tok!r}. Try --classes person,car,laptop")
            out.add(tid)
    return out


class Stream:
    """One RTSP camera: background reader, own YOLO+tracker instance, lock state.

    Each Stream gets its own YOLO so ultralytics' persist=True tracker state
    doesn't get mixed across cameras.
    """

    def __init__(self, target: str, sub: bool, win_x: int, model_path: str):
        self.target = target
        self.sub = sub
        self.cap, self.url = open_stream(target, sub)
        self.model = YOLO(model_path)
        self.win = f"tracker [{target}]"
        cv2.namedWindow(self.win, cv2.WINDOW_NORMAL)
        cv2.moveWindow(self.win, win_x, 60)
        self.lock = threading.Lock()
        self.latest: object | None = None
        self.alive = True
        self.thread = threading.Thread(target=self._reader, daemon=True)
        self.thread.start()
        self.frame_times: deque[float] = deque(maxlen=30)
        self.last_t = time.perf_counter()

        # tracking state
        self.selected_id: int | None = None
        self.visible_ids: list[int] = []         # eligible after class filter, slot order
        self.last_seen: dict[int, float] = {}
        self.last_box: dict[int, tuple[int, int, int, int]] = {}
        self.last_class: dict[int, int] = {}
        self.last_conf: dict[int, float] = {}
        self.last_err_print: float = 0.0
        self.last_frame: np.ndarray | None = None    # clean (un-annotated) latest frame for ReID crops
        self.last_aim: dict[int, tuple[int, int]] = {}     # aim point per track
        self.last_kpts: dict[int, tuple] = {}              # (kpts_xy, kpts_conf) per track for drawing

    def _reader(self) -> None:
        while self.alive:
            ok, frame = self.cap.read()
            if not ok:
                print(f"[warn {self.target}] read failed, reconnecting...", flush=True)
                self.cap.release()
                time.sleep(0.5)
                try:
                    self.cap, self.url = open_stream(self.target, self.sub)
                except SystemExit as e:
                    print(f"[warn {self.target}] {e}, retrying in 2s", flush=True)
                    time.sleep(2.0)
                continue
            with self.lock:
                self.latest = frame

    def grab(self):
        with self.lock:
            f = self.latest
            self.latest = None
        return f

    def fps(self) -> float:
        return len(self.frame_times) / sum(self.frame_times) if self.frame_times else 0.0

    def tick(self) -> None:
        now = time.perf_counter()
        self.frame_times.append(now - self.last_t)
        self.last_t = now

    def close(self) -> None:
        self.alive = False
        self.cap.release()
        cv2.destroyWindow(self.win)


def find_ndi_source(finder, pattern: str, timeout: float = 5.0):
    """Wait up to `timeout` for an NDI source whose name contains `pattern` (case-insensitive).

    Returns the cyndilib Source object or None.
    """
    deadline = time.time() + timeout
    pat = pattern.lower()
    while time.time() < deadline:
        for src in finder.iter_sources():
            if pat in src.name.lower():
                return src
        time.sleep(0.2)
    return None


class NdiStream:
    """One NDI source: background framesync reader, own YOLO+tracker instance, lock state.

    Mirrors Stream's public surface (target, url, win, lock, latest, alive, model,
    frame_times, last_t, selected_id, visible_ids, last_seen/box/class/conf, last_err_print,
    grab/fps/tick/close) so update_tracks/draw_overlay/print_error work unchanged.
    """

    def __init__(self, source, win_x: int, model_path: str):
        from cyndilib.receiver import Receiver
        from cyndilib.video_frame import VideoFrameSync
        from cyndilib.wrapper import RecvColorFormat, RecvBandwidth

        self.target = source.name
        self.url = source.name
        self.receiver = Receiver(
            color_format=RecvColorFormat.BGRX_BGRA,
            bandwidth=RecvBandwidth.highest,
            recv_name="prisual-tracker",
        )
        self.video_frame = VideoFrameSync()
        self.receiver.frame_sync.set_video_frame(self.video_frame)
        self.receiver.set_source(source)

        self.model = YOLO(model_path)
        self.win = f"tracker [{source.name}]"
        cv2.namedWindow(self.win, cv2.WINDOW_NORMAL)
        cv2.moveWindow(self.win, win_x, 60)

        self.lock = threading.Lock()
        self.latest: object | None = None
        self.alive = True
        self.thread = threading.Thread(target=self._reader, daemon=True)
        self.thread.start()
        self.frame_times: deque[float] = deque(maxlen=30)
        self.last_t = time.perf_counter()

        self.selected_id: int | None = None
        self.visible_ids: list[int] = []
        self.last_seen: dict[int, float] = {}
        self.last_box: dict[int, tuple[int, int, int, int]] = {}
        self.last_class: dict[int, int] = {}
        self.last_conf: dict[int, float] = {}
        self.last_err_print: float = 0.0
        self.last_frame: np.ndarray | None = None    # clean (un-annotated) latest frame for ReID crops
        self.last_aim: dict[int, tuple[int, int]] = {}     # aim point per track
        self.last_kpts: dict[int, tuple] = {}              # (kpts_xy, kpts_conf) per track for drawing

    def _reader(self) -> None:
        while self.alive:
            self.receiver.frame_sync.capture_video()
            xres = self.video_frame.xres
            yres = self.video_frame.yres
            if xres == 0 or yres == 0:
                time.sleep(0.01)
                continue
            arr = self.video_frame.get_array()
            try:
                frame_bgra = np.asarray(arr).reshape((yres, xres, 4))
            except ValueError:
                continue
            frame_bgr = np.ascontiguousarray(frame_bgra[:, :, :3])
            with self.lock:
                self.latest = frame_bgr

    def grab(self):
        with self.lock:
            f = self.latest
            self.latest = None
        return f

    def fps(self) -> float:
        return len(self.frame_times) / sum(self.frame_times) if self.frame_times else 0.0

    def tick(self) -> None:
        now = time.perf_counter()
        self.frame_times.append(now - self.last_t)
        self.last_t = now

    def close(self) -> None:
        self.alive = False
        try:
            self.receiver.disconnect()
        except Exception:
            pass
        cv2.destroyWindow(self.win)


def update_tracks(s, results, allowed: set[int] | None,
                  aim_mode: str, kpt_conf_min: float) -> None:
    """Fold a tracking result into the stream's per-id state.

    All tracks update last_seen so a lock survives even when class filter hides it;
    only filter-eligible tracks land in visible_ids (the 1-9 slot list).

    Pose models additionally produce per-track keypoints; the chosen aim point
    (driven by aim_mode) is computed and stored as last_aim[tid] for the
    downstream error calculation.
    """
    boxes = results.boxes
    kp_obj = getattr(results, "keypoints", None)
    has_kpts = (
        kp_obj is not None
        and getattr(kp_obj, "xy", None) is not None
        and len(kp_obj.xy) > 0
    )
    kpts_xy_all = kp_obj.xy.cpu().numpy() if has_kpts else None
    kpts_conf_all = (
        kp_obj.conf.cpu().numpy()
        if has_kpts and getattr(kp_obj, "conf", None) is not None
        else None
    )

    now = time.perf_counter()
    eligible: list[int] = []

    if boxes is not None and boxes.id is not None and len(boxes):
        ids = boxes.id.cpu().numpy().astype(int)
        xyxy = boxes.xyxy.cpu().numpy()
        cls = boxes.cls.cpu().numpy().astype(int)
        conf = boxes.conf.cpu().numpy()
        for i, ((x1, y1, x2, y2), tid, k, c) in enumerate(zip(xyxy, ids, cls, conf)):
            tid = int(tid)
            box = (int(x1), int(y1), int(x2), int(y2))
            s.last_seen[tid] = now
            s.last_box[tid] = box
            s.last_class[tid] = int(k)
            s.last_conf[tid] = float(c)

            kxy = kpts_xy_all[i] if kpts_xy_all is not None and i < len(kpts_xy_all) else None
            kc = kpts_conf_all[i] if kpts_conf_all is not None and i < len(kpts_conf_all) else None
            s.last_aim[tid] = compute_aim(box, kxy, kc, aim_mode, kpt_conf_min)
            if kxy is not None:
                s.last_kpts[tid] = (kxy, kc)
            else:
                s.last_kpts.pop(tid, None)

            if allowed is None or int(k) in allowed:
                eligible.append(tid)

    s.visible_ids = sorted(eligible)[:9]

    stale = [tid for tid, t in s.last_seen.items() if now - t > TRACK_STALE_S]
    for tid in stale:
        s.last_seen.pop(tid, None)
        s.last_box.pop(tid, None)
        s.last_class.pop(tid, None)
        s.last_conf.pop(tid, None)
        s.last_aim.pop(tid, None)
        s.last_kpts.pop(tid, None)
        if s.selected_id == tid:
            print(f"[{s.target}] lock #{tid} expired (stale)", flush=True)
            s.selected_id = None


def _safe_crop(frame: np.ndarray, box: tuple[int, int, int, int]) -> np.ndarray:
    h, w = frame.shape[:2]
    x1, y1, x2, y2 = box
    x1 = max(0, min(w - 1, int(x1)))
    y1 = max(0, min(h - 1, int(y1)))
    x2 = max(x1 + 1, min(w, int(x2)))
    y2 = max(y1 + 1, min(h, int(y2)))
    return frame[y1:y2, x1:x2].copy()


def _sanitize(name: str) -> str:
    return "".join(c if c.isalnum() or c in "-_." else "_" for c in name)


def propagate_lock(src, others, threshold: float, reid: CrossReid,
                   debug_dir: str | None = None) -> None:
    """When src has a locked track, find each other stream's best ReID match and lock it.

    Computes OSNet (or whichever boxmot backbone) embeddings on demand for
    the source bbox and each candidate bbox in destination streams. Falls
    back to "no candidates" silently if any stream lacks a clean frame yet.
    """
    if src.selected_id is None or src.last_frame is None:
        return
    src_box = src.last_box.get(src.selected_id)
    if src_box is None:
        print(f"[{src.target}] no box for #{src.selected_id}; cross-lock skipped", flush=True)
        return
    src_embs = reid.embed(src.last_frame, [src_box])
    if len(src_embs) == 0:
        return
    src_emb = src_embs[0]

    ts = time.strftime("%Y%m%d_%H%M%S")
    if debug_dir is not None:
        os.makedirs(debug_dir, exist_ok=True)
        src_path = os.path.join(
            debug_dir,
            f"{ts}_{_sanitize(src.target)}_id{src.selected_id:03d}_SRC.png",
        )
        cv2.imwrite(src_path, _safe_crop(src.last_frame, src_box))
        print(f"[debug-reid] wrote {src_path}", flush=True)

    for dst in others:
        if dst.last_frame is None:
            continue
        cand_ids = [tid for tid in dst.visible_ids if tid in dst.last_box]
        cand_boxes = [dst.last_box[tid] for tid in cand_ids]
        if not cand_boxes:
            print(f"[{dst.target}] no candidates for cross-lock", flush=True)
            continue
        cand_embs = reid.embed(dst.last_frame, cand_boxes)
        if len(cand_embs) == 0:
            continue
        sims = cand_embs @ src_emb  # both rows are L2-normalized
        order = np.argsort(-sims)  # descending
        best_idx = int(order[0])
        best_sim = float(sims[best_idx])
        best_id = cand_ids[best_idx]
        runner_sim = float(sims[int(order[1])]) if len(order) > 1 else float("-inf")
        gap = best_sim - runner_sim if runner_sim != float("-inf") else float("inf")

        print(f"[{dst.target}] reid scores vs [{src.target}] #{src.selected_id}:", flush=True)
        for rank, idx in enumerate(order):
            idx = int(idx)
            tag = "  <-- WIN" if idx == best_idx else ""
            print(f"    #{cand_ids[idx]:>3}  sim={float(sims[idx]):+.3f}{tag}", flush=True)

        if debug_dir is not None:
            for rank, idx in enumerate(order):
                idx = int(idx)
                tid = cand_ids[idx]
                sim = float(sims[idx])
                tag = "_WIN" if idx == best_idx else ""
                crop_path = os.path.join(
                    debug_dir,
                    f"{ts}_{_sanitize(dst.target)}_rank{rank}_id{tid:03d}_sim{sim:+.3f}{tag}.png",
                )
                cv2.imwrite(crop_path, _safe_crop(dst.last_frame, dst.last_box[tid]))
            print(f"[debug-reid] wrote {len(order)} candidate crops for {dst.target}", flush=True)

        gap_str = f"gap={gap:+.3f}" if gap != float("inf") else "gap=inf (no runner-up)"
        if best_sim >= threshold:
            dst.selected_id = best_id
            print(f"[{dst.target}] reid cross-locked #{best_id} (sim={best_sim:.2f}, {gap_str})",
                  flush=True)
        else:
            print(f"[{dst.target}] best reid match #{best_id} sim={best_sim:.2f} < threshold {threshold:.2f} ({gap_str})",
                  flush=True)


def draw_overlay(frame, s, device: str, allowed: set[int] | None,
                 names: dict[int, str], is_active: bool, aim_mode: str = "shoulders") -> None:
    h, w = frame.shape[:2]
    fcx = w // 2
    fcy = int(AIM_Y_FRAC.get(aim_mode, 0.5) * h)  # target row for current mode

    to_draw = list(s.visible_ids)
    if (s.selected_id is not None
            and s.selected_id in s.last_box
            and s.selected_id not in to_draw):
        to_draw.append(s.selected_id)

    for tid in to_draw:
        x1, y1, x2, y2 = s.last_box[tid]
        k = s.last_class[tid]
        c = s.last_conf[tid]
        is_sel = (tid == s.selected_id)
        col = (0, 0, 255) if is_sel else (0, 255, 0)
        thick = 3 if is_sel else 2
        label = f"#{tid} {names.get(k, k)} {c:.2f}"
        cv2.rectangle(frame, (x1, y1), (x2, y2), col, thick)
        (tw, th), _ = cv2.getTextSize(label, cv2.FONT_HERSHEY_SIMPLEX, 0.5, 1)
        cv2.rectangle(frame, (x1, y1 - th - 6), (x1 + tw + 4, y1), col, -1)
        cv2.putText(frame, label, (x1 + 2, y1 - 4),
                    cv2.FONT_HERSHEY_SIMPLEX, 0.5, (0, 0, 0), 1, cv2.LINE_AA)

        # Skeleton (light) when keypoints are available
        kpts = s.last_kpts.get(tid)
        if kpts is not None:
            kxy, kc = kpts
            skel_col = (180, 180, 180) if not is_sel else (200, 200, 255)
            for a, b in SKELETON:
                if kc is not None and (float(kc[a]) < 0.3 or float(kc[b]) < 0.3):
                    continue
                pa = (int(kxy[a][0]), int(kxy[a][1]))
                pb = (int(kxy[b][0]), int(kxy[b][1]))
                cv2.line(frame, pa, pb, skel_col, 1, cv2.LINE_AA)

        # Aim point — yellow crosshair, larger for the locked track
        aim = s.last_aim.get(tid)
        if aim is not None:
            size = 18 if is_sel else 10
            cv2.drawMarker(frame, aim, (0, 255, 255), cv2.MARKER_CROSS, size, 2 if is_sel else 1)

    cv2.drawMarker(frame, (fcx, fcy), (255, 255, 255), cv2.MARKER_CROSS, 16, 1)

    lock_status = ""
    if s.selected_id is not None:
        aim = s.last_aim.get(s.selected_id)
        if aim is not None:
            cv2.line(frame, (fcx, fcy), aim, (0, 0, 255), 2)
            lock_status = f"LOCK #{s.selected_id}"
        else:
            age = time.perf_counter() - s.last_seen.get(s.selected_id, time.perf_counter())
            lock_status = f"LOCK #{s.selected_id} LOST {age:.1f}s"

    bar_col = (0, 180, 0) if is_active else (60, 60, 60)
    cv2.rectangle(frame, (0, 0), (w, 22), bar_col, -1)
    filt_desc = "all" if allowed is None else (
        "none" if not allowed else ",".join(sorted(names.get(i, str(i)) for i in allowed))
    )
    hud = (f"{'>' if is_active else ' '} {device} | {w}x{h} | {s.fps():5.1f} fps "
           f"| filter: {filt_desc} | aim: {aim_mode}")
    if lock_status:
        hud += f" | {lock_status}"
    cv2.putText(frame, hud, (6, 16), cv2.FONT_HERSHEY_SIMPLEX, 0.5, (255, 255, 255), 1, cv2.LINE_AA)

    lines = ["tracks (1-9 lock, 0 release):"]
    if not s.visible_ids:
        lines.append("  (no tracks)")
    for i, tid in enumerate(s.visible_ids, start=1):
        k = s.last_class[tid]
        c = s.last_conf[tid]
        marker = "*" if tid == s.selected_id else " "
        lines.append(f"  {i}{marker} #{tid} {names.get(k, str(k))} {c:.2f}")

    line_h = 16
    box_h = line_h * len(lines) + 8
    box_w = 260
    y0 = h - box_h - 6
    cv2.rectangle(frame, (6, y0), (6 + box_w, y0 + box_h), (0, 0, 0), -1)
    for i, line in enumerate(lines):
        cv2.putText(frame, line, (10, y0 + line_h * (i + 1)),
                    cv2.FONT_HERSHEY_SIMPLEX, 0.42, (200, 200, 200), 1, cv2.LINE_AA)


class TrackerSink:
    """UDP emitter for per-track aim-point packets consumed by the Go shim.

    Fire-and-forget. Packets are JSON:
        {"cam": str, "id": int, "mode": str,
         "ax": float, "ay": float, "ts": float}

    ax/ay are the aim point in normalized frame coords [0, 1] (top-left
    origin). The shim subtracts its own per-mode framing target before
    feeding the PD controller — this keeps framing live-adjustable in the
    shim TUI without bouncing config back to the tracker.
    """

    def __init__(self, host: str, port: int):
        self.addr = (host, port)
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        self.sock.setblocking(False)

    def send(self, cam: str, tid: int, mode: str, ax: float, ay: float) -> None:
        payload = json.dumps({
            "cam": cam, "id": tid, "mode": mode,
            "ax": ax, "ay": ay, "ts": time.time(),
        }).encode("ascii")
        try:
            self.sock.sendto(payload, self.addr)
        except OSError:
            pass  # buffer full or transient socket error — next tick covers it


def print_error(s, w: int, h: int, aim_mode: str = "shoulders",
                sink: TrackerSink | None = None) -> None:
    """Emit the locked track's aim point (and log a local debug snapshot).

    UDP carries the *absolute* aim point in normalized frame coords plus the
    current aim mode. The shim subtracts its own framing target before
    feeding the PD controller so the framing target can be live-edited in
    the shim TUI without bouncing config back here.

    Terminal print is rate-limited to ERR_PRINT_HZ and shows the err
    relative to the tracker's *default* AIM_Y_FRAC purely for debugging —
    it may diverge from the shim's actual framing target.
    """
    if s.selected_id is None or s.selected_id not in s.last_aim:
        return
    tcx, tcy = s.last_aim[s.selected_id]
    ax = tcx / w if w > 0 else 0.5
    ay = tcy / h if h > 0 else 0.5
    if sink is not None:
        sink.send(s.target, s.selected_id, aim_mode, ax, ay)
    now = time.perf_counter()
    if now - s.last_err_print < 1.0 / ERR_PRINT_HZ:
        return
    s.last_err_print = now
    target_y = AIM_Y_FRAC.get(aim_mode, 0.5) * h
    dx = (tcx - w / 2) / (w / 2)
    dy = (tcy - target_y) / (h / 2)
    print(f"[{s.target} #{s.selected_id}] aim=({ax:.3f},{ay:.3f}) err≈({dx:+.3f}, {dy:+.3f})",
          flush=True)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("targets", nargs="*",
                    help="one or more RTSP URLs/IPs (default) or NDI source name substrings (with --ndi)")
    ap.add_argument("--ndi", action="store_true",
                    help="interpret targets as NDI source name substrings instead of RTSP. "
                         "Works alongside vMix consuming the same sources.")
    ap.add_argument("--list-ndi", action="store_true",
                    help="discover NDI sources on the network for 3s and exit")
    ap.add_argument("--model", default="yolo11m-pose.pt",
                    help="ultralytics model (auto-downloads). Use a *-pose model for stable "
                         "aim-point tracking via keypoints; detection-only models still work "
                         "but fall back to bbox top-quarter for the aim point.")
    ap.add_argument("--aim-mode", default="shoulders", choices=["nose", "shoulders", "torso"],
                    help="initial aim landmark; 'f' at runtime cycles modes")
    ap.add_argument("--kpt-conf", type=float, default=0.4,
                    help="minimum keypoint confidence to trust a landmark; below this, falls back to bbox")
    ap.add_argument("--conf", type=float, default=0.35, help="confidence threshold")
    ap.add_argument("--imgsz", type=int, default=640, help="inference size")
    ap.add_argument("--classes", nargs="*", default=None,
                    help="initial filter — names or ids, comma/space separated. Toggle at runtime.")
    ap.add_argument("--sub", action="store_true", help="use RTSP sub-stream when probing")
    ap.add_argument("--device", default=None, help="torch device, default: cuda if available")
    ap.add_argument("--half", action="store_true", help="FP16 inference (~2x faster on Ampere+, tiny accuracy hit)")
    ap.add_argument("--win-step", type=int, default=720, help="x offset between windows (px)")
    ap.add_argument("--tracker", default=os.path.join(SCRIPT_DIR, "botsort_reid.yaml"),
                    help="ultralytics tracker config; default is BoT-SORT + native-feature ReID "
                         "with track_buffer=300. Pass 'bytetrack.yaml' for motion-only/faster.")
    ap.add_argument("--cross-lock-threshold", type=float, default=CROSS_LOCK_THRESHOLD,
                    help="cosine similarity threshold for auto cross-stream lock (0..1)")
    ap.add_argument("--no-cross-lock", action="store_true",
                    help="disable cross-stream ReID lock entirely")
    ap.add_argument("--reid-model", default=None,
                    help="path or boxmot-known name of ReID weights; default = osnet_x0_25_msmt17.pt")
    ap.add_argument("--debug-reid", default=None,
                    help="directory to dump src + candidate crops with sim scores each cross-lock attempt")
    ap.add_argument("--track-out", default=None,
                    help="HOST:PORT to UDP-emit per-track error JSON (e.g. 127.0.0.1:9876). "
                         "Consumed by the Go shim for auto-pan/tilt.")
    args = ap.parse_args()

    if args.list_ndi:
        from cyndilib.finder import Finder
        finder = Finder()
        finder.open()
        print("[ndi] discovering sources for 10s...", flush=True)
        time.sleep(10.0)
        names = finder.get_source_names()
        if not names:
            print("[ndi] no sources found", flush=True)
        for name in names:
            print(f"  {name}", flush=True)
        finder.close()
        return 0

    if not args.targets:
        ap.error("at least one target is required (or --list-ndi to discover)")

    device = args.device or ("cuda:0" if torch.cuda.is_available() else "cpu")
    if device.startswith("cuda") and not torch.cuda.is_available():
        print("[warn] cuda requested but not available, falling back to cpu", flush=True)
        device = "cpu"

    print(f"[init] device={device} model={args.model} conf={args.conf} imgsz={args.imgsz}", flush=True)
    print(f"[init] streams: {len(args.targets)} -> {args.targets}", flush=True)

    name_probe = YOLO(args.model)
    names: dict[int, str] = dict(name_probe.names)
    name_to_id = {v.lower(): k for k, v in names.items()}
    del name_probe
    allowed = parse_classes(args.classes, name_to_id)
    print(f"[init] {len(names)} classes loaded; initial filter: "
          f"{'all' if allowed is None else sorted(names[i] for i in allowed)}", flush=True)

    reid = CrossReid(device=device, weights=args.reid_model) if not args.no_cross_lock else None

    sink: TrackerSink | None = None
    if args.track_out:
        host, _, port_s = args.track_out.partition(":")
        if not port_s:
            raise SystemExit(f"--track-out must be HOST:PORT, got {args.track_out!r}")
        sink = TrackerSink(host, int(port_s))
        print(f"[track-out] emitting UDP to {host}:{port_s}", flush=True)

    aim_mode = args.aim_mode
    print(f"[init] aim mode: {aim_mode} (press 'f' to cycle)", flush=True)

    ndi_finder = None
    if args.ndi:
        from cyndilib.finder import Finder
        ndi_finder = Finder()
        ndi_finder.open()
        print("[ndi] waiting up to 10s for sources to appear...", flush=True)
        deadline = time.time() + 10.0
        while time.time() < deadline:
            time.sleep(0.5)
            if all(find_ndi_source(ndi_finder, t, timeout=0.0) for t in args.targets):
                break
        sources = []
        for t in args.targets:
            src = find_ndi_source(ndi_finder, t, timeout=2.0)
            if src is None:
                avail = ndi_finder.get_source_names()
                raise SystemExit(f"no NDI source matching {t!r}. Available: {avail}")
            print(f"[ndi] matched {t!r} -> {src.name!r}", flush=True)
            sources.append(src)
        streams = [NdiStream(src, win_x=20 + i * args.win_step, model_path=args.model)
                   for i, src in enumerate(sources)]
    else:
        streams = [Stream(t, args.sub, win_x=20 + i * args.win_step, model_path=args.model)
                   for i, t in enumerate(args.targets)]
    active_idx = 0

    try:
        while True:
            any_drew = False
            for s_idx, s in enumerate(streams):
                frame = s.grab()
                if frame is None:
                    continue
                any_drew = True
                results = s.model.track(
                    frame, persist=True, device=device, conf=args.conf, imgsz=args.imgsz,
                    half=args.half, verbose=False, tracker=args.tracker,
                )[0]
                s.tick()
                update_tracks(s, results, allowed, aim_mode, args.kpt_conf)
                s.last_frame = frame.copy()  # clean snapshot for ReID, before draw_overlay annotates
                draw_overlay(frame, s, device, allowed, names, is_active=(s_idx == active_idx),
                             aim_mode=aim_mode)
                print_error(s, frame.shape[1], frame.shape[0], aim_mode=aim_mode, sink=sink)
                cv2.imshow(s.win, frame)

            k = cv2.waitKey(1) & 0xFF
            if k == 255:
                if not any_drew:
                    time.sleep(0.005)
                continue
            if k in (ord("q"), 27):
                break
            elif k == ord("\t"):
                active_idx = (active_idx + 1) % len(streams)
                print(f"[active] stream {active_idx} ({streams[active_idx].target})", flush=True)
            elif k == ord("a"):
                allowed = None
            elif k == ord("n"):
                allowed = set()
            elif k == ord("p"):
                pid = name_to_id.get("person")
                allowed = {pid} if pid is not None else allowed
            elif k == ord("s"):
                for s in streams:
                    inv = ", ".join(
                        f"#{tid}({names.get(s.last_class[tid], '?')},{s.last_conf[tid]:.2f})"
                        for tid in s.visible_ids
                    )
                    sel = f" lock=#{s.selected_id}" if s.selected_id is not None else ""
                    print(f"[seen {s.target}]{sel} {inv or '(none)'}", flush=True)
            elif k == ord("0"):
                s = streams[active_idx]
                if s.selected_id is not None:
                    print(f"[{s.target}] released lock on #{s.selected_id}", flush=True)
                    s.selected_id = None
            elif ord("1") <= k <= ord("9"):
                idx = k - ord("1")
                s = streams[active_idx]
                if idx < len(s.visible_ids):
                    s.selected_id = s.visible_ids[idx]
                    print(f"[{s.target}] locked #{s.selected_id}", flush=True)
                    if reid is not None and len(streams) > 1:
                        propagate_lock(s, [o for o in streams if o is not s],
                                       args.cross_lock_threshold, reid,
                                       debug_dir=args.debug_reid)
            elif k == ord("x"):
                s = streams[active_idx]
                if reid is not None and len(streams) > 1:
                    propagate_lock(s, [o for o in streams if o is not s],
                                   args.cross_lock_threshold, reid,
                                   debug_dir=args.debug_reid)
            elif k == ord("f"):
                aim_mode = AIM_MODES[(AIM_MODES.index(aim_mode) + 1) % len(AIM_MODES)]
                print(f"[aim] mode -> {aim_mode}", flush=True)
    finally:
        for s in streams:
            s.close()
        if ndi_finder is not None:
            try:
                ndi_finder.close()
            except Exception:
                pass
        cv2.destroyAllWindows()
    return 0


if __name__ == "__main__":
    sys.exit(main())

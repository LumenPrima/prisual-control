#!/usr/bin/env python3
"""
Proof of concept: Logitech Extreme 3D Pro → VISCA focus + zoom + presets
on a Prisual TEN-20N Pro camera.

Stick X/Y (axes 0/1)     → variable-speed pan/tilt (jog, spring-return safe)
Throttle slider (axis 3) → absolute focus position
Twist axis (axis 2)      → variable-speed zoom (jog, spring-return safe)
Trigger (button 0)       → toggle auto/manual focus
Base buttons 7-12        → recall preset 1-6
Thumb (btn 1) + 7-12     → save preset 1-6
"""

import socket
import sys
import time

import pygame

# --- Configuration ---
CAMERA_IP = "10.2.2.212"
VISCA_PORT = 5678

# Axis indices (confirmed by probing)
AXIS_X = 0          # stick left/right → pan speed (springs back to center)
AXIS_Y = 1          # stick forward/back → tilt speed (springs back to center)
AXIS_TWIST = 2      # twist → zoom speed (springs back to center)
AXIS_THROTTLE = 3   # throttle slider → focus (stays where you leave it)

# VISCA focus range (confirmed by probing camera)
FOCUS_MIN = 0x0080
FOCUS_MAX = 0x1180

# Deadzones for spring-return axes
STICK_DEADZONE = 0.10
TWIST_DEADZONE = 0.15

# Button mapping (pygame index = label - 1)
BTN_TRIGGER = 0      # label 1: trigger — toggle auto/manual focus
BTN_THUMB = 1        # label 2: side thumb — hold for preset save modifier
# Base buttons (labels 7-12, pygame 6-11) → presets 1-6
BTN_PRESET_BASE = 6
BTN_PRESET_COUNT = 6

# Camera preset slots to use (0-254). Using 200+ to avoid colliding with
# any presets set via the camera's web UI or remote.
PRESET_SLOT_OFFSET = 200  # preset 1 → slot 200, preset 2 → slot 201, etc.

# How often to send focus commands (seconds)
FOCUS_INTERVAL = 0.10  # 10 Hz

# Jitter filter: ignore changes smaller than this when the axis is idle.
# Measured jitter at rest: 0 positions (ADC is rock solid), so threshold of 1 is safe.
FOCUS_JITTER_THRESHOLD = 1
FOCUS_MOVE_THRESHOLD = 0.005  # axis velocity that counts as "moving"


# --- Non-blocking VISCA ---

class VISCAConnection:
    """Non-blocking VISCA TCP connection. Fire-and-forget sends."""

    def __init__(self, ip, port):
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.sock.settimeout(3.0)
        self.sock.connect((ip, port))
        self.sock.setblocking(False)

    def drain(self):
        """Drain any pending responses without blocking."""
        try:
            while True:
                self.sock.recv(1024)
        except BlockingIOError:
            pass

    def send(self, cmd_bytes):
        """Send a command, draining stale responses first."""
        self.drain()
        self.sock.setblocking(True)
        self.sock.settimeout(0.5)
        self.sock.sendall(cmd_bytes)
        self.sock.setblocking(False)

    def send_and_recv(self, cmd_bytes, delay=0.05):
        """Send a command and wait for a response (for inquiries)."""
        self.drain()
        self.sock.setblocking(True)
        self.sock.settimeout(2.0)
        self.sock.sendall(cmd_bytes)
        time.sleep(delay)
        try:
            resp = self.sock.recv(1024)
        except socket.timeout:
            resp = b""
        self.sock.setblocking(False)
        return resp

    def close(self):
        self.sock.close()


def parse_position(resp):
    """Parse a VISCA inquiry response to extract a 4-nibble position."""
    if resp:
        for i in range(len(resp) - 6):
            if (resp[i] & 0xF0) == 0x90 and resp[i + 1] == 0x50:
                return (
                    ((resp[i + 2] & 0xF) << 12)
                    | ((resp[i + 3] & 0xF) << 8)
                    | ((resp[i + 4] & 0xF) << 4)
                    | (resp[i + 5] & 0xF)
                )
    return None


# --- VISCA commands ---

def focus_inquiry(cam):
    return parse_position(cam.send_and_recv(bytes([0x81, 0x09, 0x04, 0x48, 0xFF])))

def zoom_inquiry(cam):
    return parse_position(cam.send_and_recv(bytes([0x81, 0x09, 0x04, 0x47, 0xFF])))

def focus_mode_inquiry(cam):
    resp = cam.send_and_recv(bytes([0x81, 0x09, 0x04, 0x38, 0xFF]))
    if resp:
        for i in range(len(resp) - 3):
            if (resp[i] & 0xF0) == 0x90 and resp[i + 1] == 0x50:
                if resp[i + 2] == 0x02:
                    return "auto"
                elif resp[i + 2] == 0x03:
                    return "manual"
    return None

def set_manual_focus(cam):
    cam.send(bytes([0x81, 0x01, 0x04, 0x38, 0x03, 0xFF]))

def set_auto_focus(cam):
    cam.send(bytes([0x81, 0x01, 0x04, 0x38, 0x02, 0xFF]))

def focus_direct(cam, position):
    """Set absolute focus position."""
    p = (position >> 12) & 0x0F
    q = (position >> 8) & 0x0F
    r = (position >> 4) & 0x0F
    s = position & 0x0F
    cam.send(bytes([0x81, 0x01, 0x04, 0x48, p, q, r, s, 0xFF]))

def zoom_variable(cam, speed):
    """Variable-speed zoom. speed: -7 to +7 (negative=wide, positive=tele). 0=stop."""
    if speed == 0:
        cam.send(bytes([0x81, 0x01, 0x04, 0x07, 0x00, 0xFF]))
    elif speed > 0:
        # Tele (zoom in): 2p where p = speed 0-7
        cam.send(bytes([0x81, 0x01, 0x04, 0x07, 0x20 | min(speed, 7), 0xFF]))
    else:
        # Wide (zoom out): 3p where p = speed 0-7
        cam.send(bytes([0x81, 0x01, 0x04, 0x07, 0x30 | min(abs(speed), 7), 0xFF]))

def pantilt_variable(cam, pan_speed, tilt_speed):
    """Variable-speed pan/tilt. Speeds: -24 to +24 (pan), -20 to +20 (tilt). 0,0=stop."""
    # VISCA: 81 01 06 01 VV WW DD DD FF
    # VV = pan speed (01-18), WW = tilt speed (01-14)
    # DD DD = direction: 01=left/up, 02=right/down, 03=stop
    if pan_speed == 0 and tilt_speed == 0:
        cam.send(bytes([0x81, 0x01, 0x06, 0x01, 0x01, 0x01, 0x03, 0x03, 0xFF]))
        return

    vv = max(1, min(abs(pan_speed), 0x18)) if pan_speed != 0 else 0x01
    ww = max(1, min(abs(tilt_speed), 0x14)) if tilt_speed != 0 else 0x01

    if pan_speed < 0:
        pan_dir = 0x01  # left
    elif pan_speed > 0:
        pan_dir = 0x02  # right
    else:
        pan_dir = 0x03  # stop

    if tilt_speed < 0:
        tilt_dir = 0x01  # up (forward on stick = up = negative axis)
    elif tilt_speed > 0:
        tilt_dir = 0x02  # down
    else:
        tilt_dir = 0x03  # stop

    cam.send(bytes([0x81, 0x01, 0x06, 0x01, vv, ww, pan_dir, tilt_dir, 0xFF]))


def preset_save(cam, slot):
    """Save current pan/tilt/zoom/focus to camera preset slot (0-254)."""
    cam.send(bytes([0x81, 0x01, 0x04, 0x3F, 0x01, slot & 0xFF, 0xFF]))

def preset_recall(cam, slot):
    """Recall camera preset slot (0-254)."""
    cam.send(bytes([0x81, 0x01, 0x04, 0x3F, 0x02, slot & 0xFF, 0xFF]))


# --- Axis mapping ---

def throttle_to_focus(value):
    """Map throttle (-1.0 to 1.0) to focus position. Inverted: forward=far, back=near."""
    normalized = (-value + 1.0) / 2.0
    return int(FOCUS_MIN + normalized * (FOCUS_MAX - FOCUS_MIN))

# Expo curve: higher = more range at low end. 1.0 = linear, 2.0 = square, 3.0 = cubic
EXPO_CURVE = 2.5

def axis_to_speed(value, deadzone, max_speed):
    """Map a spring-return axis to a speed value with deadzone and expo curve."""
    if abs(value) < deadzone:
        return 0
    sign = 1 if value > 0 else -1
    magnitude = (abs(value) - deadzone) / (1.0 - deadzone)
    magnitude = magnitude ** EXPO_CURVE
    return sign * max(1, int(magnitude * max_speed + 0.5))


# --- Main loop ---

def main():
    pygame.init()
    pygame.joystick.init()

    if pygame.joystick.get_count() == 0:
        print("No joystick detected. Plug in the Extreme 3D Pro and retry.")
        sys.exit(1)

    joy = pygame.joystick.Joystick(0)
    joy.init()
    print(f"Joystick: {joy.get_name()}")
    print(f"  Axes: {joy.get_numaxes()}, Buttons: {joy.get_numbuttons()}, Hats: {joy.get_numhats()}")

    # Connect to camera
    print(f"\nConnecting to camera at {CAMERA_IP}:{VISCA_PORT}...")
    cam = VISCAConnection(CAMERA_IP, VISCA_PORT)
    print("Connected.")

    # Query current state
    mode = focus_mode_inquiry(cam)
    cur_focus = focus_inquiry(cam)
    cur_zoom = zoom_inquiry(cam)
    print(f"Focus mode: {mode}")
    print(f"Focus: 0x{cur_focus:04X}" if cur_focus is not None else "Focus: unknown")
    print(f"Zoom:  0x{cur_zoom:04X}" if cur_zoom is not None else "Zoom: unknown")

    # Ensure manual focus
    if mode != "manual":
        print("Switching to manual focus...")
        set_manual_focus(cam)
        time.sleep(0.3)

    manual_focus = True
    last_focus_pos = -1
    last_zoom_speed = 0
    last_pan_speed = 0
    last_tilt_speed = 0
    last_focus_time = 0
    last_throttle = 0.0
    prev_buttons = [False] * joy.get_numbuttons()
    cmd_count = 0
    focus_cmds = 0
    zoom_cmds = 0

    print("\n--- Controls ---")
    print("Stick X/Y        : Pan/Tilt (speed)")
    print("Twist            : Zoom (speed)")
    print("Throttle slider  : Focus (absolute)")
    print("Trigger (btn 0)  : Toggle auto/manual focus")
    print(f"Base btns 7-12   : Recall preset 1-{BTN_PRESET_COUNT}")
    print(f"Thumb + 7-12     : Save preset 1-{BTN_PRESET_COUNT}")
    print("Ctrl+C           : Quit")
    print()

    try:
        while True:
            pygame.event.pump()
            now = time.time()

            # --- Read all buttons ---
            buttons = [joy.get_button(i) for i in range(joy.get_numbuttons())]
            thumb_held = buttons[BTN_THUMB]

            # --- Trigger: toggle focus mode ---
            if buttons[BTN_TRIGGER] and not prev_buttons[BTN_TRIGGER]:
                if manual_focus:
                    set_auto_focus(cam)
                    manual_focus = False
                    print("\n→ Auto focus ON")
                else:
                    set_manual_focus(cam)
                    manual_focus = True
                    print("\n→ Manual focus ON")

            # --- Preset buttons ---
            for i in range(BTN_PRESET_COUNT):
                btn = BTN_PRESET_BASE + i
                if buttons[btn] and not prev_buttons[btn]:
                    slot = PRESET_SLOT_OFFSET + i
                    if thumb_held:
                        preset_save(cam, slot)
                        print(f"\n→ Saved preset {i + 1} (slot {slot})")
                    else:
                        preset_recall(cam, slot)
                        print(f"\n→ Recall preset {i + 1} (slot {slot})")
                    cmd_count += 1

            prev_buttons = buttons

            # --- Stick → Pan/Tilt speed ---
            pan_speed = axis_to_speed(joy.get_axis(AXIS_X), STICK_DEADZONE, 0x18)
            tilt_speed = axis_to_speed(joy.get_axis(AXIS_Y), STICK_DEADZONE, 0x14)
            if pan_speed != last_pan_speed or tilt_speed != last_tilt_speed:
                pantilt_variable(cam, pan_speed, tilt_speed)
                last_pan_speed = pan_speed
                last_tilt_speed = tilt_speed
                cmd_count += 1

            # --- Twist → Zoom speed ---
            zoom_speed = axis_to_speed(joy.get_axis(AXIS_TWIST), TWIST_DEADZONE, 7)
            if zoom_speed != last_zoom_speed:
                zoom_variable(cam, zoom_speed)
                last_zoom_speed = zoom_speed
                zoom_cmds += 1
                cmd_count += 1

            # --- Throttle → Focus (rate-limited, jitter-filtered) ---
            if manual_focus and now - last_focus_time >= FOCUS_INTERVAL:
                throttle = joy.get_axis(AXIS_THROTTLE)
                focus_pos = throttle_to_focus(throttle)
                delta = abs(focus_pos - last_focus_pos)
                moving = abs(throttle - last_throttle) > FOCUS_MOVE_THRESHOLD
                last_throttle = throttle
                # When actively moving: send every change. When idle: suppress jitter.
                if delta > 0 and (moving or delta > FOCUS_JITTER_THRESHOLD):
                    focus_direct(cam, focus_pos)
                    last_focus_pos = focus_pos
                    last_focus_time = now
                    focus_cmds += 1
                    cmd_count += 1

            # --- Status line ---
            ps = f"{last_pan_speed:+d}" if last_pan_speed else " 0"
            ts = f"{last_tilt_speed:+d}" if last_tilt_speed else " 0"
            zs = f"{last_zoom_speed:+d}" if last_zoom_speed else " 0"
            print(
                f"\rP:{ps} T:{ts} Z:{zs}  "
                f"Focus:0x{last_focus_pos:04X}  "
                f"{'MF' if manual_focus else 'AF'}  "
                f"Cmds:{cmd_count}   ",
                end="", flush=True,
            )

            time.sleep(0.01)

    except KeyboardInterrupt:
        # Stop all motion
        pantilt_variable(cam, 0, 0)
        zoom_variable(cam, 0)
        print("\n\nShutting down.")
    finally:
        cam.close()
        pygame.quit()


if __name__ == "__main__":
    main()

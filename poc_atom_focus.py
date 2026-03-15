#!/usr/bin/env python3
"""
PreSonus ATOM encoders → VISCA absolute focus and zoom
on a Prisual TEN-20N Pro camera.

Knob 1 (CC 14) → Focus position
Knob 2 (CC 15) → Zoom position
"""

import socket
import sys
import time

import mido

# --- Configuration ---
CAMERA_IP = "10.2.2.212"
VISCA_PORT = 5678
ATOM_PORT = "ATOM:ATOM MIDI 1 28:0"

# ATOM encoder CC numbers (confirmed by sniffing)
CC_FOCUS = 14   # Knob 1
CC_ZOOM = 15    # Knob 2

# VISCA ranges (confirmed by probing camera)
FOCUS_MIN = 0x0080
FOCUS_MAX = 0x1180
ZOOM_MIN = 0x0000
ZOOM_MAX = 0x4000


# --- VISCA helpers ---

def visca_connect(ip, port):
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.settimeout(2.0)
    sock.connect((ip, port))
    return sock


def visca_drain(sock):
    """Drain any pending responses."""
    sock.setblocking(False)
    try:
        while True:
            sock.recv(1024)
    except:
        pass
    sock.setblocking(True)
    sock.settimeout(2.0)


def visca_send(sock, cmd_bytes):
    visca_drain(sock)
    sock.sendall(cmd_bytes)


def visca_send_and_read(sock, cmd_bytes):
    visca_drain(sock)
    sock.sendall(cmd_bytes)
    time.sleep(0.05)
    try:
        return sock.recv(1024)
    except socket.timeout:
        return b""


def visca_inquiry_position(sock, cmd_bytes):
    """Send an inquiry and parse a 4-nibble position response."""
    resp = visca_send_and_read(sock, cmd_bytes)
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


def visca_focus_direct(sock, position):
    p = (position >> 12) & 0x0F
    q = (position >> 8) & 0x0F
    r = (position >> 4) & 0x0F
    s = position & 0x0F
    visca_send(sock, bytes([0x81, 0x01, 0x04, 0x48, p, q, r, s, 0xFF]))


def visca_zoom_direct(sock, position):
    p = (position >> 12) & 0x0F
    q = (position >> 8) & 0x0F
    r = (position >> 4) & 0x0F
    s = position & 0x0F
    visca_send(sock, bytes([0x81, 0x01, 0x04, 0x47, p, q, r, s, 0xFF]))


def cc_to_position(cc_value, min_pos, max_pos):
    """Map a 7-bit CC value (0-127) to a VISCA position range."""
    return int(min_pos + (cc_value / 127.0) * (max_pos - min_pos))


# --- Main ---

def main():
    # Connect to camera
    print(f"Connecting to camera at {CAMERA_IP}:{VISCA_PORT}...")
    sock = visca_connect(CAMERA_IP, VISCA_PORT)
    print("Connected.")

    # Query current state
    focus_pos = visca_inquiry_position(sock, bytes([0x81, 0x09, 0x04, 0x48, 0xFF]))
    zoom_pos = visca_inquiry_position(sock, bytes([0x81, 0x09, 0x04, 0x47, 0xFF]))
    print(f"Current focus: 0x{focus_pos:04X}  zoom: 0x{zoom_pos:04X}")

    # Set manual focus
    visca_send_and_read(sock, bytes([0x81, 0x01, 0x04, 0x38, 0x03, 0xFF]))
    print("Manual focus set.")

    # Open MIDI
    print(f"Opening MIDI: {ATOM_PORT}")
    midi_in = mido.open_input(ATOM_PORT)

    last_focus = -1
    last_zoom = -1

    print("\n--- Controls ---")
    print("Knob 1 (CC 14) : Focus")
    print("Knob 2 (CC 15) : Zoom")
    print("Ctrl+C          : Quit")
    print()

    try:
        while True:
            msg = midi_in.poll()
            if msg is None:
                time.sleep(0.005)
                continue

            if msg.type != "control_change":
                continue

            if msg.control == CC_FOCUS:
                focus = cc_to_position(msg.value, FOCUS_MIN, FOCUS_MAX)
                if focus != last_focus:
                    visca_focus_direct(sock, focus)
                    last_focus = focus
                    print(
                        f"\rFocus: {msg.value:3d}/127 → 0x{focus:04X}  "
                        f"Zoom: 0x{last_zoom:04X}" if last_zoom >= 0 else f"\rFocus: {msg.value:3d}/127 → 0x{focus:04X}",
                        end="", flush=True,
                    )

            elif msg.control == CC_ZOOM:
                zoom = cc_to_position(msg.value, ZOOM_MIN, ZOOM_MAX)
                if zoom != last_zoom:
                    visca_zoom_direct(sock, zoom)
                    last_zoom = zoom
                    print(
                        f"\rFocus: 0x{last_focus:04X}  "
                        f"Zoom: {msg.value:3d}/127 → 0x{zoom:04X}" if last_focus >= 0 else f"\rZoom: {msg.value:3d}/127 → 0x{zoom:04X}",
                        end="", flush=True,
                    )

    except KeyboardInterrupt:
        print("\n\nRestoring auto focus...")
        visca_send_and_read(sock, bytes([0x81, 0x01, 0x04, 0x38, 0x02, 0xFF]))
        print("Done.")
    finally:
        midi_in.close()
        sock.close()


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Probe pygame joystick axis resolution — same test as joyprobe."""
import pygame
import sys
import signal

def main():
    pygame.init()
    pygame.joystick.init()

    if pygame.joystick.get_count() == 0:
        print("No joystick found.")
        sys.exit(1)

    joy = pygame.joystick.Joystick(0)
    joy.init()
    print(f"Joystick: {joy.get_name()}")
    print(f"  Axes: {joy.get_numaxes()}  Buttons: {joy.get_numbuttons()}")
    print()
    print("Move every axis through its FULL range, then press Ctrl+C.")
    print()

    num_axes = joy.get_numaxes()
    seen = [set() for _ in range(num_axes)]
    mins = [999.0] * num_axes
    maxs = [-999.0] * num_axes
    prev = [None] * num_axes
    min_delta = [999.0] * num_axes

    def report(sig=None, frame=None):
        print("\n")
        print("=== Pygame Axis Report ===")
        print()
        for i in range(num_axes):
            discrete = len(seen[i])
            r = maxs[i] - mins[i]
            vals = sorted(seen[i])
            min_step = 0
            if len(vals) > 1:
                min_step = min(vals[j] - vals[j-1] for j in range(1, len(vals)))
            avg_step = r / (discrete - 1) if discrete > 1 else 0
            print(f"Axis {i}:")
            print(f"  Min: {mins[i]:+.6f}  Max: {maxs[i]:+.6f}  Range: {r:.6f}")
            print(f"  Discrete values seen: {discrete}")
            print(f"  Min step (sorted):      {min_step:.6f}")
            if min_delta[i] < 999:
                print(f"  Min step (consecutive): {min_delta[i]:.6f}")
            print(f"  Avg step:               {avg_step:.6f}")
            # Map to focus range for comparison
            focus_range = 0x1180 - 0x0080  # 4352
            focus_per_step = focus_range * min_step / 2.0 if min_step > 0 else 0
            print(f"  Focus positions per min step: {focus_per_step:.1f}")
            print()
        sys.exit(0)

    signal.signal(signal.SIGINT, report)

    import time
    while True:
        pygame.event.pump()
        for i in range(num_axes):
            v = joy.get_axis(i)
            seen[i].add(v)
            if v < mins[i]:
                mins[i] = v
            if v > maxs[i]:
                maxs[i] = v
            if prev[i] is not None and v != prev[i]:
                d = abs(v - prev[i])
                if d < min_delta[i]:
                    min_delta[i] = d
            prev[i] = v

        print("\r", end="")
        for i in range(num_axes):
            print(f"  A{i}: {joy.get_axis(i):+.4f} ({len(seen[i]):4d} vals)", end="")
        print("   ", end="", flush=True)

        time.sleep(0.005)

if __name__ == "__main__":
    main()

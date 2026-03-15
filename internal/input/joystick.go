package input

import (
	"context"
	"math"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// JoystickState represents the current state of all axes and buttons.
type JoystickState struct {
	Axes      [4]float64
	Buttons   [12]bool
	Connected bool
	Name      string
}

// AxisToSpeed maps a spring-return axis value to a speed with deadzone and expo curve.
// Returns integer speed from -maxSpeed to +maxSpeed.
func AxisToSpeed(value, deadzone float64, maxSpeed int, expo float64) int {
	if math.Abs(value) < deadzone {
		return 0
	}
	sign := 1.0
	if value < 0 {
		sign = -1.0
	}
	magnitude := (math.Abs(value) - deadzone) / (1.0 - deadzone)
	magnitude = math.Pow(magnitude, expo)
	speed := int(magnitude*float64(maxSpeed) + 0.5)
	if speed < 1 {
		speed = 1
	}
	return int(sign) * speed
}

// ThrottleToFocus maps a throttle axis value (-1.0 to 1.0) to an absolute focus position.
// Inverted: forward (negative) = far focus, back (positive) = near focus.
func ThrottleToFocus(value float64, focusMin, focusMax uint16) uint16 {
	normalized := (-value + 1.0) / 2.0
	return focusMin + uint16(normalized*float64(focusMax-focusMin))
}

// PollJoystick polls the SDL2 joystick at ~100Hz and sends state updates on ch.
// Handles hotplug: sends Connected=false when unplugged, reconnects automatically.
// ch must be bidirectional with buffer >= 1. The goroutine uses non-blocking sends
// and drains stale values so the consumer always gets the latest state.
func PollJoystick(ctx context.Context, ch chan JoystickState) {
	if err := sdl.Init(sdl.INIT_JOYSTICK); err != nil {
		ch <- JoystickState{Connected: false}
		return
	}
	defer sdl.Quit()

	sdl.JoystickEventState(sdl.ENABLE)

	var joy *sdl.Joystick
	ticker := time.NewTicker(10 * time.Millisecond) // ~100 Hz
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if joy != nil {
				joy.Close()
			}
			return
		case <-ticker.C:
		}

		sdl.PumpEvents()

		// Handle hotplug
		if joy != nil && !joy.Attached() {
			joy.Close()
			joy = nil
			sendLatest(ch, JoystickState{Connected: false})
			continue
		}

		if joy == nil {
			n := sdl.NumJoysticks()
			if n <= 0 {
				continue
			}
			joy = sdl.JoystickOpen(0)
			if joy == nil {
				continue
			}
		}

		var state JoystickState
		state.Connected = true
		state.Name = joy.Name()

		numAxes := joy.NumAxes()
		for i := 0; i < numAxes && i < 4; i++ {
			state.Axes[i] = float64(joy.Axis(i)) / 32768.0
		}

		numButtons := joy.NumButtons()
		for i := 0; i < numButtons && i < 12; i++ {
			state.Buttons[i] = joy.Button(i) == 1
		}

		sendLatest(ch, state)
	}
}

// sendLatest does a non-blocking send, draining any stale value first.
func sendLatest(ch chan JoystickState, state JoystickState) {
	// Drain old value if present
	select {
	case <-ch:
	default:
	}
	// Send new value (non-blocking — channel should have room now)
	select {
	case ch <- state:
	default:
	}
}

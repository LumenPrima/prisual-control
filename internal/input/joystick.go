package input

import "math"

// JoystickState represents the current state of all axes and buttons.
type JoystickState struct {
	Axes      [6]float64
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

// sendLatest does a non-blocking send, draining any stale value first.
func sendLatest(ch chan JoystickState, state JoystickState) {
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- state:
	default:
	}
}

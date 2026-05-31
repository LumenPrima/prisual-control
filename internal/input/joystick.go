package input

import "math"

// Hat direction constants.
const (
	HatCentered = -1
	HatUp       = 0
	HatRight    = 1
	HatDown     = 2
	HatLeft     = 3
)

// JoystickState represents the current state of all axes and buttons.
type JoystickState struct {
	Axes      [6]float64
	Buttons   [12]bool
	Hat       int // HatCentered, HatUp, HatRight, HatDown, HatLeft
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

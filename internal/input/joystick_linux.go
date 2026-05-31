package input

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"time"
)

// Linux joystick event from /usr/include/linux/joystick.h
type jsEvent struct {
	Time   uint32
	Value  int16
	Type   uint8
	Number uint8
}

const (
	jsEventButton = 0x01
	jsEventAxis   = 0x02
	jsEventInit   = 0x80
)

// findJoystick tries /dev/input/js0 through js3.
func findJoystick() (*os.File, string, error) {
	for i := 0; i < 4; i++ {
		path := fmt.Sprintf("/dev/input/js%d", i)
		f, err := os.Open(path)
		if err == nil {
			// Read the joystick name via ioctl JSIOCGNAME
			name := readJoystickName(f)
			if name == "" {
				name = path
			}
			return f, name, nil
		}
	}
	return nil, "", fmt.Errorf("no joystick found at /dev/input/js0-3")
}

// readJoystickName reads the device name via ioctl.
// Falls back to the device path if ioctl fails.
func readJoystickName(f *os.File) string {
	// JSIOCGNAME(len) = _IOC(_IOC_READ, 'j', 0x13, len)
	// _IOC_READ = 2, 'j' = 0x6A
	// _IOC(2, 0x6A, 0x13, 128) = 0x80006A13 | (128 << 16) = 0x80806A13
	const iocGName = 0x80806A13
	buf := make([]byte, 128)
	_, _, errno := rawIoctl(f.Fd(), iocGName, buf)
	if errno != 0 {
		return ""
	}
	// Find null terminator
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

// PollJoystick reads joystick events from /dev/input/js* and sends state
// updates on ch. Handles disconnect/reconnect.
func PollJoystick(ctx context.Context, ch chan JoystickState) {
	var f *os.File
	var name string
	var state JoystickState

	reconnect := func() {
		if f != nil {
			f.Close()
			f = nil
		}
		sendLatest(ch, JoystickState{Connected: false})
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if f != nil {
				f.Close()
			}
			return
		default:
		}

		// Try to open if not connected
		if f == nil {
			var err error
			f, name, err = findJoystick()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					continue
				}
			}
			state = JoystickState{Connected: true, Name: name}
			// Set a read deadline so we can check ctx periodically
			f.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		}

		// Read one event
		var ev jsEvent
		err := binary.Read(f, binary.LittleEndian, &ev)
		if err != nil {
			if os.IsTimeout(err) {
				// Deadline expired — refresh deadline and check ctx
				f.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				continue
			}
			// Device disconnected or error
			reconnect()
			continue
		}

		switch {
		case ev.Type&jsEventAxis != 0:
			if int(ev.Number) < len(state.Axes) {
				state.Axes[ev.Number] = float64(ev.Value) / 32767.0
			}
		case ev.Type&jsEventButton != 0:
			if int(ev.Number) < len(state.Buttons) {
				state.Buttons[ev.Number] = ev.Value != 0
			}
		}

		// Don't send during init burst (init flag set)
		if ev.Type&jsEventInit != 0 {
			continue
		}

		// Derive hat from axes 4 (LR) and 5 (UD) — Linux exposes POV hat as two axes
		state.Hat = HatCentered
		h := state.Axes[4] // -1=left, +1=right
		v := state.Axes[5] // -1=up, +1=down
		if v < -0.5 {
			state.Hat = HatUp
		} else if v > 0.5 {
			state.Hat = HatDown
		} else if h < -0.5 {
			state.Hat = HatLeft
		} else if h > 0.5 {
			state.Hat = HatRight
		}

		sendLatest(ch, state)
	}
}

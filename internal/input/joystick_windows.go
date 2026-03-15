package input

import (
	"context"
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

var (
	winmm          = syscall.NewLazyDLL("winmm.dll")
	joyGetPosEx    = winmm.NewProc("joyGetPosEx")
	joyGetDevCapsW = winmm.NewProc("joyGetDevCapsW")
	joyGetNumDevs  = winmm.NewProc("joyGetNumDevs")
)

const (
	joyReturnAll = 0xFF
	maxJoyDevs   = 16
)

// JOYINFOEX — Windows multimedia joystick state
type joyInfoEx struct {
	Size       uint32
	Flags      uint32
	XPos       uint32
	YPos       uint32
	ZPos       uint32
	RPos       uint32 // Rudder/twist
	UPos       uint32
	VPos       uint32 // Throttle
	Buttons    uint32
	ButtonNum  uint32
	POV        uint32
	Reserved1  uint32
	Reserved2  uint32
}

// JOYCAPSW — joystick capabilities
type joyCapsW struct {
	Mid        uint16
	Pid        uint16
	Name       [32]uint16
	XMin       uint32
	XMax       uint32
	YMin       uint32
	YMax       uint32
	ZMin       uint32
	ZMax       uint32
	NumButtons uint32
	PeriodMin  uint32
	PeriodMax  uint32
	RMin       uint32
	RMax       uint32
	UMin       uint32
	UMax       uint32
	VMin       uint32
	VMax       uint32
	Caps       uint32
	MaxAxes    uint32
	NumAxes    uint32
	MaxButtons uint32
	RegKey     [32]uint16
	OemVxd     [260]uint16
}

func findWindowsJoystick() (uint32, string, error) {
	numDevs, _, _ := joyGetNumDevs.Call()
	if numDevs == 0 {
		return 0, "", fmt.Errorf("no joystick devices")
	}

	var info joyInfoEx
	info.Size = uint32(unsafe.Sizeof(info))
	info.Flags = joyReturnAll

	for id := uint32(0); id < uint32(numDevs) && id < maxJoyDevs; id++ {
		ret, _, _ := joyGetPosEx.Call(uintptr(id), uintptr(unsafe.Pointer(&info)))
		if ret == 0 { // JOYERR_NOERROR
			var caps joyCapsW
			ret2, _, _ := joyGetDevCapsW.Call(uintptr(id), uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps))
			name := fmt.Sprintf("Joystick %d", id)
			if ret2 == 0 {
				name = syscall.UTF16ToString(caps.Name[:])
			}
			return id, name, nil
		}
	}
	return 0, "", fmt.Errorf("no joystick connected")
}

// normalizeAxis maps a raw uint32 value from [min, max] to [-1.0, +1.0].
func normalizeAxis(val, min, max uint32) float64 {
	if max <= min {
		return 0
	}
	return (float64(val-min)/float64(max-min))*2.0 - 1.0
}

// PollJoystick polls the Windows joystick API at ~100Hz.
func PollJoystick(ctx context.Context, ch chan JoystickState) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	var joyID uint32
	var name string
	var caps joyCapsW
	connected := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if !connected {
			var err error
			joyID, name, err = findWindowsJoystick()
			if err != nil {
				continue
			}
			// Get capabilities for axis ranges
			joyGetDevCapsW.Call(uintptr(joyID), uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps))
			connected = true
		}

		var info joyInfoEx
		info.Size = uint32(unsafe.Sizeof(info))
		info.Flags = joyReturnAll

		ret, _, _ := joyGetPosEx.Call(uintptr(joyID), uintptr(unsafe.Pointer(&info)))
		if ret != 0 {
			connected = false
			sendLatest(ch, JoystickState{Connected: false})
			continue
		}

		var state JoystickState
		state.Connected = true
		state.Name = name

		// Map Windows axes to our standard layout:
		// Axis 0 = X (stick LR), Axis 1 = Y (stick FB)
		// Axis 2 = R/Rz (twist), Axis 3 = V (throttle/slider)
		state.Axes[0] = normalizeAxis(info.XPos, caps.XMin, caps.XMax)
		state.Axes[1] = normalizeAxis(info.YPos, caps.YMin, caps.YMax)
		state.Axes[2] = normalizeAxis(info.RPos, caps.RMin, caps.RMax)
		state.Axes[3] = normalizeAxis(info.VPos, caps.VMin, caps.VMax)

		// Buttons are a bitmask
		for i := 0; i < 12; i++ {
			state.Buttons[i] = info.Buttons&(1<<uint(i)) != 0
		}

		sendLatest(ch, state)
	}
}

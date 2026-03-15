package visca

import "fmt"

// parsePosition extracts a 4-nibble position from a VISCA inquiry response.
// Looks for the pattern: 0x90 0x50 0p 0q 0r 0s 0xFF
func parsePosition(resp []byte) (uint16, bool) {
	if len(resp) < 7 {
		return 0, false
	}
	for i := 0; i <= len(resp)-7; i++ {
		if resp[i]&0xF0 == 0x90 && resp[i+1] == 0x50 {
			pos := uint16(resp[i+2]&0x0F)<<12 |
				uint16(resp[i+3]&0x0F)<<8 |
				uint16(resp[i+4]&0x0F)<<4 |
				uint16(resp[i+5]&0x0F)
			return pos, true
		}
	}
	return 0, false
}

// positionNibbles splits a 16-bit position into 4 nibbles for VISCA commands.
func positionNibbles(pos uint16) (byte, byte, byte, byte) {
	return byte((pos >> 12) & 0x0F),
		byte((pos >> 8) & 0x0F),
		byte((pos >> 4) & 0x0F),
		byte(pos & 0x0F)
}

// FocusDirect sets absolute focus position.
func FocusDirect(c *Connection, position uint16) error {
	p, q, r, s := positionNibbles(position)
	return c.Send([]byte{0x81, 0x01, 0x04, 0x48, p, q, r, s, 0xFF})
}

// ZoomDirect sets absolute zoom position.
func ZoomDirect(c *Connection, position uint16) error {
	p, q, r, s := positionNibbles(position)
	return c.Send([]byte{0x81, 0x01, 0x04, 0x47, p, q, r, s, 0xFF})
}

// ZoomVariable sets variable-speed zoom. speed: -7 to +7 (negative=wide, positive=tele). 0=stop.
func ZoomVariable(c *Connection, speed int) error {
	if speed == 0 {
		return c.Send([]byte{0x81, 0x01, 0x04, 0x07, 0x00, 0xFF})
	}
	if speed > 0 {
		s := speed
		if s > 7 {
			s = 7
		}
		return c.Send([]byte{0x81, 0x01, 0x04, 0x07, 0x20 | byte(s), 0xFF})
	}
	s := -speed
	if s > 7 {
		s = 7
	}
	return c.Send([]byte{0x81, 0x01, 0x04, 0x07, 0x30 | byte(s), 0xFF})
}

// PanTiltVariable sets variable-speed pan/tilt.
// pan: -24 to +24, tilt: -20 to +20. 0,0 = stop.
func PanTiltVariable(c *Connection, pan, tilt int) error {
	if pan == 0 && tilt == 0 {
		return c.Send([]byte{0x81, 0x01, 0x06, 0x01, 0x01, 0x01, 0x03, 0x03, 0xFF})
	}

	vv := byte(0x01)
	if pan != 0 {
		v := pan
		if v < 0 {
			v = -v
		}
		if v > 0x18 {
			v = 0x18
		}
		vv = byte(v)
	}

	ww := byte(0x01)
	if tilt != 0 {
		w := tilt
		if w < 0 {
			w = -w
		}
		if w > 0x14 {
			w = 0x14
		}
		ww = byte(w)
	}

	panDir := byte(0x03)
	if pan < 0 {
		panDir = 0x01 // left
	} else if pan > 0 {
		panDir = 0x02 // right
	}

	tiltDir := byte(0x03)
	if tilt < 0 {
		tiltDir = 0x01 // up (forward on stick = up = negative axis)
	} else if tilt > 0 {
		tiltDir = 0x02 // down
	}

	return c.Send([]byte{0x81, 0x01, 0x06, 0x01, vv, ww, panDir, tiltDir, 0xFF})
}

// PresetSave saves current pan/tilt/zoom/focus to a camera preset slot (0-254).
func PresetSave(c *Connection, slot byte) error {
	return c.Send([]byte{0x81, 0x01, 0x04, 0x3F, 0x01, slot, 0xFF})
}

// PresetRecall recalls a camera preset slot (0-254).
func PresetRecall(c *Connection, slot byte) error {
	return c.Send([]byte{0x81, 0x01, 0x04, 0x3F, 0x02, slot, 0xFF})
}

// SetManualFocus switches the camera to manual focus mode.
func SetManualFocus(c *Connection) error {
	return c.Send([]byte{0x81, 0x01, 0x04, 0x38, 0x03, 0xFF})
}

// SetAutoFocus switches the camera to auto focus mode.
func SetAutoFocus(c *Connection) error {
	return c.Send([]byte{0x81, 0x01, 0x04, 0x38, 0x02, 0xFF})
}

// StopAllMotion stops pan/tilt and zoom.
func StopAllMotion(c *Connection) error {
	if err := PanTiltVariable(c, 0, 0); err != nil {
		return err
	}
	return ZoomVariable(c, 0)
}

// TallyRed turns on the camera's red tally light (program).
func TallyRed(c *Connection) error {
	return c.Send([]byte{0x81, 0x01, 0x7E, 0x01, 0x0A, 0x00, 0x02, 0xFF})
}

// TallyGreen turns on the camera's green tally light (preview).
func TallyGreen(c *Connection) error {
	return c.Send([]byte{0x81, 0x01, 0x7E, 0x01, 0x0A, 0x00, 0x03, 0xFF})
}

// TallyOff turns off the camera's tally light.
func TallyOff(c *Connection) error {
	return c.Send([]byte{0x81, 0x01, 0x7E, 0x01, 0x0A, 0x00, 0x00, 0xFF})
}

// FocusInquiry queries the camera's current focus position.
func FocusInquiry(c *Connection) (uint16, error) {
	resp, err := c.SendRecv([]byte{0x81, 0x09, 0x04, 0x48, 0xFF})
	if err != nil {
		return 0, err
	}
	pos, ok := parsePosition(resp)
	if !ok {
		return 0, fmt.Errorf("focus inquiry: no position in response")
	}
	return pos, nil
}

// ZoomInquiry queries the camera's current zoom position.
func ZoomInquiry(c *Connection) (uint16, error) {
	resp, err := c.SendRecv([]byte{0x81, 0x09, 0x04, 0x47, 0xFF})
	if err != nil {
		return 0, err
	}
	pos, ok := parsePosition(resp)
	if !ok {
		return 0, fmt.Errorf("zoom inquiry: no position in response")
	}
	return pos, nil
}

// PanTiltInquiry queries the camera's current pan and tilt positions.
// Returns signed positions (int16) for both axes.
func PanTiltInquiry(c *Connection) (pan, tilt int16, err error) {
	resp, err := c.SendRecv([]byte{0x81, 0x09, 0x06, 0x12, 0xFF})
	if err != nil {
		return 0, 0, err
	}
	// Response: y0 50 0w 0w 0w 0w 0z 0z 0z 0z FF
	if len(resp) < 11 {
		return 0, 0, fmt.Errorf("pan/tilt inquiry: response too short")
	}
	for i := 0; i <= len(resp)-11; i++ {
		if resp[i]&0xF0 == 0x90 && resp[i+1] == 0x50 {
			panU := uint16(resp[i+2]&0x0F)<<12 |
				uint16(resp[i+3]&0x0F)<<8 |
				uint16(resp[i+4]&0x0F)<<4 |
				uint16(resp[i+5]&0x0F)
			tiltU := uint16(resp[i+6]&0x0F)<<12 |
				uint16(resp[i+7]&0x0F)<<8 |
				uint16(resp[i+8]&0x0F)<<4 |
				uint16(resp[i+9]&0x0F)
			return int16(panU), int16(tiltU), nil
		}
	}
	return 0, 0, fmt.Errorf("pan/tilt inquiry: no position in response")
}

// FocusModeInquiry queries whether the camera is in auto or manual focus mode.
func FocusModeInquiry(c *Connection) (string, error) {
	resp, err := c.SendRecv([]byte{0x81, 0x09, 0x04, 0x38, 0xFF})
	if err != nil {
		return "", err
	}
	for i := 0; i <= len(resp)-4; i++ {
		if resp[i]&0xF0 == 0x90 && resp[i+1] == 0x50 {
			switch resp[i+2] {
			case 0x02:
				return "auto", nil
			case 0x03:
				return "manual", nil
			}
		}
	}
	return "", fmt.Errorf("focus mode inquiry: unrecognized response")
}

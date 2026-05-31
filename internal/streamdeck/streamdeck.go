package streamdeck

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"time"

	"rafaelmartins.com/p/usbhid"
)

const elgatoVendorID uint16 = 0x0fd9

// Supported Stream Deck product IDs.
var supportedPIDs = map[uint16]deviceModel{
	0x006c: {name: "Stream Deck XL", keys: 32, cols: 8, rows: 4, imgSize: 96, keyStart: 3},
	0x008f: {name: "Stream Deck XL V2", keys: 32, cols: 8, rows: 4, imgSize: 96, keyStart: 3},
	0x0080: {name: "Stream Deck MK.2", keys: 15, cols: 5, rows: 3, imgSize: 72, keyStart: 3},
	0x006d: {name: "Stream Deck V2", keys: 15, cols: 5, rows: 3, imgSize: 72, keyStart: 3},
}

type deviceModel struct {
	name     string
	keys     int
	cols     int
	rows     int
	imgSize  int
	keyStart int // byte offset in input report where key states begin
}

// DeckEvent is sent from the Stream Deck goroutine to the main loop.
type DeckEvent struct {
	Connected bool
	Model     string
	NumKeys   int
	Cols      int
	Rows      int
	Button    int  // 0-based key index
	Pressed   bool // true = pressed
}

// ButtonAction identifies what a Stream Deck key does.
type ButtonAction int

const (
	ActionNone ButtonAction = iota
	ActionPreset1
	ActionPreset2
	ActionPreset3
	ActionPreset4
	ActionPreset5
	ActionPreset6
	ActionCut
	ActionFade
	ActionAFToggle
	ActionOverrideToggle
	ActionInput1
	ActionInput2
	ActionInput3
	ActionInput4
	ActionInput5
	ActionInput6
	ActionInput7
	ActionInput8
	ActionPanLeft
	ActionPanRight
	ActionTiltUp
	ActionTiltDown
	ActionZoomIn
	ActionZoomOut
	ActionPTZHome
	ActionStream
	ActionPreset7
	ActionPreset8
	ActionInput9
	ActionInput10
	ActionPresetSave
	ActionOverlay
	ActionSlideNext
	ActionSlidePrev
	ActionFocusFar
	ActionFocusNear
)

// LayoutXL maps Stream Deck XL 32-key indices to actions.
// Layout (4 rows x 8 cols, keys numbered left-to-right, top-to-bottom):
//
//	Row 0:  [ P1  ] [ P2  ] [ P3  ] [ P4  ] [ P5  ] [ P6  ] [ P7  ] [ P8  ]
//	Row 1:  [IN 1 ] [IN 2 ] [IN 3 ] [IN 4 ] [IN 5 ] [ZOUT ] [ T▲  ] [Z IN]
//	Row 2:  [ S<  ] [ S>  ] [FAR  ] [NEAR ] [ LT  ] [ P◀  ] [HOME ] [ P▶  ]
//	Row 3:  [STRM ] [ --- ] [OVRD ] [ AF  ] [ CUT ] [FADE ] [ T▼  ] [ SET ]
var LayoutXL = map[int]ButtonAction{
	// Row 0: Presets
	0:  ActionPreset1,
	1:  ActionPreset2,
	2:  ActionPreset3,
	3:  ActionPreset4,
	4:  ActionPreset5,
	5:  ActionPreset6,
	6:  ActionPreset7,
	7:  ActionPreset8,
	// Row 1: Inputs 1-5, zoom in, tilt up, zoom out
	8:  ActionInput1,
	9:  ActionInput2,
	10: ActionInput3,
	11: ActionInput4,
	12: ActionInput5,
	13: ActionZoomOut,
	14: ActionTiltUp,
	15: ActionZoomIn,
	// Row 2: slides, focus, overlay, pan/home
	16: ActionSlidePrev,
	17: ActionSlideNext,
	18: ActionFocusFar,
	19: ActionFocusNear,
	20: ActionOverlay,
	21: ActionPanLeft,
	22: ActionPTZHome,
	23: ActionPanRight,
	// Row 3: Stream, override, AF, cut, fade, tilt down, set
	24: ActionStream,
	26: ActionOverrideToggle,
	27: ActionAFToggle,
	28: ActionCut,
	29: ActionFade,
	30: ActionTiltDown,
	31: ActionPresetSave,
}

// Layout15 maps Stream Deck 15-key indices to actions.
// Layout (3 rows x 5 cols):
//
//	Row 0:  [ P1  ] [ P2  ] [ P3  ] [ P4  ] [ P5  ]
//	Row 1:  [ P6  ] [ CUT ] [FADE ] [ AF  ] [OVRD ]
//	Row 2:  [CAM1 ] [CAM2 ] [CAM3 ] [CAM4 ] [CAM5 ]
var Layout15 = map[int]ButtonAction{
	0:  ActionPreset1,
	1:  ActionPreset2,
	2:  ActionPreset3,
	3:  ActionPreset4,
	4:  ActionPreset5,
	5:  ActionPreset6,
	6:  ActionCut,
	7:  ActionFade,
	8:  ActionAFToggle,
	9:  ActionOverrideToggle,
	10: ActionInput1,
	11: ActionInput2,
	12: ActionInput3,
	13: ActionInput4,
	14: ActionInput5,
}

// IsHoldAction returns true for actions that need press/release handling.
func IsHoldAction(a ButtonAction) bool {
	switch a {
	case ActionPanLeft, ActionPanRight, ActionTiltUp, ActionTiltDown,
		ActionZoomIn, ActionZoomOut, ActionAFToggle,
		ActionFocusFar, ActionFocusNear:
		return true
	}
	return false
}

// GetLayout returns the appropriate layout for the key count.
func GetLayout(numKeys int) map[int]ButtonAction {
	if numKeys >= 32 {
		return LayoutXL
	}
	return Layout15
}

// ActionLabel returns the display label for an action.
func ActionLabel(a ButtonAction) string {
	switch a {
	case ActionPreset1:
		return "P1"
	case ActionPreset2:
		return "P2"
	case ActionPreset3:
		return "P3"
	case ActionPreset4:
		return "P4"
	case ActionPreset5:
		return "P5"
	case ActionPreset6:
		return "P6"
	case ActionCut:
		return "CUT"
	case ActionFade:
		return "FADE"
	case ActionAFToggle:
		return "FOCUS"
	case ActionOverrideToggle:
		return "DRIVE\nLIVE"
	case ActionInput1:
		return "IN1"
	case ActionInput2:
		return "IN2"
	case ActionInput3:
		return "IN3"
	case ActionInput4:
		return "IN4"
	case ActionInput5:
		return "IN5"
	case ActionInput6:
		return "IN6"
	case ActionInput7:
		return "IN7"
	case ActionInput8:
		return "IN8"
	case ActionPanLeft:
		return "<"
	case ActionPanRight:
		return ">"
	case ActionTiltUp:
		return "UP"
	case ActionTiltDown:
		return "DOWN"
	case ActionZoomIn:
		return "Z+"
	case ActionZoomOut:
		return "Z-"
	case ActionPTZHome:
		return "HOME"
	case ActionStream:
		return "STREAM"
	case ActionPreset7:
		return "P7"
	case ActionPreset8:
		return "P8"
	case ActionInput9:
		return "IN9"
	case ActionInput10:
		return "IN10"
	case ActionPresetSave:
		return "SET"
	case ActionOverlay:
		return "LOWER\nTHIRD"
	case ActionSlidePrev:
		return "SLIDE\n<"
	case ActionSlideNext:
		return "SLIDE\n>"
	case ActionFocusFar:
		return "FOCUS\nFAR"
	case ActionFocusNear:
		return "FOCUS\nNEAR"
	default:
		return ""
	}
}

// InputSlot describes a vMix input assigned to a Stream Deck key.
type InputSlot struct {
	Number int    // vMix input number (1-based, 0 = empty)
	Label  string // short display label
}

// FeedbackState is sent from the main loop to the Stream Deck goroutine
// to update LCD key visuals.
type FeedbackState struct {
	CameraTally map[int]string // input number -> "PROGRAM"/"PREVIEW"/""
	Inputs      []InputSlot    // vMix inputs to show on input keys (up to 8 for XL)
	AFActive       bool
	AFLatched      bool
	OverrideActive bool
	Streaming      bool
	PresetSaveMode bool
	ActivePreset   int // 1-based, 0 = none
	OverlayActive  bool
	FocusJog       int // -1=far, 0=stopped, +1=near
}

// PollStreamDeck discovers a Stream Deck, handles button events,
// and updates LCD keys from feedback. Blocks until ctx is cancelled.
func PollStreamDeck(ctx context.Context, eventCh chan<- DeckEvent, feedbackCh <-chan FeedbackState) {
	for {
		if err := runDevice(ctx, eventCh, feedbackCh); err != nil {
			select {
			case eventCh <- DeckEvent{Connected: false}:
			default:
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func findDevice() (*usbhid.Device, deviceModel, error) {
	devices, err := usbhid.Enumerate(func(d *usbhid.Device) bool {
		if d.VendorId() != elgatoVendorID {
			return false
		}
		_, ok := supportedPIDs[d.ProductId()]
		return ok
	})
	if err != nil {
		return nil, deviceModel{}, err
	}
	if len(devices) == 0 {
		return nil, deviceModel{}, fmt.Errorf("no Stream Deck found")
	}
	dev := devices[0]
	model := supportedPIDs[dev.ProductId()]
	return dev, model, nil
}

func runDevice(ctx context.Context, eventCh chan<- DeckEvent, feedbackCh <-chan FeedbackState) error {
	dev, model, err := findDevice()
	if err != nil {
		return err
	}

	if err := dev.Open(false); err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer dev.Close()

	numKeys := model.keys
	layout := GetLayout(numKeys)

	// Send connected event
	select {
	case eventCh <- DeckEvent{
		Connected: true,
		Model:     model.name,
		NumKeys:   numKeys,
		Cols:      model.cols,
		Rows:      model.rows,
	}:
	default:
	}

	// Set brightness
	setBrightness(dev, 80)

	// Paint initial labels
	paintKeys(dev, model, layout, nil)

	// Read button states in a goroutine
	type buttonEvent struct {
		key     int
		pressed bool
	}
	buttonCh := make(chan buttonEvent, 32)
	errCh := make(chan error, 1)

	go func() {
		prevState := make([]bool, numKeys)
		for {
			_, data, err := dev.GetInputReport()
			if err != nil {
				errCh <- err
				return
			}
			// Key states start at keyStart offset
			for i := 0; i < numKeys; i++ {
				idx := model.keyStart + i
				if idx >= len(data) {
					break
				}
				pressed := data[idx] != 0
				if pressed != prevState[i] {
					prevState[i] = pressed
					buttonCh <- buttonEvent{key: i, pressed: pressed}
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errCh:
			return err
		case evt := <-buttonCh:
			select {
			case eventCh <- DeckEvent{
				Connected: true,
				Model:     model.name,
				NumKeys:   numKeys,
				Cols:      model.cols,
				Rows:      model.rows,
				Button:    evt.key,
				Pressed:   evt.pressed,
			}:
			default:
			}
		case state := <-feedbackCh:
			paintKeys(dev, model, layout, &state)
		}
	}
}

// --- HID protocol ---

func setBrightness(dev *usbhid.Device, perc byte) error {
	pl := make([]byte, dev.GetFeatureReportLength())
	pl[0] = 0x08
	pl[1] = perc
	return dev.SetFeatureReport(3, pl)
}

func sendKeyImage(dev *usbhid.Device, keyIdx int, jpegData []byte) error {
	hdrLen := 7
	reportLen := int(dev.GetOutputReportLength())
	payloadLen := reportLen - hdrLen

	var page byte
	offset := 0
	remaining := len(jpegData)

	for remaining > 0 {
		chunkSize := payloadLen
		if chunkSize > remaining {
			chunkSize = remaining
		}
		var isLast byte
		if remaining <= payloadLen {
			isLast = 1
		}

		payload := make([]byte, reportLen)
		payload[0] = 0x07
		payload[1] = byte(keyIdx)
		payload[2] = isLast
		payload[3] = byte(chunkSize & 0xFF)
		payload[4] = byte(chunkSize >> 8)
		payload[5] = page
		payload[6] = 0x00
		copy(payload[hdrLen:], jpegData[offset:offset+chunkSize])

		if err := dev.SetOutputReport(2, payload); err != nil {
			return err
		}

		offset += chunkSize
		remaining -= chunkSize
		page++
	}
	return nil
}

// --- Key rendering ---

func paintKeys(dev *usbhid.Device, model deviceModel, layout map[int]ButtonAction, state *FeedbackState) {
	for i := 0; i < model.keys; i++ {
		action := layout[i]
		label := ActionLabel(action)
		bg := color.RGBA{20, 20, 20, 255}
		fg := color.RGBA{120, 120, 120, 255}

		if action == ActionNone {
			// Unassigned key — dark
			bg = color.RGBA{5, 5, 5, 255}
			fg = color.RGBA{30, 30, 30, 255}
		}

		if state != nil && action != ActionNone {
			switch action {
			case ActionInput1, ActionInput2, ActionInput3, ActionInput4,
				ActionInput5, ActionInput6, ActionInput7, ActionInput8,
				ActionInput9, ActionInput10:
				slotIdx := int(action - ActionInput1)
				if slotIdx < len(state.Inputs) {
					slot := state.Inputs[slotIdx]
					if slot.Number > 0 {
						label = slot.Label
						fg = color.RGBA{180, 180, 180, 255}
						tally := state.CameraTally[slot.Number]
						switch tally {
						case "PROGRAM":
							bg = color.RGBA{180, 0, 0, 255}
							fg = color.RGBA{255, 255, 255, 255}
						case "PREVIEW":
							bg = color.RGBA{0, 140, 0, 255}
							fg = color.RGBA{255, 255, 255, 255}
						}
					}
				} else {
					// No input for this slot
					bg = color.RGBA{5, 5, 5, 255}
					fg = color.RGBA{30, 30, 30, 255}
				}

			case ActionAFToggle:
				if state.AFActive {
					if state.AFLatched {
						bg = color.RGBA{200, 120, 0, 255}
						label = "AF"
					} else {
						bg = color.RGBA{180, 60, 180, 255}
						label = "AF"
					}
					fg = color.RGBA{255, 255, 255, 255}
				}

			case ActionOverrideToggle:
				if state.OverrideActive {
					bg = color.RGBA{200, 0, 0, 255}
					fg = color.RGBA{255, 255, 255, 255}
				}

			case ActionCut:
				bg = color.RGBA{50, 0, 0, 255}
				fg = color.RGBA{255, 60, 60, 255}

			case ActionFade:
				bg = color.RGBA{0, 0, 50, 255}
				fg = color.RGBA{60, 60, 255, 255}

			case ActionPreset1, ActionPreset2, ActionPreset3,
				ActionPreset4, ActionPreset5, ActionPreset6,
				ActionPreset7, ActionPreset8:
				presetNum := int(action-ActionPreset1) + 1
				fg = color.RGBA{180, 180, 180, 255}
				if state.PresetSaveMode {
					bg = color.RGBA{80, 60, 0, 255}
					fg = color.RGBA{255, 200, 40, 255}
				} else if state.ActivePreset == presetNum {
					bg = color.RGBA{0, 80, 160, 255}
					fg = color.RGBA{255, 255, 255, 255}
				} else if state.OverrideActive {
					bg = color.RGBA{60, 0, 0, 255}
				}

			case ActionPresetSave:
				bg = color.RGBA{50, 40, 0, 255}
				fg = color.RGBA{220, 180, 40, 255}

			case ActionPanLeft, ActionPanRight, ActionTiltUp, ActionTiltDown,
				ActionZoomIn, ActionZoomOut:
				bg = color.RGBA{30, 30, 50, 255}
				fg = color.RGBA{100, 140, 220, 255}

			case ActionPTZHome:
				bg = color.RGBA{30, 30, 50, 255}
				fg = color.RGBA{100, 140, 220, 255}

			case ActionSlidePrev, ActionSlideNext:
				bg = color.RGBA{0, 40, 50, 255}
				fg = color.RGBA{40, 180, 200, 255}

			case ActionFocusFar, ActionFocusNear:
				bg = color.RGBA{45, 35, 60, 255}
				fg = color.RGBA{180, 150, 230, 255}
				if (action == ActionFocusFar && state.FocusJog < 0) ||
					(action == ActionFocusNear && state.FocusJog > 0) {
					bg = color.RGBA{110, 70, 170, 255}
					fg = color.RGBA{255, 255, 255, 255}
				}

			case ActionOverlay:
				if state.OverlayActive {
					bg = color.RGBA{180, 140, 0, 255}
					fg = color.RGBA{255, 255, 255, 255}
				} else {
					bg = color.RGBA{40, 30, 0, 255}
					fg = color.RGBA{140, 120, 40, 255}
				}

			case ActionStream:
				if state.Streaming {
					bg = color.RGBA{200, 0, 0, 255}
					fg = color.RGBA{255, 255, 255, 255}
					label = "LIVE"
				} else {
					bg = color.RGBA{30, 30, 30, 255}
					fg = color.RGBA{100, 100, 100, 255}
				}
			}
		}

		renderAndSendKey(dev, model, i, label, bg, fg)
	}
}

func renderAndSendKey(dev *usbhid.Device, model deviceModel, keyIdx int, label string, bg, fg color.RGBA) {
	size := model.imgSize
	rect := image.Rect(0, 0, size, size)
	img := image.NewRGBA(rect)
	draw.Draw(img, rect, &image.Uniform{bg}, image.Point{}, draw.Src)

	if label != "" {
		drawText(img, label, fg, rect)
	}

	// Flip horizontal + vertical (required for all current models)
	flipped := image.NewRGBA(rect)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			flipped.Set(size-1-x, size-1-y, img.At(x, y))
		}
	}

	var buf bytes.Buffer
	jpeg.Encode(&buf, flipped, &jpeg.Options{Quality: 90})
	sendKeyImage(dev, keyIdx, buf.Bytes())
}

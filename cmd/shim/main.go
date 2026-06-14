package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"prisual_control/internal/config"
	"prisual_control/internal/input"
	"prisual_control/internal/proclaim"
	"prisual_control/internal/router"
	deck "prisual_control/internal/streamdeck"
	"prisual_control/internal/tracker"
	"prisual_control/internal/visca"
	"prisual_control/internal/vmix"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// CLI flags
var (
	flagVmix         = flag.String("vmix", "", "vMix host IP (enables auto-discovery and tally)")
	flagCamera       = flag.String("camera", "", "single camera IP (fallback if no vMix)")
	flagViscaPort    = flag.Int("visca-port", 5678, "VISCA TCP port")
	flagFadeDuration = flag.Int("fade-ms", 1000, "fade transition duration in milliseconds")
	flagConfig       = flag.String("config", "", "controller config JSON file (default: built-in Extreme 3D Pro)")
	flagDumpConfig   = flag.Bool("dump-config", false, "print default controller config JSON and exit")
	flagStreamDeck   = flag.Bool("streamdeck", false, "enable Stream Deck support")
	flagProclaim     = flag.String("proclaim", "", "Proclaim host IP (enables slide control)")
	flagProclaimPass = flag.String("proclaim-pass", "proclaim", "Proclaim network password")
	flagTrackerPort  = flag.Int("tracker-port", 0, "UDP port to receive auto-tracker error packets (0 disables)")
	flagTrackerKp    = flag.Float64("tracker-kp", 10.0, "proportional gain for auto-track pan/tilt (speed per unit error)")
	flagTrackerKd    = flag.Float64("tracker-kd", 1.5, "derivative gain (damps overshoot from control-loop lag); 0 disables")
	flagTrackerDead  = flag.Float64("tracker-deadband", 0.08, "normalized error magnitude below which auto-track stops (gives center a 'sticky' zone)")
	flagTrackerStale = flag.Int("tracker-stale-ms", 500, "auto-track stops if no fresh error within this many milliseconds")
	flagFocusStep    = flag.Int("focus-step", 1, "FocusDirect step (focus positions) per FOCUS button tap; 1 = finest")
	flagFocusHoldMs  = flag.Int("focus-hold-ms", 500, "hold a FOCUS button this long (ms) before a continuous speed-1 jog begins")
	// PWM jog knobs — currently bypassed (see processFocusPulse), kept for re-enable.
	flagFocusOnMs  = flag.Int("focus-pulse-on-ms", 40, "PWM jog ON window in ms (bypassed; speed-1 jog runs this long per pulse)")
	flagFocusOffMs = flag.Int("focus-pulse-off-ms", 60, "PWM jog OFF window in ms; 0 = continuous (bypassed)")
)

// Camera constants (these are properties of the Prisual cameras, not the controller)
const (
	zoomMax = 0x4000

	maxPanSpeed  = 0x18 // 24
	maxTiltSpeed = 0x14 // 20
	maxZoomSpeed = 7
	// VISCA variable focus jog speed (1-7). Speed 1 (slowest) is used for the
	// hold-to-jog phase; fine adjustment is done by FocusDirect taps instead.
	maxFocusSpeed = 1
	// Usable focus position range (probed): FocusDirect taps clamp to this.
	focusPosMin = 0x0080
	focusPosMax = 0x1180

	presetSlotOffset = 200

	// Display ranges for pan/tilt position bars
	panPosRange  = 2500
	tiltPosRange = 1200

	// Exposure position ranges (verify on hardware — these are typical for Sony-derived VISCA)
	shutterMin = 0x00
	shutterMax = 0x15 // 21 positions
	gainMin    = 0x00
	gainMax    = 0x0F // 16 positions
)

// Focus control tuning, set from flags in main().
//   - focusStep / focusHoldMs drive the active model: a FocusDirect step per
//     tap, escalating to a continuous speed-1 jog after a hold.
//   - focusPulseOnMs / focusPulseOffMs feed the PWM jog (processFocusPulse),
//     currently bypassed but retained for possible re-enable.
var (
	focusStep   = 1
	focusHoldMs = 500

	focusPulseOnMs  = 40
	focusPulseOffMs = 60
)

// --- Bubbletea messages ---

type tallyMsg vmix.TallyUpdate
type joystickMsg input.JoystickState
type deckMsg deck.DeckEvent
type tickMsg time.Time
type viscaReplyMsg visca.Reply
type trackerMsg tracker.Event

// Tag types for VISCA inquiries (used by visca.Reply.Tag dispatch in Update).
type (
	queryPanTilt   struct{}
	queryZoom      struct{}
	queryShutter   struct{}
	queryGain      struct{}
	queryFocusMode struct{}
	// queryFocusStep carries a one-shot focus inquiry whose reply triggers a
	// FocusDirect nudge of `dir` * focusStep positions, where dir is the
	// position-value polarity (+1 toward far, -1 toward near on this camera).
	queryFocusStep struct{ dir int }
)

// --- Bubbletea model ---

// Mapping mode steps
const (
	mapAxisPan = iota
	mapAxisTilt
	mapAxisZoom
	mapBtnAFHold
	mapBtnAFLatch
	mapBtnProgramOverride
	mapBtnFade
	mapBtnCut
	mapBtnCyclePreview
	mapBtnPresetSave
	mapBtnPreset1
	mapBtnPreset2
	mapBtnPreset3
	mapBtnPreset4
	mapBtnPreset5
	mapBtnPreset6
	mapStepCount // sentinel
)

var mapStepNames = [mapStepCount]string{
	"Pan (move stick left/right)",
	"Tilt (move stick forward/back)",
	"Zoom (twist or secondary axis)",
	"AF Hold button (trigger)",
	"AF Latch button (thumb)",
	"Program Override button",
	"Fade button",
	"Cut button",
	"Cycle Preview button",
	"Preset Save modifier button",
	"Preset 1 button",
	"Preset 2 button",
	"Preset 3 button",
	"Preset 4 button",
	"Preset 5 button",
	"Preset 6 button",
}

type model struct {
	ctx       context.Context
	cancel    context.CancelFunc
	cfg       config.ControllerConfig
	cfgPath   string // path used to load config (for saving)
	router    *router.CameraRouter
	tallyCh   chan vmix.TallyUpdate
	joyCh     chan input.JoystickState // bidirectional for non-blocking send pattern
	viscaCh   chan visca.Reply         // shared inquiry reply channel
	vmixHost  string
	vmixOK    bool
	vmixCmd       *vmix.Commander // nil if no vMix
	vmixNumInputs int            // total vMix inputs (for preview cycling)
	vmixInputs    []vmix.InputInfo // all vMix inputs (for Stream Deck)
	singleCam     bool           // true when running without vMix

	// Stream Deck
	deckCh         chan deck.DeckEvent
	deckFeedback   chan deck.FeedbackState
	deckConnected  bool
	deckModel      string
	deckNumKeys    int
	streaming      bool // vMix streaming state
	overlayActive  bool // overlay 1 active
	overlayInput   int  // vMix input number for overlay 1
	proclaimCmd    *proclaim.Client // nil if no Proclaim
	deckPresetSave bool         // true when waiting for preset button to save
	activePresets  map[int]int // input number -> last recalled preset on that camera (1-based, 0/missing = none)
	streamArmed    time.Time   // when stream button was first tapped (zero = not armed)

	// Joystick state
	joyConnected bool
	joyName      string
	latestJoy    input.JoystickState // most recent joystick snapshot
	joyReady     bool                // true once we've received at least one state
	// Last commanded pan/tilt PER cam (input number) so concurrent auto-track
	// across multiple cams maintains independent change-detection.
	lastPan      map[int]int
	lastTilt     map[int]int
	lastZoom     int
	manualFocus  bool
	afLatched    bool // true when AF is latched (thumb+trigger)
	lastFocusJog int  // -1=far, 0=stopped, +1=near
	// Focus step+hold: a FOCUS button tap does one FocusDirect step; holding
	// past focusHoldMs escalates to a continuous speed-1 jog.
	focusPressAt time.Time // when the held FOCUS button was pressed
	focusHeldJog bool      // true once the hold escalated to a continuous jog
	// Focus jog PWM (bypassed; see processFocusPulse). Retained for re-enable.
	focusPulseOn bool      // true during the ON (moving) phase of the pulse
	focusPulseAt time.Time // when the current ON/OFF phase began
	cmdCount     int

	// Zoom-AF: auto-switch to AF while zooming, restore MF after
	zoomAF bool // true when zoom triggered AF

	// Camera positions (queried periodically when idle)
	camPanPos     int16
	camTiltPos    int16
	camZoomPos    uint16
	posQueried    bool // true once we've done at least one position query
	lastQueryTime time.Time
	queryPhase    int // 0 = pan/tilt, 1 = zoom, 2 = shutter, 3 = gain

	// Exposure (shutter/gain via hat)
	camShutter      byte
	camGain         byte
	exposureQueried bool
	prevHat         int

	// Button edge detection
	prevButtons [12]bool

	// After a camera switch, suppress movement until stick returns to center
	switchMute bool

	// Auto-track (Python tracker -> UDP -> here)
	trackerCh         chan tracker.Event       // nil if tracker listener disabled
	trackerErr        map[string]tracker.Event // latest event keyed by source cam IP
	trackerPrev       map[string]tracker.Event // previous event for D-term derivative
	// Per-cam (input num) enable. State stays with the camera; tally changes
	// don't carry the toggle with them. Use 't' to toggle current program cam,
	// 'p' to toggle current preview cam.
	autoTrackEnabled  map[int]bool
	trackerKp         float64
	trackerKd         float64
	trackerDeadband   float64
	trackerStaleAfter time.Duration

	// Framing: per-aim-mode target point in normalized frame coords [0,1].
	// PD error is computed shim-side as (aim - target) so this is live-tunable
	// from the framing modal without touching the tracker.
	framingTargetX map[string]float64
	framingTargetY map[string]float64
	framingMode    bool   // true while operator is in the framing editor
	framingEditing string // which mode's target the editor is currently nudging

	// View state for the framing widget
	lastAimMode string  // last received aim mode (for live indicator)
	lastAimX    float64 // last received ax
	lastAimY    float64 // last received ay
	lastAimAt   time.Time

	// Display
	logLines []string

	// Mapping mode
	mapping        bool                  // true when in mapping mode
	mapStep        int                   // current step (mapAxis* or mapBtn*)
	mapCfg         config.ControllerConfig // config being built
	mapBaseAxes    [6]float64            // axis snapshot when step started (for detecting movement)
	mapBaseButtons [12]bool              // button snapshot when step started
	mapDetected    int                   // detected axis/button index, -1 if none yet
	mapSettled     bool                  // true once detection has settled
}

func (m *model) addLog(msg string) {
	m.logLines = append(m.logLines, msg)
	if len(m.logLines) > 5 {
		m.logLines = m.logLines[len(m.logLines)-5:]
	}
}

func initialModel(
	ctx context.Context,
	cancel context.CancelFunc,
	cfg config.ControllerConfig,
	cfgPath string,
	r *router.CameraRouter,
	tallyCh chan vmix.TallyUpdate,
	joyCh chan input.JoystickState,
	viscaCh chan visca.Reply,
	deckCh chan deck.DeckEvent,
	deckFeedbackCh chan deck.FeedbackState,
	trackerCh chan tracker.Event,
	trackerKp, trackerKd, trackerDeadband float64,
	trackerStaleAfter time.Duration,
	vmixHost string,
	vmixOK bool,
	vmixCmd *vmix.Commander,
	vmixNumInputs int,
	vmixInputs []vmix.InputInfo,
	streamingNow bool,
	overlayInput int,
	overlayActive bool,
	proclaimCmd *proclaim.Client,
	singleCam bool,
) model {
	return model{
		ctx:           ctx,
		cancel:        cancel,
		cfg:           cfg,
		cfgPath:       cfgPath,
		router:        r,
		tallyCh:       tallyCh,
		joyCh:         joyCh,
		viscaCh:       viscaCh,
		deckCh:        deckCh,
		deckFeedback:  deckFeedbackCh,
		vmixHost:      vmixHost,
		vmixOK:        vmixOK,
		vmixCmd:       vmixCmd,
		vmixNumInputs: vmixNumInputs,
		vmixInputs:    vmixInputs,
		streaming:     streamingNow,
		overlayInput:  overlayInput,
		overlayActive: overlayActive,
		proclaimCmd:   proclaimCmd,
		singleCam:     singleCam,
		manualFocus:       false,
		prevHat:           input.HatCentered,
		activePresets:     make(map[int]int),
		lastPan:           make(map[int]int),
		lastTilt:          make(map[int]int),
		trackerCh:         trackerCh,
		trackerErr:        make(map[string]tracker.Event),
		trackerPrev:       make(map[string]tracker.Event),
		autoTrackEnabled:  make(map[int]bool),
		trackerKp:         trackerKp,
		trackerKd:         trackerKd,
		trackerDeadband:   trackerDeadband,
		trackerStaleAfter: trackerStaleAfter,
		framingTargetX: map[string]float64{
			"nose": 0.5, "shoulders": 0.5, "torso": 0.5,
		},
		framingTargetY: map[string]float64{
			"nose": 0.33, "shoulders": 0.40, "torso": 0.50,
		},
		framingEditing: "shoulders",
	}
}

// --- Bubbletea commands ---

func listenTally(ch chan vmix.TallyUpdate) tea.Cmd {
	return func() tea.Msg {
		return tallyMsg(<-ch)
	}
}

func listenJoystick(ch chan input.JoystickState) tea.Cmd {
	return func() tea.Msg {
		return joystickMsg(<-ch)
	}
}

func listenDeck(ch chan deck.DeckEvent) tea.Cmd {
	return func() tea.Msg {
		return deckMsg(<-ch)
	}
}

func listenVisca(ch chan visca.Reply) tea.Cmd {
	return func() tea.Msg {
		return viscaReplyMsg(<-ch)
	}
}

func listenTracker(ch chan tracker.Event) tea.Cmd {
	return func() tea.Msg {
		return trackerMsg(<-ch)
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(10*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		listenJoystick(m.joyCh),
		listenVisca(m.viscaCh),
		tickCmd(),
	}
	if m.vmixOK {
		cmds = append(cmds, listenTally(m.tallyCh))
	}
	if m.deckCh != nil {
		cmds = append(cmds, listenDeck(m.deckCh))
	}
	if m.trackerCh != nil {
		cmds = append(cmds, listenTracker(m.trackerCh))
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		key := msg.String()

		if key == "ctrl+c" {
			for _, cam := range m.router.Cameras {
				visca.StopAllMotion(cam)
			}
			m.cancel()
			return m, tea.Quit
		}

		// Mapping mode key handling
		if m.mapping {
			switch key {
			case "esc":
				m.mapping = false
				m.addLog("Mapping cancelled")
			case "enter":
				if m.mapDetected >= 0 {
					m.applyMapStep()
					m.mapStep++
					if m.mapStep >= mapStepCount {
						// Done — save config
						m.cfg = m.mapCfg
						m.cfg.Name = m.joyName
						savePath := m.cfgPath
						if savePath == "" {
							savePath = "controller.json"
						}
						if err := m.cfg.Save(savePath); err != nil {
							m.addLog(fmt.Sprintf("Save error: %v", err))
						} else {
							m.addLog(fmt.Sprintf("Saved to %s", savePath))
						}
						m.mapping = false
					} else {
						m.startMapStep()
					}
				}
			case "tab":
				// Skip this mapping step (keep default)
				m.mapStep++
				if m.mapStep >= mapStepCount {
					m.cfg = m.mapCfg
					m.cfg.Name = m.joyName
					savePath := m.cfgPath
					if savePath == "" {
						savePath = "controller.json"
					}
					if err := m.cfg.Save(savePath); err != nil {
						m.addLog(fmt.Sprintf("Save error: %v", err))
					} else {
						m.addLog(fmt.Sprintf("Saved to %s", savePath))
					}
					m.mapping = false
				} else {
					m.startMapStep()
				}
			}
			return m, nil
		}

		// Framing mode key handling
		if m.framingMode {
			const nudge = 0.01
			switch key {
			case "esc", "f", "F", "enter":
				m.framingMode = false
				m.addLog("Framing editor closed")
			case "up", "k":
				m.framingTargetY[m.framingEditing] -= nudge
				if m.framingTargetY[m.framingEditing] < 0 {
					m.framingTargetY[m.framingEditing] = 0
				}
			case "down", "j":
				m.framingTargetY[m.framingEditing] += nudge
				if m.framingTargetY[m.framingEditing] > 1 {
					m.framingTargetY[m.framingEditing] = 1
				}
			case "left", "h":
				m.framingTargetX[m.framingEditing] -= nudge
				if m.framingTargetX[m.framingEditing] < 0 {
					m.framingTargetX[m.framingEditing] = 0
				}
			case "right", "l":
				m.framingTargetX[m.framingEditing] += nudge
				if m.framingTargetX[m.framingEditing] > 1 {
					m.framingTargetX[m.framingEditing] = 1
				}
			case "tab":
				modes := []string{"shoulders", "nose", "torso"}
				for i, mode := range modes {
					if mode == m.framingEditing {
						m.framingEditing = modes[(i+1)%len(modes)]
						break
					}
				}
			case "r", "R":
				// Reset current mode to defaults
				switch m.framingEditing {
				case "nose":
					m.framingTargetX[m.framingEditing] = 0.5
					m.framingTargetY[m.framingEditing] = 0.33
				case "shoulders":
					m.framingTargetX[m.framingEditing] = 0.5
					m.framingTargetY[m.framingEditing] = 0.40
				case "torso":
					m.framingTargetX[m.framingEditing] = 0.5
					m.framingTargetY[m.framingEditing] = 0.50
				}
				m.addLog(fmt.Sprintf("Reset %s framing target", m.framingEditing))
			}
			return m, nil
		}

		// Normal mode keys
		switch key {
		case "q":
			for _, cam := range m.router.Cameras {
				visca.StopAllMotion(cam)
			}
			m.cancel()
			return m, tea.Quit
		case "m":
			if m.joyConnected {
				m.mapping = true
				m.mapStep = 0
				m.mapCfg = m.cfg // start from current config
				m.startMapStep()
				m.addLog("Entering mapping mode...")
			}
		case "t":
			m.toggleAutoTrackFor(m.router.Program, "program")
		case "p":
			m.toggleAutoTrackFor(m.router.Preview, "preview")
		case "f", "F":
			if m.trackerCh == nil {
				m.addLog("Framing editor needs --tracker-port")
			} else {
				m.framingMode = true
				if m.lastAimMode != "" {
					// Start editing whichever mode the tracker is currently sending
					if _, ok := m.framingTargetX[m.lastAimMode]; ok {
						m.framingEditing = m.lastAimMode
					}
				}
				m.addLog("Framing editor: arrows nudge, Tab cycle, R reset, Esc exit")
			}
		}
		return m, nil

	case tallyMsg:
		update := vmix.TallyUpdate(msg)
		oldTarget := m.router.TargetInput()
		if m.router.UpdateTally(update.Program, update.Preview) {
			m.router.HandleSwitch(oldTarget)
			// Reset old cam's pan/tilt state (HandleSwitch stopped it physically)
			if oldTarget > 0 {
				m.lastPan[oldTarget] = 0
				m.lastTilt[oldTarget] = 0
			}
			m.lastZoom = 0
			m.lastFocusJog = 0
			m.switchMute = true
			m.posQueried = false
			m.exposureQueried = false
			m.addLog(fmt.Sprintf("Tally: pgm=%d pvw=%d", update.Program, update.Preview))
		}
		m.sendDeckFeedback()
		return m, listenTally(m.tallyCh)

	case joystickMsg:
		state := input.JoystickState(msg)
		m.joyConnected = state.Connected
		m.joyName = state.Name
		if state.Connected {
			m.latestJoy = state
			m.joyReady = true
		}
		return m, listenJoystick(m.joyCh)

	case deckMsg:
		event := deck.DeckEvent(msg)
		m.deckConnected = event.Connected
		m.deckModel = event.Model
		m.deckNumKeys = event.NumKeys
		if event.Pressed {
			m.handleDeckPress(event.Button)
		} else {
			m.handleDeckRelease(event.Button)
		}
		m.sendDeckFeedback()
		return m, listenDeck(m.deckCh)

	case tickMsg:
		if m.mapping {
			// In mapping mode, run detection instead of camera control
			if m.joyReady && m.joyConnected {
				m.updateMapDetection()
			}
		} else {
			// Auto-track drives every enabled cam, independent of joystick.
			m.processAutoTrack()
			// Focus hold-to-jog is deck-driven, so it runs regardless of joystick.
			m.processFocusHold()
			if m.joyReady && m.joyConnected {
				m.processJoystick()
			}
		}
		return m, tickCmd()

	case viscaReplyMsg:
		m.handleViscaReply(visca.Reply(msg))
		return m, listenVisca(m.viscaCh)

	case trackerMsg:
		ev := tracker.Event(msg)
		// Track ID change resets D (the prior error belonged to a different subject)
		if cur, had := m.trackerErr[ev.Cam]; had && cur.ID == ev.ID {
			m.trackerPrev[ev.Cam] = cur
		} else {
			delete(m.trackerPrev, ev.Cam)
		}
		m.trackerErr[ev.Cam] = ev
		m.lastAimMode = ev.Mode
		m.lastAimX = ev.Ax
		m.lastAimY = ev.Ay
		m.lastAimAt = ev.At
		return m, listenTracker(m.trackerCh)
	}

	return m, nil
}

// handleViscaReply dispatches an inquiry response by Tag type and updates
// cached camera state used by the TUI.
func (m *model) handleViscaReply(r visca.Reply) {
	switch t := r.Tag.(type) {
	case queryFocusStep:
		// One-shot: a FOCUS tap queried the position; nudge by one step. Skip
		// if the hold already escalated to a jog (don't fight it) or the cam
		// switched out from under the in-flight inquiry.
		if r.Err == nil && !m.focusHeldJog && m.activeCamAddrIs(r.CamAddr) {
			pos := int(r.Result.(uint16))
			pos += t.dir * focusStep
			if pos < focusPosMin {
				pos = focusPosMin
			} else if pos > focusPosMax {
				pos = focusPosMax
			}
			if cam := m.router.ActiveCamera(); cam != nil {
				visca.FocusDirect(cam, uint16(pos))
				m.cmdCount++
			}
		}

	case queryPanTilt:
		if r.Err == nil && m.activeCamAddrIs(r.CamAddr) {
			pt := r.Result.(visca.PanTiltResult)
			m.camPanPos = pt.Pan
			m.camTiltPos = pt.Tilt
			m.posQueried = true
		}

	case queryZoom:
		if r.Err == nil && m.activeCamAddrIs(r.CamAddr) {
			m.camZoomPos = r.Result.(uint16)
		}

	case queryShutter:
		if r.Err == nil && m.activeCamAddrIs(r.CamAddr) {
			m.camShutter = r.Result.(byte)
			m.exposureQueried = true
		}

	case queryGain:
		if r.Err == nil && m.activeCamAddrIs(r.CamAddr) {
			m.camGain = r.Result.(byte)
		}

	case queryFocusMode:
		if r.Err == nil && m.activeCamAddrIs(r.CamAddr) {
			mode := r.Result.(string)
			if mode == "auto" && m.manualFocus {
				m.addLog("WARNING: camera reverted to AF!")
				if cam := m.router.ActiveCamera(); cam != nil {
					visca.SetManualFocus(cam)
				}
			}
		}
	}
}

// activeCamAddrIs reports whether the currently active camera matches the given addr.
// Used to discard inquiry replies that arrived after a camera switch.
func (m *model) activeCamAddrIs(addr string) bool {
	cam := m.router.ActiveCamera()
	return cam != nil && cam.Addr() == addr
}

// btn reads a button state safely, returning false for unmapped buttons (index < 0 or out of range).
func btn(buttons [12]bool, index int) bool {
	if index < 0 || index >= len(buttons) {
		return false
	}
	return buttons[index]
}

// btnEdge returns true on a rising edge (pressed this tick, not last tick).
func btnEdge(buttons, prev [12]bool, index int) bool {
	return btn(buttons, index) && !btn(prev, index)
}

// btnRelease returns true on a falling edge (released this tick).
func btnRelease(buttons, prev [12]bool, index int) bool {
	return !btn(buttons, index) && btn(prev, index)
}

// hatEdge returns true when the hat just moved to the given direction.
func hatEdge(hat, prevHat, direction int) bool {
	return hat == direction && prevHat != direction
}

func (m *model) startMapStep() {
	m.mapDetected = -1
	m.mapSettled = false
	if m.joyReady {
		m.mapBaseAxes = m.latestJoy.Axes
		m.mapBaseButtons = m.latestJoy.Buttons
	}
}

func (m *model) updateMapDetection() {
	if !m.joyReady {
		return
	}
	state := m.latestJoy

	if m.mapStep < mapBtnAFHold {
		// Axis detection: find axis with largest absolute movement from baseline
		bestAxis := -1
		bestDelta := 0.3 // minimum threshold to detect
		for i := 0; i < 6; i++ {
			delta := math.Abs(state.Axes[i] - m.mapBaseAxes[i])
			if delta > bestDelta {
				bestDelta = delta
				bestAxis = i
			}
		}
		if bestAxis >= 0 {
			m.mapDetected = bestAxis
			m.mapSettled = true
		}
	} else {
		// Button detection: find first button that's pressed now but wasn't at baseline
		for i := 0; i < 12; i++ {
			if state.Buttons[i] && !m.mapBaseButtons[i] {
				m.mapDetected = i
				m.mapSettled = true
				return
			}
		}
	}
}

func (m *model) applyMapStep() {
	idx := m.mapDetected
	switch m.mapStep {
	case mapAxisPan:
		m.mapCfg.Axes.Pan.Index = idx
	case mapAxisTilt:
		m.mapCfg.Axes.Tilt.Index = idx
	case mapAxisZoom:
		m.mapCfg.Axes.Zoom.Index = idx
	case mapBtnAFHold:
		m.mapCfg.Buttons.AFHold = idx
	case mapBtnAFLatch:
		m.mapCfg.Buttons.AFLatch = idx
	case mapBtnProgramOverride:
		m.mapCfg.Buttons.ProgramOverride = idx
	case mapBtnFade:
		m.mapCfg.Buttons.Fade = idx
	case mapBtnCut:
		m.mapCfg.Buttons.Cut = idx
	case mapBtnCyclePreview:
		m.mapCfg.Buttons.CyclePreview = idx
	case mapBtnPresetSave:
		m.mapCfg.Buttons.PresetSave = idx
	case mapBtnPreset1:
		m.mapCfg.Buttons.Presets[0] = idx
	case mapBtnPreset2:
		m.mapCfg.Buttons.Presets[1] = idx
	case mapBtnPreset3:
		m.mapCfg.Buttons.Presets[2] = idx
	case mapBtnPreset4:
		m.mapCfg.Buttons.Presets[3] = idx
	case mapBtnPreset5:
		m.mapCfg.Buttons.Presets[4] = idx
	case mapBtnPreset6:
		m.mapCfg.Buttons.Presets[5] = idx
	}
}

func (m *model) handleDeckPress(button int) {
	layout := deck.GetLayout(m.deckNumKeys)
	action := layout[button]
	cam := m.router.ActiveCamera()

	// If in preset save mode, check if a preset button was pressed
	if m.deckPresetSave {
		presetIdx := -1
		switch action {
		case deck.ActionPreset1:
			presetIdx = 0
		case deck.ActionPreset2:
			presetIdx = 1
		case deck.ActionPreset3:
			presetIdx = 2
		case deck.ActionPreset4:
			presetIdx = 3
		case deck.ActionPreset5:
			presetIdx = 4
		case deck.ActionPreset6:
			presetIdx = 5
		case deck.ActionPreset7:
			presetIdx = 6
		case deck.ActionPreset8:
			presetIdx = 7
		}
		if presetIdx >= 0 && cam != nil {
			slot := byte(presetSlotOffset + presetIdx)
			visca.PresetSave(cam, slot)
			m.addLog(fmt.Sprintf("Deck: SAVED preset %d", presetIdx+1))
			m.cmdCount++
		}
		m.deckPresetSave = false
		return
	}

	switch action {
	case deck.ActionPreset1, deck.ActionPreset2, deck.ActionPreset3,
		deck.ActionPreset4, deck.ActionPreset5, deck.ActionPreset6,
		deck.ActionPreset7, deck.ActionPreset8:
		presetIdx := int(action - deck.ActionPreset1)
		if cam != nil {
			slot := byte(presetSlotOffset + presetIdx)
			visca.PresetRecall(cam, slot)
			m.activePresets[m.router.TargetInput()] = presetIdx + 1
			m.addLog(fmt.Sprintf("Deck: recall preset %d", presetIdx+1))
			m.cmdCount++
		}

	case deck.ActionPresetSave:
		m.deckPresetSave = true
		m.addLog("Deck: press a preset to save...")

	case deck.ActionCut:
		if m.vmixCmd != nil {
			if err := m.vmixCmd.Cut(); err != nil {
				m.addLog(fmt.Sprintf("Deck cut error: %v", err))
			} else {
				m.addLog("Deck: cut")
			}
		}

	case deck.ActionFade:
		if m.vmixCmd != nil {
			if err := m.vmixCmd.Fade(*flagFadeDuration); err != nil {
				m.addLog(fmt.Sprintf("Deck fade error: %v", err))
			} else {
				m.addLog(fmt.Sprintf("Deck: fade %dms", *flagFadeDuration))
			}
		}

	case deck.ActionAFToggle:
		// Hold for AF — press enters AF, release returns to MF
		if cam != nil {
			visca.FocusStop(cam)
			m.lastFocusJog = 0
			visca.SetAutoFocus(cam)
			m.manualFocus = false
			m.afLatched = false
			m.addLog("Deck: AF (hold)")
		}

	case deck.ActionOverrideToggle:
		oldTarget := m.router.TargetInput()
		if m.router.SetOverride(!m.router.Override) {
			m.router.HandleSwitch(oldTarget)
			if oldTarget > 0 {
				m.lastPan[oldTarget] = 0
				m.lastTilt[oldTarget] = 0
			}
			m.lastZoom = 0
			m.lastFocusJog = 0
			m.switchMute = true
			m.posQueried = false
			m.exposureQueried = false
		}
		if m.router.Override {
			m.addLog("Deck: override ON")
		} else {
			m.addLog("Deck: override OFF")
		}

	case deck.ActionInput1, deck.ActionInput2, deck.ActionInput3,
		deck.ActionInput4, deck.ActionInput5, deck.ActionInput6,
		deck.ActionInput7, deck.ActionInput8, deck.ActionInput9,
		deck.ActionInput10:
		slotIdx := int(action - deck.ActionInput1)
		if slotIdx < len(m.vmixInputs) && m.vmixCmd != nil {
			inp := m.vmixInputs[slotIdx]
			if err := m.vmixCmd.SetPreview(inp.Number); err != nil {
				m.addLog(fmt.Sprintf("Deck preview error: %v", err))
			} else {
				m.addLog(fmt.Sprintf("Deck: preview %s", inp.Title))
			}
		}

	// PTZ controls — start motion on press
	case deck.ActionPanLeft:
		if cam != nil {
			visca.PanTiltVariable(cam, scaleSpeed(-maxPanSpeed/2, m.camZoomPos), 0)
			delete(m.activePresets, m.router.TargetInput())
		}
	case deck.ActionPanRight:
		if cam != nil {
			visca.PanTiltVariable(cam, scaleSpeed(maxPanSpeed/2, m.camZoomPos), 0)
			delete(m.activePresets, m.router.TargetInput())
		}
	case deck.ActionTiltUp:
		if cam != nil {
			visca.PanTiltVariable(cam, 0, scaleSpeed(-maxTiltSpeed/2, m.camZoomPos))
			delete(m.activePresets, m.router.TargetInput())
		}
	case deck.ActionTiltDown:
		if cam != nil {
			visca.PanTiltVariable(cam, 0, scaleSpeed(maxTiltSpeed/2, m.camZoomPos))
			delete(m.activePresets, m.router.TargetInput())
		}
	case deck.ActionZoomIn:
		if cam != nil {
			if m.manualFocus && !m.zoomAF {
				visca.SetAutoFocus(cam)
				m.manualFocus = false
				m.zoomAF = true
			}
			visca.ZoomVariable(cam, maxZoomSpeed/2)
			delete(m.activePresets, m.router.TargetInput())
		}
	case deck.ActionZoomOut:
		if cam != nil {
			if m.manualFocus && !m.zoomAF {
				visca.SetAutoFocus(cam)
				m.manualFocus = false
				m.zoomAF = true
			}
			visca.ZoomVariable(cam, -maxZoomSpeed/2)
			delete(m.activePresets, m.router.TargetInput())
		}
	case deck.ActionFocusFar:
		if cam != nil {
			visca.SetManualFocus(cam)
			m.manualFocus = true
			m.afLatched = false
			m.lastFocusJog = -1
			m.focusPressAt = time.Now()
			m.focusHeldJog = false
			m.startFocusStep(1) // FocusDirect position increases toward far on this camera
			m.addLog("Deck: focus far (step)")
			m.cmdCount++
		}
	case deck.ActionFocusNear:
		if cam != nil {
			visca.SetManualFocus(cam)
			m.manualFocus = true
			m.afLatched = false
			m.lastFocusJog = 1
			m.focusPressAt = time.Now()
			m.focusHeldJog = false
			m.startFocusStep(-1) // FocusDirect position decreases toward near on this camera
			m.addLog("Deck: focus near (step)")
			m.cmdCount++
		}
	case deck.ActionPTZHome:
		if cam != nil {
			visca.PresetRecall(cam, byte(presetSlotOffset))
			m.addLog("Deck: home (preset 1)")
			m.cmdCount++
		}

	case deck.ActionSlideNext:
		if m.proclaimCmd != nil {
			if err := m.proclaimCmd.NextSlide(); err != nil {
				m.addLog(fmt.Sprintf("Deck slide error: %v", err))
			} else {
				m.addLog("Deck: next slide")
			}
		}

	case deck.ActionSlidePrev:
		if m.proclaimCmd != nil {
			if err := m.proclaimCmd.PreviousSlide(); err != nil {
				m.addLog(fmt.Sprintf("Deck slide error: %v", err))
			} else {
				m.addLog("Deck: prev slide")
			}
		}

	case deck.ActionOverlay:
		if m.vmixCmd != nil && m.overlayInput > 0 {
			if err := m.vmixCmd.OverlayToggle(1, m.overlayInput); err != nil {
				m.addLog(fmt.Sprintf("Deck overlay error: %v", err))
			} else {
				m.overlayActive = !m.overlayActive
				if m.overlayActive {
					m.addLog("Deck: overlay ON")
				} else {
					m.addLog("Deck: overlay OFF")
				}
			}
		}

	case deck.ActionStream:
		if m.vmixCmd != nil {
			now := time.Now()
			if !m.streamArmed.IsZero() && now.Sub(m.streamArmed) < 2*time.Second {
				// Second tap — execute
				m.streamArmed = time.Time{}
				if m.streaming {
					if err := m.vmixCmd.StopStreaming(); err != nil {
						m.addLog(fmt.Sprintf("Deck stream stop error: %v", err))
					} else {
						m.streaming = false
						m.addLog("Deck: stream STOPPED")
					}
				} else {
					if err := m.vmixCmd.StartStreaming(); err != nil {
						m.addLog(fmt.Sprintf("Deck stream start error: %v", err))
					} else {
						m.streaming = true
						m.addLog("Deck: stream STARTED")
					}
				}
			} else {
				// First tap — arm
				m.streamArmed = now
				m.addLog("Deck: tap again to toggle stream")
			}
		}
	}
}

func (m *model) handleDeckRelease(button int) {
	layout := deck.GetLayout(m.deckNumKeys)
	action := layout[button]

	if !deck.IsHoldAction(action) {
		return
	}

	cam := m.router.ActiveCamera()
	if cam == nil {
		return
	}

	switch action {
	case deck.ActionPanLeft, deck.ActionPanRight, deck.ActionTiltUp, deck.ActionTiltDown:
		visca.PanTiltVariable(cam, 0, 0)
	case deck.ActionZoomIn, deck.ActionZoomOut:
		visca.ZoomVariable(cam, 0)
		if m.zoomAF {
			visca.SetManualFocus(cam)
			m.manualFocus = true
			m.zoomAF = false
		}
	case deck.ActionFocusFar, deck.ActionFocusNear:
		visca.FocusStop(cam)
		m.lastFocusJog = 0
		m.focusHeldJog = false
		m.focusPulseOn = false
	case deck.ActionAFToggle:
		// Release — back to MF.
		visca.SetManualFocus(cam)
		m.manualFocus = true
		m.afLatched = false
		m.addLog("Deck: MF (released)")
	}
}

func (m *model) sendDeckFeedback() {
	if m.deckFeedback == nil {
		return
	}

	// Build tally map for all known inputs
	tally := make(map[int]string)
	for _, inp := range m.vmixInputs {
		tally[inp.Number] = m.router.TallyState(inp.Number)
	}
	// Also include PTZ-only cameras (single-cam mode)
	for _, num := range m.router.InputNumbers() {
		if _, ok := tally[num]; !ok {
			tally[num] = m.router.TallyState(num)
		}
	}

	// Build input slots with short labels
	var slots []deck.InputSlot
	for _, inp := range m.vmixInputs {
		slots = append(slots, deck.InputSlot{
			Number: inp.Number,
			Label:  shortLabel(inp.Title),
		})
	}
	// Single-cam fallback (no vMix inputs discovered)
	if len(slots) == 0 {
		for _, num := range m.router.InputNumbers() {
			ip := m.router.CameraIPs[num]
			slots = append(slots, deck.InputSlot{
				Number: num,
				Label:  fmt.Sprintf("CAM%d", num),
			})
			_ = ip
		}
	}

	state := deck.FeedbackState{
		CameraTally:    tally,
		Inputs:         slots,
		AFActive:       !m.manualFocus,
		AFLatched:      m.afLatched,
		OverrideActive: m.router.Override,
		Streaming:      m.streaming,
		PresetSaveMode: m.deckPresetSave,
		ActivePreset:   m.activePresets[m.router.TargetInput()],
		OverlayActive:  m.overlayActive,
		FocusJog:       m.lastFocusJog,
	}

	select {
	case m.deckFeedback <- state:
	default:
	}
}

// shortLabel derives a short display label from a vMix input title.
// Returns up to two lines separated by "\n" for longer names.
// Each line fits on a 96px Stream Deck key (max ~7 chars at 2x scale).
//
// Examples:
//
//	"Side"            -> "SIDE"
//	"Primary"         -> "PRIMARY"
//	"Lower Thirds"    -> "LOWER\nTHIRDS"
//	"NDI HD CAMERA (NDI HX2,192.168.1.51)" -> "CAM\n.51"
//	"Audio Microphone" -> "AUDIO"
//	"AVMATRIX USB Capture Video" -> "AVMAT\nCAPTUR"
func shortLabel(title string) string {
	upper := strings.ToUpper(title)
	const maxLine = 7

	// NDI cameras: show "CAM" + last IP octet on two lines
	if strings.HasPrefix(upper, "NDI HD CAMERA") {
		parts := strings.FieldsFunc(title, func(r rune) bool {
			return r == '(' || r == ')' || r == ',' || r == ' '
		})
		for _, p := range parts {
			octets := strings.Split(p, ".")
			if len(octets) == 4 {
				return "CAM\n." + octets[3]
			}
		}
		return "CAM"
	}

	// NDI sources: use the part after " - " (e.g. "Proclaim - Lower Thirds")
	if strings.HasPrefix(upper, "NDI ") {
		if idx := strings.LastIndex(title, " - "); idx >= 0 {
			t := strings.TrimSpace(title[idx+3:])
			t = strings.TrimRight(t, ")")
			tu := strings.ToUpper(t)
			if strings.HasPrefix(tu, "VMIX") {
				t = t[4:]
			}
			t = strings.ToUpper(strings.TrimSpace(t))
			return fitTwoLines(t, maxLine)
		}
		t := strings.TrimPrefix(upper, "NDI ")
		if idx := strings.IndexAny(t, " .("); idx > 0 {
			t = t[:idx]
		}
		return fitTwoLines(t, maxLine)
	}

	// Audio sources
	if strings.Contains(upper, "AUDIO") || strings.Contains(upper, "MICROPHONE") {
		return "AUDIO"
	}

	// Generic: strip parenthetical, use meaningful words
	t := upper
	if idx := strings.Index(t, "("); idx > 0 {
		t = strings.TrimSpace(t[:idx])
	}
	words := strings.Fields(t)
	// Filter generic words
	filtered := make([]string, 0, len(words))
	for _, w := range words {
		if w == "USB" || w == "VIDEO" {
			continue
		}
		filtered = append(filtered, w)
	}
	if len(filtered) == 0 {
		filtered = words
	}
	if len(filtered) == 0 {
		return "?"
	}

	return fitTwoLines(strings.Join(filtered, " "), maxLine)
}

// fitTwoLines splits text to fit on Stream Deck keys.
// If it fits on one line, returns as-is. Otherwise splits on space
// or truncates to two lines.
func fitTwoLines(text string, maxLine int) string {
	if len(text) <= maxLine {
		return text
	}

	// Try splitting on space
	words := strings.Fields(text)
	if len(words) >= 2 {
		line1 := words[0]
		line2 := strings.Join(words[1:], " ")
		if len(line1) > maxLine {
			line1 = line1[:maxLine]
		}
		if len(line2) > maxLine {
			line2 = line2[:maxLine]
		}
		return line1 + "\n" + line2
	}

	// Single long word: split at maxLine
	return text[:maxLine] + "\n" + text[maxLine:]
}

// toggleAutoTrackFor flips the per-cam auto-track state for one input. If
// disabling, it sends StopAllMotion and resets the per-cam last speed cache
// so the cam doesn't drift after the next tick. The label ("program" /
// "preview") is just for the operator-facing log line.
func (m *model) toggleAutoTrackFor(num int, label string) {
	if m.trackerCh == nil {
		m.addLog("Auto-track disabled (no --tracker-port)")
		return
	}
	if num == 0 {
		m.addLog(fmt.Sprintf("No %s cam to toggle", label))
		return
	}
	cam := m.router.Cameras[num]
	if cam == nil {
		m.addLog(fmt.Sprintf("Cam %d not connected", num))
		return
	}
	m.autoTrackEnabled[num] = !m.autoTrackEnabled[num]
	if m.autoTrackEnabled[num] {
		m.addLog(fmt.Sprintf("Auto-track ON  cam %d (%s)", num, label))
	} else {
		// Stop motion immediately; reset cached last speed for clean re-engage
		visca.PanTiltVariable(cam, 0, 0)
		m.lastPan[num] = 0
		m.lastTilt[num] = 0
		m.addLog(fmt.Sprintf("Auto-track OFF cam %d (%s)", num, label))
	}
}

// eventToError derives the normalized PD error (-1..+1, half-frame units)
// from an event's absolute aim point and the framing target for that mode.
func (m *model) eventToError(ev tracker.Event) (dx, dy float64) {
	tx, ok := m.framingTargetX[ev.Mode]
	if !ok {
		tx = 0.5
	}
	ty, ok := m.framingTargetY[ev.Mode]
	if !ok {
		ty = 0.5
	}
	// aim and target are in [0,1]; double the delta so the magnitude matches
	// the old "normalized to half-frame" convention (|dx|=1 means at frame edge).
	return (ev.Ax - tx) * 2.0, (ev.Ay - ty) * 2.0
}

// processAutoTrack drives pan/tilt for every cam with auto-track enabled,
// independent of which cam is currently routed. Each cam runs its own PD
// controller against its own tracker error, with its own lastPan/lastTilt
// change-detection. Zoom scaling uses m.camZoomPos (accurate only for the
// currently active cam) — non-active cams' zoom isn't polled.
func (m *model) processAutoTrack() {
	if m.trackerCh == nil || len(m.autoTrackEnabled) == 0 {
		return
	}
	for num, enabled := range m.autoTrackEnabled {
		if !enabled {
			continue
		}
		cam := m.router.Cameras[num]
		if cam == nil {
			continue
		}
		ip := m.router.CameraIPs[num]
		ev, ok := m.trackerErr[ip]
		var panSpeed, tiltSpeed int
		if ok && time.Since(ev.At) < m.trackerStaleAfter {
			dx, dy := m.eventToError(ev)
			var prevDx, prevDy float64
			prev, hasPrev := m.trackerPrev[ip]
			if hasPrev {
				prevDx, prevDy = m.eventToError(prev)
			}
			panSpeed = trackerSpeed(dx, prevDx, ev.At, prev.At, hasPrev,
				m.trackerKp, m.trackerKd, m.trackerDeadband, maxPanSpeed)
			tiltSpeed = trackerSpeed(dy, prevDy, ev.At, prev.At, hasPrev,
				m.trackerKp, m.trackerKd, m.trackerDeadband, maxTiltSpeed)
			panSpeed = scaleSpeed(panSpeed, m.camZoomPos)
			tiltSpeed = scaleSpeed(tiltSpeed, m.camZoomPos)
		}
		if panSpeed != m.lastPan[num] || tiltSpeed != m.lastTilt[num] {
			visca.PanTiltVariable(cam, panSpeed, tiltSpeed)
			m.lastPan[num] = panSpeed
			m.lastTilt[num] = tiltSpeed
			m.cmdCount++
			if panSpeed != 0 || tiltSpeed != 0 {
				delete(m.activePresets, num)
			}
		}
	}
}

// startFocusStep fires one FocusDirect nudge: it queries the current focus
// position, and handleViscaReply applies dir*focusStep (dir: -1 far, +1 near)
// when the reply lands. Called on a FOCUS button press, which also arms the
// hold-to-jog timer (focusPressAt).
func (m *model) startFocusStep(dir int) {
	if cam := m.router.ActiveCamera(); cam != nil {
		cam.InquireFocus(queryFocusStep{dir: dir})
	}
}

// processFocusHold escalates a held FOCUS button to a continuous speed-1 jog
// once focusHoldMs has elapsed. The tap-time FocusDirect step has already
// fired; this is the coarse-travel mode for a sustained hold.
func (m *model) processFocusHold() {
	if m.lastFocusJog == 0 || m.focusHeldJog {
		return
	}
	if time.Since(m.focusPressAt) < time.Duration(focusHoldMs)*time.Millisecond {
		return
	}
	cam := m.router.ActiveCamera()
	if cam == nil {
		return
	}
	m.focusHeldJog = true
	if m.lastFocusJog < 0 {
		visca.FocusFar(cam, maxFocusSpeed)
	} else {
		visca.FocusNear(cam, maxFocusSpeed)
	}
	m.cmdCount++
	m.addLog("Focus jog (held)")
}

// startFocusPulse / processFocusPulse implement a PWM jog (sub-speed-1 via
// duty cycling). Currently BYPASSED in favor of the step+hold model above,
// but retained for possible re-enable — wire processFocusPulse back into the
// tick loop and call startFocusPulse from the FOCUS press handlers.
func (m *model) startFocusPulse() {
	m.focusPulseOn = true
	m.focusPulseAt = time.Now()
}

func (m *model) processFocusPulse() {
	if m.lastFocusJog == 0 || focusPulseOffMs <= 0 {
		return
	}
	cam := m.router.ActiveCamera()
	if cam == nil {
		return
	}
	now := time.Now()
	if m.focusPulseOn {
		if now.Sub(m.focusPulseAt) >= time.Duration(focusPulseOnMs)*time.Millisecond {
			visca.FocusStop(cam)
			m.focusPulseOn = false
			m.focusPulseAt = now
		}
	} else if now.Sub(m.focusPulseAt) >= time.Duration(focusPulseOffMs)*time.Millisecond {
		if m.lastFocusJog < 0 {
			visca.FocusFar(cam, maxFocusSpeed)
		} else {
			visca.FocusNear(cam, maxFocusSpeed)
		}
		m.focusPulseOn = true
		m.focusPulseAt = now
	}
}

func (m *model) processJoystick() {
	state := m.latestJoy
	cam := m.router.ActiveCamera()
	b := m.cfg.Buttons
	a := m.cfg.Axes

	// --- Button edge detection ---
	buttons := state.Buttons

	// --- Focus mode: af_hold=hold-for-AF, af_latch=latch modifier ---
	// AF hold pressed
	if btnEdge(buttons, m.prevButtons, b.AFHold) && cam != nil {
		if m.afLatched {
			// Unlatching — back to MF
			visca.SetManualFocus(cam)
			m.manualFocus = true
			m.afLatched = false
			m.addLog("AF unlatched")
		} else {
			// Enter AF
			visca.FocusStop(cam)
			m.lastFocusJog = 0
			visca.SetAutoFocus(cam)
			m.manualFocus = false
			m.addLog("AF (held)")
		}
	}

	// AF hold released (normal hold-for-AF)
	if btnRelease(buttons, m.prevButtons, b.AFHold) && cam != nil && !m.afLatched {
		if !m.manualFocus {
			visca.SetManualFocus(cam)
			m.manualFocus = true
		}
	}

	// AF latch modifier while in AF
	if !m.manualFocus && !m.afLatched && btnEdge(buttons, m.prevButtons, b.AFLatch) {
		m.afLatched = true
		m.addLog("AF latched")
	}

	// Override button
	if config.BtnMapped(b.ProgramOverride) {
		if btn(buttons, b.ProgramOverride) != btn(m.prevButtons, b.ProgramOverride) {
			oldTarget := m.router.TargetInput()
			if m.router.SetOverride(btn(buttons, b.ProgramOverride)) {
				m.router.HandleSwitch(oldTarget)
				if oldTarget > 0 {
					m.lastPan[oldTarget] = 0
					m.lastTilt[oldTarget] = 0
				}
				m.lastZoom = 0
				m.lastFocusJog = 0
				m.switchMute = true // suppress until stick returns to center
				m.posQueried = false
				m.exposureQueried = false
			}
		}
	}

	// Preset buttons
	saveHeld := btn(buttons, b.PresetSave)
	for i, pBtn := range b.Presets {
		if config.BtnMapped(pBtn) && btnEdge(buttons, m.prevButtons, pBtn) {
			if cam != nil {
				slot := byte(presetSlotOffset + i)
				if saveHeld {
					visca.PresetSave(cam, slot)
					m.addLog(fmt.Sprintf("Saved preset %d (slot %d)", i+1, presetSlotOffset+i))
				} else {
					visca.PresetRecall(cam, slot)
					m.activePresets[m.router.TargetInput()] = i + 1
					m.addLog(fmt.Sprintf("Recall preset %d (slot %d)", i+1, presetSlotOffset+i))
				}
				m.cmdCount++
			}
		}
	}

	// --- vMix buttons ---
	if m.vmixCmd != nil {
		if config.BtnMapped(b.Fade) && btnEdge(buttons, m.prevButtons, b.Fade) {
			if err := m.vmixCmd.Fade(*flagFadeDuration); err != nil {
				m.addLog(fmt.Sprintf("Fade error: %v", err))
			} else {
				m.addLog(fmt.Sprintf("Fade %dms", *flagFadeDuration))
			}
		}
		if config.BtnMapped(b.Cut) && btnEdge(buttons, m.prevButtons, b.Cut) {
			if err := m.vmixCmd.Cut(); err != nil {
				m.addLog(fmt.Sprintf("Cut error: %v", err))
			} else {
				m.addLog("Cut")
			}
		}
		if config.BtnMapped(b.CyclePreview) && btnEdge(buttons, m.prevButtons, b.CyclePreview) {
			if m.vmixNumInputs > 0 {
				if err := m.vmixCmd.NextPreview(m.vmixNumInputs, m.router.Preview); err != nil {
					m.addLog(fmt.Sprintf("Preview cycle error: %v", err))
				} else {
					m.addLog("Preview next")
				}
			}
		}
	}

	m.prevButtons = buttons

	// --- Hat: shutter/gain stepping ---
	hat := state.Hat
	if cam != nil {
		if hatEdge(hat, m.prevHat, input.HatUp) && m.camShutter < shutterMax {
			m.camShutter++
			visca.ShutterDirect(cam, m.camShutter)
			m.cmdCount++
			m.addLog(fmt.Sprintf("Shutter → %d", m.camShutter))
		}
		if hatEdge(hat, m.prevHat, input.HatDown) && m.camShutter > shutterMin {
			m.camShutter--
			visca.ShutterDirect(cam, m.camShutter)
			m.cmdCount++
			m.addLog(fmt.Sprintf("Shutter → %d", m.camShutter))
		}
		if hatEdge(hat, m.prevHat, input.HatRight) && m.camGain < gainMax {
			m.camGain++
			visca.GainDirect(cam, m.camGain)
			m.cmdCount++
			m.addLog(fmt.Sprintf("Gain → %d", m.camGain))
		}
		if hatEdge(hat, m.prevHat, input.HatLeft) && m.camGain > gainMin {
			m.camGain--
			visca.GainDirect(cam, m.camGain)
			m.cmdCount++
			m.addLog(fmt.Sprintf("Gain → %d", m.camGain))
		}
	}
	m.prevHat = hat

	if cam == nil {
		return
	}

	// --- Pan/Tilt ---
	panAxis := state.Axes[a.Pan.Index]
	if a.Pan.Inverted {
		panAxis = -panAxis
	}
	panSpeed := scaleSpeed(input.AxisToSpeed(panAxis, a.Pan.Deadzone, maxPanSpeed, a.Pan.Expo), m.camZoomPos)

	tiltAxis := state.Axes[a.Tilt.Index]
	if a.Tilt.Inverted {
		tiltAxis = -tiltAxis
	}
	tiltSpeed := scaleSpeed(input.AxisToSpeed(tiltAxis, a.Tilt.Deadzone, maxTiltSpeed, a.Tilt.Expo), m.camZoomPos)

	zoomAxis := state.Axes[a.Zoom.Index]
	if a.Zoom.Inverted {
		zoomAxis = -zoomAxis
	}
	zoomSpeed := input.AxisToSpeed(zoomAxis, a.Zoom.Deadzone, maxZoomSpeed, a.Zoom.Expo)

	// switchMute is a joystick safety: after a cam switch, suppress motion
	// until the operator has re-centered. Auto-tracked cams bypass it
	// (their motion is intentional, owned by processAutoTrack).
	activeNum := m.router.TargetInput()
	activeAutoTracked := m.autoTrackEnabled[activeNum]
	if m.switchMute && !activeAutoTracked {
		if panSpeed == 0 && tiltSpeed == 0 && zoomSpeed == 0 {
			m.switchMute = false
		} else {
			panSpeed = 0
			tiltSpeed = 0
			zoomSpeed = 0
		}
	} else if m.switchMute && activeAutoTracked {
		m.switchMute = false
	}

	// Skip joystick pan/tilt entirely when the active cam is auto-tracked —
	// processAutoTrack owns its pan/tilt this tick.
	if !activeAutoTracked {
		if panSpeed != m.lastPan[activeNum] || tiltSpeed != m.lastTilt[activeNum] {
			visca.PanTiltVariable(cam, panSpeed, tiltSpeed)
			m.lastPan[activeNum] = panSpeed
			m.lastTilt[activeNum] = tiltSpeed
			m.cmdCount++
			if panSpeed != 0 || tiltSpeed != 0 {
				delete(m.activePresets, activeNum)
			}
		}
	}

	// --- Zoom (with auto-AF during zoom) ---
	// Edge-trigger off the joystick's own zoom transitions. m.zoomAF is shared
	// with the Stream Deck zoom buttons, so the AF→MF restore must NOT fire
	// just because joystick zoomSpeed happens to be 0 — that would interrupt a
	// deck-driven zoom. The deck press/release handlers manage their own
	// AF/MF flips.
	if zoomSpeed != m.lastZoom {
		visca.ZoomVariable(cam, zoomSpeed)
		m.cmdCount++
		if zoomSpeed != 0 {
			delete(m.activePresets, m.router.TargetInput())
		}

		if zoomSpeed != 0 && m.manualFocus && !m.zoomAF {
			// Joystick zoom just started — switch to AF to prevent focus drift
			visca.SetAutoFocus(cam)
			m.manualFocus = false
			m.zoomAF = true
		} else if zoomSpeed == 0 && m.lastZoom != 0 && m.zoomAF {
			// Joystick zoom just stopped — restore MF.
			visca.SetManualFocus(cam)
			m.manualFocus = true
			m.zoomAF = false
		}
		m.lastZoom = zoomSpeed
	}

	now := time.Now()

	// --- Position queries (only when camera is idle) ---
	activeNumForIdle := m.router.TargetInput()
	idle := m.lastPan[activeNumForIdle] == 0 && m.lastTilt[activeNumForIdle] == 0 && m.lastZoom == 0
	queryInterval := time.Second
	if !m.posQueried {
		queryInterval = 0 // query immediately on first tick
	}
	if idle && now.Sub(m.lastQueryTime) >= queryInterval {
		m.lastQueryTime = now
		switch m.queryPhase {
		case 0:
			cam.InquirePanTilt(queryPanTilt{})
		case 1:
			cam.InquireZoom(queryZoom{})
		case 2:
			cam.InquireShutter(queryShutter{})
		case 3:
			cam.InquireGain(queryGain{})
		case 4:
			cam.InquireFocusMode(queryFocusMode{})
		}
		m.queryPhase = (m.queryPhase + 1) % 5
	}
}

// zoomScale returns a speed multiplier based on current zoom position.
// Wide (0x0000) = 1.0, full tele (0x4000) = 0.2. Linear interpolation.
func zoomScale(zoomPos uint16) float64 {
	const minScale = 0.2
	ratio := float64(zoomPos) / float64(zoomMax)
	return 1.0 - ratio*(1.0-minScale)
}

// scaleSpeed applies zoom-adaptive scaling to a pan/tilt speed value.
// trackerSpeed converts a normalized error (-1..+1, frame-edge units) into a
// VISCA pan/tilt speed using a PD controller, with a deadband and clamp.
// Returns 0 for |err| below deadband. D is skipped when there is no usable
// previous sample (first event, ID change, or an implausibly long dt).
func trackerSpeed(err, prevErr float64, at, prevAt time.Time, hasPrev bool,
	kp, kd, deadband float64, maxSpeed int) int {
	if math.Abs(err) < deadband {
		return 0
	}
	v := err * kp
	if hasPrev && kd != 0 {
		dt := at.Sub(prevAt).Seconds()
		if dt > 0 && dt < 0.5 { // skip stale prev (> 500ms gap is meaningless for D)
			v += kd * (err - prevErr) / dt
		}
	}
	if v > float64(maxSpeed) {
		v = float64(maxSpeed)
	} else if v < -float64(maxSpeed) {
		v = -float64(maxSpeed)
	}
	out := int(math.Round(v))
	if out == 0 {
		// Preserve direction at very low magnitudes (speed=1 is the slowest VISCA step)
		if err > 0 {
			return 1
		}
		return -1
	}
	return out
}

func scaleSpeed(speed int, zoomPos uint16) int {
	if speed == 0 {
		return 0
	}
	scaled := int(float64(speed) * zoomScale(zoomPos))
	// Preserve direction — minimum magnitude of 1
	if scaled == 0 {
		if speed > 0 {
			return 1
		}
		return -1
	}
	return scaled
}

// --- TUI styles and rendering ---

var (
	// Colors
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	accentDim  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	white      = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	bold       = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	cyan       = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	green      = lipgloss.NewStyle().Foreground(lipgloss.Color("84"))
	red        = lipgloss.NewStyle().Foreground(lipgloss.Color("197"))
	yellow     = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	orange     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	pink       = lipgloss.NewStyle().Foreground(lipgloss.Color("211"))
	redBold    = lipgloss.NewStyle().Foreground(lipgloss.Color("197")).Bold(true)
	greenBold  = lipgloss.NewStyle().Foreground(lipgloss.Color("84")).Bold(true)

	headerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Bold(true)
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Width(7).Align(lipgloss.Right)
	valueStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1)

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("81")).
			Bold(true)
)

const barWidth = 30

// centeredBar renders a track with center mark and position marker.
func centeredBar(pos, minPos, maxPos, width int) string {
	bar := make([]rune, width)
	for i := range bar {
		bar[i] = '─'
	}
	center := width / 2
	bar[center] = '┼'

	posRange := maxPos - minPos
	if posRange <= 0 {
		return string(bar)
	}

	idx := (pos - minPos) * (width - 1) / posRange
	if idx < 0 {
		idx = 0
	}
	if idx >= width {
		idx = width - 1
	}
	bar[idx] = '◆'

	return string(bar)
}

// positionBar renders a left-to-right fill bar.
func positionBar(pos, minPos, maxPos, width int) string {
	bar := make([]rune, width)
	for i := range bar {
		bar[i] = '░'
	}
	posRange := maxPos - minPos
	if posRange <= 0 {
		return string(bar)
	}
	filled := (pos - minPos) * width / posRange
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	for i := 0; i < filled; i++ {
		bar[i] = '█'
	}
	return string(bar)
}

// axisLabel returns a display name for a Windows axis index.
func axisLabel(idx int) string {
	switch idx {
	case 0:
		return "X"
	case 1:
		return "Y"
	case 2:
		return "Z"
	case 3:
		return "R"
	case 4:
		return "U"
	case 5:
		return "V"
	default:
		return "?"
	}
}

func (m model) View() string {
	if m.mapping {
		return m.viewMapping()
	}
	if m.framingMode {
		return m.viewFraming()
	}

	var content strings.Builder

	// --- Header ---
	header := titleStyle.Render("PRISUAL") + dimStyle.Render(" Camera Shim")
	if m.trackerCh != nil {
		header += dimStyle.Render("   auto-track: T=program, P=preview")
	}
	content.WriteString(header + "\n")

	// --- Camera list ---
	content.WriteString("\n")
	content.WriteString(headerStyle.Render("CAMERAS") + "\n")
	target := m.router.TargetInput()
	for _, num := range m.router.InputNumbers() {
		ip := m.router.CameraIPs[num]
		tally := m.router.TallyState(num)
		cam := m.router.Cameras[num]

		var dot, tallyTag, ctrl string
		switch tally {
		case "PROGRAM":
			dot = red.Render("●")
			tallyTag = redBold.Render("PGM")
		case "PREVIEW":
			dot = green.Render("●")
			tallyTag = greenBold.Render("PVW")
		default:
			dot = dimStyle.Render("○")
			tallyTag = dimStyle.Render(" · ")
		}

		health := dimStyle.Render(" ")
		if cam != nil && !cam.Healthy() {
			health = yellow.Render("!")
		}

		ctrl = ""
		if num == target {
			if tally == "PROGRAM" && m.router.Override {
				ctrl = redBold.Render(" ◀ LIVE")
			} else {
				ctrl = cyan.Render(" ◀")
			}
		}

		atTag := dimStyle.Render("    ")
		if m.autoTrackEnabled[num] {
			ev, ok := m.trackerErr[ip]
			if ok && time.Since(ev.At) < m.trackerStaleAfter {
				atTag = greenBold.Render(" AT ")
			} else {
				atTag = yellow.Render(" AT?")
			}
		}

		content.WriteString(fmt.Sprintf(" %s%s %s %-14s %s%s %s\n",
			dot, health,
			dimStyle.Render(fmt.Sprintf("%d", num)),
			accentDim.Render(ip),
			tallyTag,
			ctrl,
			atTag))
	}

	// --- Axes ---
	content.WriteString("\n")
	if m.joyConnected {
		joyLabel := m.joyName
		if m.cfg.Name != "" && m.cfg.Name != m.joyName {
			joyLabel = m.joyName + " [" + m.cfg.Name + "]"
		}
		content.WriteString(headerStyle.Render("AXES") + dimStyle.Render("  "+joyLabel) + "\n")

		activeViewNum := m.router.TargetInput()
		activePan := m.lastPan[activeViewNum]
		activeTilt := m.lastTilt[activeViewNum]

		// Pan
		panBar := centeredBar(int(m.camPanPos), -panPosRange, panPosRange, barWidth)
		panVal := fmt.Sprintf("%+5d", m.camPanPos)
		panExtra := ""
		if activePan != 0 {
			panExtra = yellow.Render(fmt.Sprintf(" %+3d", activePan))
		}
		content.WriteString(fmt.Sprintf(" %s %s %s%s\n",
			labelStyle.Render("Pan"),
			cyan.Render(panBar),
			valueStyle.Render(panVal),
			panExtra))

		// Tilt
		tiltBar := centeredBar(int(m.camTiltPos), -tiltPosRange, tiltPosRange, barWidth)
		tiltVal := fmt.Sprintf("%+5d", m.camTiltPos)
		tiltExtra := ""
		if activeTilt != 0 {
			tiltExtra = yellow.Render(fmt.Sprintf(" %+3d", activeTilt))
		}
		content.WriteString(fmt.Sprintf(" %s %s %s%s\n",
			labelStyle.Render("Tilt"),
			cyan.Render(tiltBar),
			valueStyle.Render(tiltVal),
			tiltExtra))

		// Zoom
		zoomBar := positionBar(int(m.camZoomPos), 0, zoomMax, barWidth)
		zoomVal := fmt.Sprintf("0x%04X", m.camZoomPos)
		zoomExtra := ""
		if m.lastZoom != 0 {
			zoomExtra = yellow.Render(fmt.Sprintf(" %+2d", m.lastZoom))
		}
		content.WriteString(fmt.Sprintf(" %s %s %s%s\n",
			labelStyle.Render("Zoom"),
			cyan.Render(zoomBar),
			valueStyle.Render(zoomVal),
			zoomExtra))

		// Focus
		content.WriteString("\n")
		content.WriteString(headerStyle.Render("FOCUS") + "\n")
		if !m.manualFocus {
			afBar := strings.Repeat("≈", barWidth)
			if m.afLatched {
				content.WriteString(fmt.Sprintf(" %s %s %s\n",
					labelStyle.Render(""),
					orange.Render(afBar),
					orange.Bold(true).Render("● AF LATCHED")))
			} else {
				content.WriteString(fmt.Sprintf(" %s %s %s\n",
					labelStyle.Render(""),
					pink.Render(afBar),
					pink.Bold(true).Render("● AF")))
			}
		} else {
			focusBar := strings.Repeat("─", barWidth)
			focusLabel := "MANUAL"
			focusStyle := green
			switch {
			case m.lastFocusJog < 0:
				focusBar = "◆" + strings.Repeat("─", barWidth-1)
				focusLabel = "FOCUS FAR"
				focusStyle = yellow
			case m.lastFocusJog > 0:
				focusBar = strings.Repeat("─", barWidth-1) + "◆"
				focusLabel = "FOCUS NEAR"
				focusStyle = yellow
			}
			content.WriteString(fmt.Sprintf(" %s %s %s %s\n",
				labelStyle.Render(""),
				focusStyle.Render(focusBar),
				focusStyle.Render(focusLabel),
				dimStyle.Render("deck hold")))
		}
		// Exposure (shutter/gain)
		content.WriteString("\n")
		content.WriteString(headerStyle.Render("EXPOSURE") + "\n")
		if m.exposureQueried {
			sBar := positionBar(int(m.camShutter), shutterMin, shutterMax, barWidth)
			content.WriteString(fmt.Sprintf(" %s %s %s %s\n",
				labelStyle.Render("Shut"),
				yellow.Render(sBar),
				valueStyle.Render(fmt.Sprintf("%2d", m.camShutter)),
				dimStyle.Render("↕ hat")))
			gBar := positionBar(int(m.camGain), gainMin, gainMax, barWidth)
			content.WriteString(fmt.Sprintf(" %s %s %s %s\n",
				labelStyle.Render("Gain"),
				yellow.Render(gBar),
				valueStyle.Render(fmt.Sprintf("%2d", m.camGain)),
				dimStyle.Render("↔ hat")))
		} else {
			content.WriteString(dimStyle.Render(" querying...") + "\n")
		}
	} else {
		content.WriteString(headerStyle.Render("JOYSTICK") + "\n")
		content.WriteString(dimStyle.Render(" waiting for device...") + "\n")
	}

	// --- Stream Deck ---
	if m.deckCh != nil {
		content.WriteString("\n")
		content.WriteString(headerStyle.Render("STREAM DECK") + "\n")
		if m.deckConnected {
			content.WriteString(green.Render(" ● ") + dimStyle.Render(fmt.Sprintf("%s (%d keys)", m.deckModel, m.deckNumKeys)) + "\n")
		} else {
			content.WriteString(dimStyle.Render(" ○ waiting for device...") + "\n")
		}
	}

	// --- Status bar ---
	content.WriteString("\n")
	var statusParts []string
	if m.vmixHost != "" {
		if m.vmixOK {
			statusParts = append(statusParts, green.Render("●")+dimStyle.Render(" vMix "+m.vmixHost))
		} else {
			statusParts = append(statusParts, red.Render("●")+dimStyle.Render(" vMix "+m.vmixHost))
		}
	}
	statusParts = append(statusParts, dimStyle.Render(fmt.Sprintf("cmds %d", m.cmdCount)))
	content.WriteString(dimStyle.Render(strings.Join(statusParts, "  ")) + "\n")

	// --- Log ---
	if len(m.logLines) > 0 {
		content.WriteString("\n")
		for _, line := range m.logLines {
			content.WriteString(dimStyle.Render(" › "+line) + "\n")
		}
	}

	// --- Help ---
	content.WriteString("\n")
	helpParts := " q quit  m mapping  trigger AF  thumb+trigger latch  base 7-12 presets"
	helpParts += "\n hat ↕ shutter  hat ↔ gain"
	if m.vmixCmd != nil {
		helpParts += "  btn4 fade  btn5 cut  btn6 next preview"
	}
	content.WriteString(dimStyle.Render(helpParts))

	// Wrap in panel
	return "\n" + panelStyle.Render(content.String()) + "\n"
}

// viewFraming renders the live framing editor: a 16:9 ASCII box with a
// crosshair at the framing target and a dot at the latest aim point coming
// in from the tracker.
func (m model) viewFraming() string {
	const (
		boxCols = 56 // characters wide (interior)
		boxRows = 16 // characters tall (interior)
	)

	tx := m.framingTargetX[m.framingEditing]
	ty := m.framingTargetY[m.framingEditing]
	targetCol := int(tx * float64(boxCols-1))
	targetRow := int(ty * float64(boxRows-1))

	// Live aim marker only when the latest event matches the mode being edited
	// and is reasonably fresh, so the editor doesn't show stale data.
	showAim := m.lastAimMode == m.framingEditing &&
		time.Since(m.lastAimAt) < m.trackerStaleAfter
	aimCol, aimRow := -1, -1
	if showAim {
		aimCol = int(m.lastAimX * float64(boxCols-1))
		aimRow = int(m.lastAimY * float64(boxRows-1))
		if aimCol < 0 {
			aimCol = 0
		} else if aimCol >= boxCols {
			aimCol = boxCols - 1
		}
		if aimRow < 0 {
			aimRow = 0
		} else if aimRow >= boxRows {
			aimRow = boxRows - 1
		}
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("PRISUAL") + yellow.Render(" Framing Editor") + "\n\n")
	b.WriteString(fmt.Sprintf("  %s %s   %s (%.2f, %.2f)\n",
		dimStyle.Render("mode:"),
		bold.Render(m.framingEditing),
		dimStyle.Render("target:"),
		tx, ty))
	if showAim {
		dx, dy := (m.lastAimX-tx)*2.0, (m.lastAimY-ty)*2.0
		b.WriteString(fmt.Sprintf("  %s (%.2f, %.2f)   %s (%+.3f, %+.3f)\n",
			dimStyle.Render("live aim:"),
			m.lastAimX, m.lastAimY,
			dimStyle.Render("err:"),
			dx, dy))
	} else {
		b.WriteString("  " + dimStyle.Render(
			"(no live aim for this mode — switch aim mode in tracker or Tab to a mode being sent)") + "\n")
	}
	b.WriteString("\n")

	// Top border
	b.WriteString("  ┌")
	for i := 0; i < boxCols; i++ {
		b.WriteRune('─')
	}
	b.WriteString("┐\n")

	// Body — each row is built char-by-char so we can plot markers
	for r := 0; r < boxRows; r++ {
		b.WriteString("  │")
		for c := 0; c < boxCols; c++ {
			switch {
			case c == targetCol && r == targetRow:
				b.WriteString(redBold.Render("+"))
			case c == aimCol && r == aimRow:
				b.WriteString(greenBold.Render("●"))
			case r == targetRow && c == targetCol-1:
				b.WriteString(redBold.Render("-"))
			case r == targetRow && c == targetCol+1:
				b.WriteString(redBold.Render("-"))
			case c == targetCol && r == targetRow-1:
				b.WriteString(redBold.Render("|"))
			case c == targetCol && r == targetRow+1:
				b.WriteString(redBold.Render("|"))
			case r == boxRows/2 && c == boxCols/2:
				b.WriteString(dimStyle.Render("·")) // geometric center for reference
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString("│\n")
	}

	// Bottom border
	b.WriteString("  └")
	for i := 0; i < boxCols; i++ {
		b.WriteRune('─')
	}
	b.WriteString("┘\n\n")

	b.WriteString("  " + dimStyle.Render(
		"↑/↓/←/→ nudge   Tab cycle mode   R reset mode   Esc exit") + "\n")
	b.WriteString("\n  ")
	for _, mode := range []string{"shoulders", "nose", "torso"} {
		tag := fmt.Sprintf(" %s (%.2f, %.2f) ", mode,
			m.framingTargetX[mode], m.framingTargetY[mode])
		if mode == m.framingEditing {
			b.WriteString(greenBold.Render(tag))
		} else {
			b.WriteString(dimStyle.Render(tag))
		}
		b.WriteString(" ")
	}
	b.WriteString("\n")

	return b.String()
}

func (m model) viewMapping() string {
	var content strings.Builder

	content.WriteString(titleStyle.Render("PRISUAL") + yellow.Render(" Controller Mapping") + "\n")
	content.WriteString("\n")

	// Progress
	content.WriteString(dimStyle.Render(fmt.Sprintf("  Step %d/%d", m.mapStep+1, mapStepCount)) + "\n")
	content.WriteString("\n")

	// Current step prompt
	stepName := mapStepNames[m.mapStep]
	if m.mapStep < mapBtnAFHold {
		content.WriteString(yellow.Bold(true).Render("  Move axis: ") + bold.Render(stepName) + "\n")
	} else {
		content.WriteString(yellow.Bold(true).Render("  Press button: ") + bold.Render(stepName) + "\n")
	}
	content.WriteString("\n")

	// Show live axis values
	content.WriteString(headerStyle.Render("  AXES") + "\n")
	for i := 0; i < 6; i++ {
		val := 0.0
		base := 0.0
		if m.joyReady {
			val = m.latestJoy.Axes[i]
			base = m.mapBaseAxes[i]
		}
		delta := math.Abs(val - base)

		barVal := int((val + 1.0) / 2.0 * float64(barWidth))
		if barVal < 0 {
			barVal = 0
		}
		if barVal > barWidth {
			barVal = barWidth
		}
		bar := make([]rune, barWidth)
		for j := range bar {
			if j < barVal {
				bar[j] = '█'
			} else {
				bar[j] = '░'
			}
		}

		label := fmt.Sprintf("  %s ", axisLabel(i))
		valStr := fmt.Sprintf(" %+6.3f", val)

		style := dimStyle
		if m.mapStep < mapBtnAFHold && i == m.mapDetected {
			style = green
			label = green.Bold(true).Render(label)
			valStr = green.Render(valStr)
		} else if m.mapStep < mapBtnAFHold && delta > 0.1 {
			style = cyan
			label = cyan.Render(label)
			valStr = cyan.Render(valStr)
		} else {
			label = dimStyle.Render(label)
			valStr = dimStyle.Render(valStr)
		}

		content.WriteString(label + style.Render(string(bar)) + valStr + "\n")
	}

	// Show live button states
	content.WriteString("\n")
	content.WriteString(headerStyle.Render("  BUTTONS") + "\n")
	content.WriteString("  ")
	for i := 0; i < 12; i++ {
		pressed := m.joyReady && m.latestJoy.Buttons[i]
		label := fmt.Sprintf("%2d", i)
		if m.mapStep >= mapBtnAFHold && i == m.mapDetected {
			if pressed {
				content.WriteString(green.Bold(true).Render("["+label+"]") + " ")
			} else {
				content.WriteString(green.Render("["+label+"]") + " ")
			}
		} else if pressed {
			content.WriteString(yellow.Render("["+label+"]") + " ")
		} else {
			content.WriteString(dimStyle.Render(" "+label+" ") + " ")
		}
	}
	content.WriteString("\n")

	// Detection status
	content.WriteString("\n")
	if m.mapDetected >= 0 {
		if m.mapStep < mapBtnAFHold {
			content.WriteString(green.Bold(true).Render(fmt.Sprintf("  Detected: Axis %s (%d)", axisLabel(m.mapDetected), m.mapDetected)))
		} else {
			content.WriteString(green.Bold(true).Render(fmt.Sprintf("  Detected: Button %d", m.mapDetected)))
		}
		content.WriteString(dimStyle.Render("  — Enter to confirm") + "\n")
	} else {
		if m.mapStep < mapBtnAFHold {
			content.WriteString(dimStyle.Render("  Waiting for axis movement...") + "\n")
		} else {
			content.WriteString(dimStyle.Render("  Waiting for button press...") + "\n")
		}
	}

	// Help
	content.WriteString("\n")
	content.WriteString(dimStyle.Render("  Enter confirm  Tab skip  Esc cancel"))

	return "\n" + panelStyle.Render(content.String()) + "\n"
}

// --- Main ---

func main() {
	flag.Parse()

	// --dump-config: print default config and exit
	if *flagDumpConfig {
		fmt.Println(config.DefaultConfig().DumpJSON())
		return
	}

	focusStep = *flagFocusStep
	focusHoldMs = *flagFocusHoldMs
	focusPulseOnMs = *flagFocusOnMs
	focusPulseOffMs = *flagFocusOffMs

	// Load controller config: explicit flag > controller.json > built-in default
	cfg := config.DefaultConfig()
	configPath := *flagConfig
	if configPath == "" {
		if _, err := os.Stat("controller.json"); err == nil {
			configPath = "controller.json"
		}
	}
	if configPath != "" {
		var err error
		cfg, err = config.Load(configPath)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Loaded controller config: %s (%s)", cfg.Name, configPath)
	}

	if *flagVmix == "" && *flagCamera == "" {
		fmt.Fprintln(os.Stderr, "Usage: shim --vmix <host> or shim --camera <ip>")
		flag.PrintDefaults()
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := &router.CameraRouter{
		Cameras:   make(map[int]*visca.Camera),
		CameraIPs: make(map[int]string),
	}

	// Shared inquiry-reply channel: every camera worker posts its inquiry
	// results here, the bubbletea Update loop drains it via listenVisca.
	// Buffered so a brief stall on the main goroutine doesn't block workers.
	viscaCh := make(chan visca.Reply, 64)

	vmixOK := false
	vmixNumInputs := 0

	if *flagVmix != "" {
		// Try vMix discovery
		cameras, numInputs, err := vmix.DiscoverCameras(*flagVmix)
		if err != nil {
			log.Printf("vMix discovery failed: %v", err)
			if *flagCamera == "" {
				log.Fatal("No cameras available. Use --camera for single-camera mode.")
			}
		} else {
			vmixOK = true
			vmixNumInputs = numInputs
			log.Printf("Discovered %d camera(s) from %d vMix inputs", len(cameras), numInputs)
			for _, cam := range cameras {
				log.Printf("  Input %d: %s (%s)", cam.InputNumber, cam.IP, cam.Title)
				// NewCamera dials in the worker goroutine; failures don't block
				// startup, the worker keeps trying to reconnect.
				r.Cameras[cam.InputNumber] = visca.NewCamera(cam.IP, *flagViscaPort, viscaCh)
				r.CameraIPs[cam.InputNumber] = cam.IP
			}
		}
	}

	// Discover all vMix inputs (for Stream Deck input switching)
	var vmixInputs []vmix.InputInfo
	if vmixOK {
		if inputs, err := vmix.DiscoverInputs(*flagVmix); err == nil {
			for _, inp := range inputs {
				// Skip audio-only inputs — can't preview/program them
				if strings.EqualFold(inp.Type, "Audio") {
					log.Printf("  Input %d: %s (audio, skipped)", inp.Number, inp.Title)
					continue
				}
				vmixInputs = append(vmixInputs, inp)
				log.Printf("  Input %d: %s [%s]", inp.Number, inp.Title, inp.Type)
			}
			log.Printf("Discovered %d switchable vMix inputs", len(vmixInputs))
		}
	}

	// Check initial streaming state
	var streamingNow bool
	var overlayInput int
	var overlayActive bool
	if vmixOK {
		if s, err := vmix.IsStreaming(*flagVmix); err == nil {
			streamingNow = s
			if s {
				log.Println("vMix is currently streaming")
			}
		}
		if ov, err := vmix.OverlayState(*flagVmix); err == nil && ov > 0 {
			overlayInput = ov
			overlayActive = true
			log.Printf("Overlay 1 active: input %d", ov)
		}
		// If no overlay active, find the "clear" input to use as default
		if overlayInput == 0 {
			for _, inp := range vmixInputs {
				if strings.Contains(strings.ToLower(inp.Title), "clear") ||
					strings.Contains(strings.ToLower(inp.Title), "lower") {
					overlayInput = inp.Number
					log.Printf("Overlay button will use input %d (%s)", inp.Number, inp.Title)
					break
				}
			}
		}
	}

	singleCam := false
	if len(r.Cameras) == 0 && *flagCamera != "" {
		// Single-camera fallback
		r.Cameras[1] = visca.NewCamera(*flagCamera, *flagViscaPort, viscaCh)
		r.CameraIPs[1] = *flagCamera
		r.Preview = 1 // always target this camera
		singleCam = true
		log.Printf("Single camera mode: %s", *flagCamera)
	}

	if len(r.Cameras) == 0 {
		log.Fatal("No cameras connected.")
	}

	// Start in manual focus. AF hold can temporarily switch a camera back to AF.
	for _, cam := range r.Cameras {
		visca.SetManualFocus(cam)
	}

	// Channels
	tallyCh := make(chan vmix.TallyUpdate, 1)
	joyCh := make(chan input.JoystickState, 1)

	// vMix command connection (separate from tally subscription)
	var vmixCmd *vmix.Commander
	if vmixOK {
		vmixCmd = vmix.NewCommander(*flagVmix, 8099)
		defer vmixCmd.Close()
	}

	// Start tally goroutine
	if vmixOK {
		go vmix.SubscribeTally(ctx, *flagVmix, 8099, tallyCh)
	}

	// Start joystick goroutine
	go input.PollJoystick(ctx, joyCh)

	// Stream Deck channels (nil if not enabled)
	var deckCh chan deck.DeckEvent
	var deckFeedbackCh chan deck.FeedbackState
	if *flagStreamDeck {
		deckCh = make(chan deck.DeckEvent, 4)
		deckFeedbackCh = make(chan deck.FeedbackState, 4)
		go deck.PollStreamDeck(ctx, deckCh, deckFeedbackCh)
	}

	// Proclaim client
	var proclaimCmd *proclaim.Client
	if *flagProclaim != "" {
		proclaimCmd = proclaim.NewClient(*flagProclaim, *flagProclaimPass)
		log.Printf("Proclaim control: %s", *flagProclaim)
	}

	// Auto-tracker UDP listener (optional)
	var trackerCh chan tracker.Event
	if *flagTrackerPort > 0 {
		trackerCh = make(chan tracker.Event, 32)
		go tracker.Listen(ctx, *flagTrackerPort, trackerCh)
		log.Printf("Auto-track listener on UDP 127.0.0.1:%d", *flagTrackerPort)
	}
	trackerStale := time.Duration(*flagTrackerStale) * time.Millisecond

	// Run bubbletea TUI
	m := initialModel(ctx, cancel, cfg, configPath, r, tallyCh, joyCh, viscaCh, deckCh, deckFeedbackCh, trackerCh, *flagTrackerKp, *flagTrackerKd, *flagTrackerDead, trackerStale, *flagVmix, vmixOK, vmixCmd, vmixNumInputs, vmixInputs, streamingNow, overlayInput, overlayActive, proclaimCmd, singleCam)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}

	// Cleanup
	cancel()
	for _, cam := range r.Cameras {
		cam.Close()
	}
}

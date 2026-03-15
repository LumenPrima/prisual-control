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

	"prisual_control/internal/input"
	"prisual_control/internal/router"
	"prisual_control/internal/visca"
	"prisual_control/internal/vmix"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// CLI flags
var (
	flagVmix      = flag.String("vmix", "", "vMix host IP (enables auto-discovery and tally)")
	flagCamera    = flag.String("camera", "", "single camera IP (fallback if no vMix)")
	flagViscaPort = flag.Int("visca-port", 5678, "VISCA TCP port")
	flagExpo      = flag.Float64("expo", 2.5, "expo curve for spring-return axes")
	flagFocusHz    = flag.Int("focus-hz", 10, "focus command send rate (Hz)")
	flagInvertTilt = flag.Bool("invert-tilt", true, "invert tilt axis (stick forward = tilt down)")
	flagFocusRange    = flag.Int("focus-range", 300, "focus half-range: full slider sweep = ±N positions from anchor")
	flagFadeDuration  = flag.Int("fade-ms", 1000, "fade transition duration in milliseconds")
)

// Constants matching the Python POC
const (
	focusMin = 0x0080
	focusMax = 0x1180
	zoomMax  = 0x4000

	// Display ranges for pan/tilt position bars (may need tuning per camera)
	panPosRange  = 2500 // ±panPosRange for centered bar
	tiltPosRange = 1200

	stickDeadzone = 0.10
	twistDeadzone = 0.15

	maxPanSpeed  = 0x18 // 24
	maxTiltSpeed = 0x14 // 20
	maxZoomSpeed = 7

	presetSlotOffset = 200
	presetCount      = 6

	// Button indices (Logitech Extreme 3D Pro)
	btnTrigger    = 0  // hold for AF, thumb+trigger = latch AF
	btnThumb      = 1  // preset save modifier
	btnOverride   = 2  // hold to control program camera
	btnTransition = 3  // execute active transition (cut/fade)
	btnCut        = 4  // instant cut to preview
	btnCyclePreview = 5 // cycle preview to next input
	btnPresetBase = 6  // buttons 7-12 → camera presets 1-6

	// vMix transition
	defaultFadeDuration = 1000 // ms

	focusJitterThreshold = 1
)

// --- Bubbletea messages ---

type tallyMsg vmix.TallyUpdate
type joystickMsg input.JoystickState
type tickMsg time.Time

// --- Bubbletea model ---

type model struct {
	ctx       context.Context
	cancel    context.CancelFunc
	router    *router.CameraRouter
	tallyCh   chan vmix.TallyUpdate
	joyCh     chan input.JoystickState // bidirectional for non-blocking send pattern
	vmixHost  string
	vmixOK    bool
	vmixCmd       *vmix.Commander // nil if no vMix
	vmixNumInputs int            // total vMix inputs (for preview cycling)
	singleCam     bool           // true when running without vMix

	// Joystick state
	joyConnected bool
	joyName      string
	latestJoy    input.JoystickState // most recent joystick snapshot
	joyReady     bool                // true once we've received at least one state
	lastPan      int
	lastTilt     int
	lastZoom     int
	lastFocusPos int
	manualFocus  bool
	afLatched    bool // true when AF is latched (thumb+trigger)
	cmdCount     int

	// Anchor-relative focus: throttle displacement from anchor = focus offset
	focusCenter   int       // focus position when anchor was set
	focusAnchor   float64   // raw throttle value when anchor was set
	focusAnchored bool      // true once we have a valid anchor
	anchorDelay   time.Time // if non-zero, re-anchor at this time (after preset settling)

	// Focus rate limiting
	lastFocusTime time.Time
	focusInterval time.Duration

	// Camera positions (queried periodically when idle)
	camPanPos     int16
	camTiltPos    int16
	camZoomPos    uint16
	posQueried    bool // true once we've done at least one position query
	lastQueryTime time.Time
	queryPhase    int // 0 = pan/tilt, 1 = zoom

	// Button edge detection
	prevButtons [12]bool

	// Display
	logLines []string
}

func (m *model) anchorFocus(centerPos int, throttle float64) {
	m.focusCenter = centerPos
	m.focusAnchor = throttle
	m.focusAnchored = true
	m.lastFocusPos = centerPos
	m.addLog(fmt.Sprintf("Anchored @ 0x%04X", centerPos))
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
	r *router.CameraRouter,
	tallyCh chan vmix.TallyUpdate,
	joyCh chan input.JoystickState,
	vmixHost string,
	vmixOK bool,
	vmixCmd *vmix.Commander,
	vmixNumInputs int,
	singleCam bool,
) model {
	return model{
		ctx:           ctx,
		cancel:        cancel,
		router:        r,
		tallyCh:       tallyCh,
		joyCh:         joyCh,
		vmixHost:      vmixHost,
		vmixOK:        vmixOK,
		vmixCmd:       vmixCmd,
		vmixNumInputs: vmixNumInputs,
		singleCam:     singleCam,
		manualFocus:   true,
		lastFocusPos:  -1,
		focusInterval: time.Second / time.Duration(*flagFocusHz),
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

func tickCmd() tea.Cmd {
	return tea.Tick(10*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		listenJoystick(m.joyCh),
		tickCmd(),
	}
	if m.vmixOK {
		cmds = append(cmds, listenTally(m.tallyCh))
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			// Stop all motion on all cameras
			for _, cam := range m.router.Cameras {
				visca.StopAllMotion(cam)
			}
			m.cancel()
			return m, tea.Quit
		}

	case tallyMsg:
		update := vmix.TallyUpdate(msg)
		oldTarget := m.router.TargetInput()
		if m.router.UpdateTally(update.Program, update.Preview) {
			m.router.HandleSwitch(oldTarget)
			m.lastPan = 0
			m.lastTilt = 0
			m.lastZoom = 0
			m.addLog(fmt.Sprintf("Tally: pgm=%d pvw=%d", update.Program, update.Preview))
		}
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

	case tickMsg:
		// Process joystick → VISCA commands on a fixed 10ms tick,
		// matching the Python POC's main loop cadence.
		if m.joyReady && m.joyConnected {
			m.processJoystick()
		}
		return m, tickCmd()
	}

	return m, nil
}

func (m *model) processJoystick() {
	state := m.latestJoy
	cam := m.router.ActiveCamera()
	expo := *flagExpo

	// --- Button edge detection ---
	buttons := state.Buttons

	// --- Focus mode: trigger=hold-for-AF, thumb+trigger=latch AF ---
	throttle := state.Axes[3]

	// Trigger pressed
	if buttons[btnTrigger] && !m.prevButtons[btnTrigger] && cam != nil {
		if m.afLatched {
			// Unlatching — back to MF
			pos, err := visca.FocusInquiry(cam)
			visca.SetManualFocus(cam)
			m.manualFocus = true
			m.afLatched = false
			if err == nil {
				m.anchorFocus(int(pos), throttle)
			}
			m.addLog("AF unlatched")
		} else {
			// Enter AF
			visca.SetAutoFocus(cam)
			m.manualFocus = false
			m.addLog("AF (held)")
		}
	}

	// Trigger released (normal hold-for-AF)
	if !buttons[btnTrigger] && m.prevButtons[btnTrigger] && cam != nil && !m.afLatched {
		if !m.manualFocus {
			pos, err := visca.FocusInquiry(cam)
			visca.SetManualFocus(cam)
			m.manualFocus = true
			if err == nil {
				m.anchorFocus(int(pos), throttle)
			}
		}
	}

	// Thumb while in AF = latch (AF stays when trigger released)
	if !m.manualFocus && !m.afLatched && buttons[btnThumb] && !m.prevButtons[btnThumb] {
		m.afLatched = true
		m.addLog("AF latched")
	}

	// Override button
	if buttons[btnOverride] != m.prevButtons[btnOverride] {
		oldTarget := m.router.TargetInput()
		if m.router.SetOverride(buttons[btnOverride]) {
			m.router.HandleSwitch(oldTarget)
			m.lastPan = 0
			m.lastTilt = 0
			m.lastZoom = 0
		}
	}

	// Preset buttons
	thumbHeld := buttons[btnThumb]
	for i := 0; i < presetCount; i++ {
		btn := btnPresetBase + i
		if btn < len(buttons) && buttons[btn] && !m.prevButtons[btn] {
			if cam != nil {
				slot := byte(presetSlotOffset + i)
				if thumbHeld {
					visca.PresetSave(cam, slot)
					m.addLog(fmt.Sprintf("Saved preset %d (slot %d)", i+1, presetSlotOffset+i))
				} else {
					visca.PresetRecall(cam, slot)
					m.focusAnchored = false
					m.anchorDelay = time.Now().Add(2 * time.Second)
					m.addLog(fmt.Sprintf("Recall preset %d (slot %d)", i+1, presetSlotOffset+i))
				}
				m.cmdCount++
			}
		}
	}

	// --- vMix buttons ---
	if m.vmixCmd != nil {
		// Transition (fade to preview)
		if buttons[btnTransition] && !m.prevButtons[btnTransition] {
			if err := m.vmixCmd.Fade(*flagFadeDuration); err != nil {
				m.addLog(fmt.Sprintf("Fade error: %v", err))
			} else {
				m.addLog(fmt.Sprintf("Fade %dms", *flagFadeDuration))
			}
		}
		// Cut to preview
		if buttons[btnCut] && !m.prevButtons[btnCut] {
			if err := m.vmixCmd.Cut(); err != nil {
				m.addLog(fmt.Sprintf("Cut error: %v", err))
			} else {
				m.addLog("Cut")
			}
		}
		// Cycle preview to next input
		if buttons[btnCyclePreview] && !m.prevButtons[btnCyclePreview] {
			numInputs := m.vmixNumInputs
			if numInputs > 0 {
				if err := m.vmixCmd.NextPreview(numInputs, m.router.Preview); err != nil {
					m.addLog(fmt.Sprintf("Preview cycle error: %v", err))
				} else {
					m.addLog("Preview next")
				}
			}
		}
	}

	m.prevButtons = buttons

	if cam == nil {
		return
	}

	// --- Pan/Tilt ---
	panSpeed := input.AxisToSpeed(state.Axes[0], stickDeadzone, maxPanSpeed, expo)
	tiltAxis := state.Axes[1]
	if *flagInvertTilt {
		tiltAxis = -tiltAxis
	}
	tiltSpeed := input.AxisToSpeed(tiltAxis, stickDeadzone, maxTiltSpeed, expo)
	if panSpeed != m.lastPan || tiltSpeed != m.lastTilt {
		visca.PanTiltVariable(cam, panSpeed, tiltSpeed)
		m.lastPan = panSpeed
		m.lastTilt = tiltSpeed
		m.cmdCount++
	}

	// --- Zoom ---
	zoomSpeed := input.AxisToSpeed(state.Axes[2], twistDeadzone, maxZoomSpeed, expo)
	if zoomSpeed != m.lastZoom {
		visca.ZoomVariable(cam, zoomSpeed)
		m.lastZoom = zoomSpeed
		m.cmdCount++
	}

	// --- Anchor-relative focus ---
	now := time.Now()

	// Initial anchor: first tick with a camera, query focus and anchor
	if !m.focusAnchored && m.anchorDelay.IsZero() && cam != nil {
		if pos, err := visca.FocusInquiry(cam); err == nil {
			m.anchorFocus(int(pos), throttle)
		}
	}

	// Re-anchor after preset settling delay
	if !m.focusAnchored && !m.anchorDelay.IsZero() && now.After(m.anchorDelay) && cam != nil {
		if pos, err := visca.FocusInquiry(cam); err == nil {
			m.anchorFocus(int(pos), throttle)
		}
		m.anchorDelay = time.Time{}
	}

	// Latched AF: slider movement unlatches
	if m.afLatched && m.focusAnchored && cam != nil {
		offset := throttle - m.focusAnchor
		if math.Abs(offset) > 0.02 { // ~2-3 axis steps
			pos, err := visca.FocusInquiry(cam)
			visca.SetManualFocus(cam)
			m.manualFocus = true
			m.afLatched = false
			if err == nil {
				m.anchorFocus(int(pos), throttle)
			}
			m.addLog("AF unlatched (slider)")
		}
	}

	// Send focus commands
	if m.focusAnchored && m.manualFocus && now.Sub(m.lastFocusTime) >= m.focusInterval {
		offset := throttle - m.focusAnchor
		focusPos := m.focusCenter + int(offset*float64(*flagFocusRange))
		if focusPos < focusMin {
			focusPos = focusMin
		}
		if focusPos > focusMax {
			focusPos = focusMax
		}

		delta := abs(focusPos - m.lastFocusPos)
		if delta > focusJitterThreshold {
			visca.FocusDirect(cam, uint16(focusPos))
			m.lastFocusPos = focusPos
			m.lastFocusTime = now
			m.cmdCount++
		}
	}

	// --- Position queries (only when camera is idle) ---
	idle := m.lastPan == 0 && m.lastTilt == 0 && m.lastZoom == 0
	queryInterval := time.Second
	if !m.posQueried {
		queryInterval = 0 // query immediately on first tick
	}
	if idle && now.Sub(m.lastQueryTime) >= queryInterval {
		m.lastQueryTime = now
		switch m.queryPhase {
		case 0:
			if pan, tilt, err := visca.PanTiltInquiry(cam); err == nil {
				m.camPanPos = pan
				m.camTiltPos = tilt
				m.posQueried = true
			}
		case 1:
			if zoom, err := visca.ZoomInquiry(cam); err == nil {
				m.camZoomPos = zoom
			}
		}
		m.queryPhase = (m.queryPhase + 1) % 2
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
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

func (m model) View() string {
	var content strings.Builder

	// --- Header ---
	content.WriteString(titleStyle.Render("PRISUAL") + dimStyle.Render(" Camera Shim") + "\n")

	// --- Camera list ---
	content.WriteString("\n")
	content.WriteString(headerStyle.Render("CAMERAS") + "\n")
	target := m.router.TargetInput()
	for _, num := range m.router.InputNumbers() {
		ip := m.router.CameraIPs[num]
		tally := m.router.TallyState(num)

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

		ctrl = ""
		if num == target {
			if tally == "PROGRAM" && m.router.Override {
				ctrl = redBold.Render(" ◀ LIVE")
			} else {
				ctrl = cyan.Render(" ◀")
			}
		}

		content.WriteString(fmt.Sprintf(" %s %s %-14s %s%s\n",
			dot,
			dimStyle.Render(fmt.Sprintf("%d", num)),
			accentDim.Render(ip),
			tallyTag,
			ctrl))
	}

	// --- Axes ---
	content.WriteString("\n")
	if m.joyConnected {
		content.WriteString(headerStyle.Render("AXES") + dimStyle.Render("  "+m.joyName) + "\n")

		// Pan
		panBar := centeredBar(int(m.camPanPos), -panPosRange, panPosRange, barWidth)
		panVal := fmt.Sprintf("%+5d", m.camPanPos)
		panExtra := ""
		if m.lastPan != 0 {
			panExtra = yellow.Render(fmt.Sprintf(" %+3d", m.lastPan))
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
		if m.lastTilt != 0 {
			tiltExtra = yellow.Render(fmt.Sprintf(" %+3d", m.lastTilt))
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
		} else if !m.focusAnchored {
			settleBar := strings.Repeat("·", barWidth)
			content.WriteString(fmt.Sprintf(" %s %s %s\n",
				labelStyle.Render(""),
				dimStyle.Render(settleBar),
				yellow.Render("settling...")))
		} else {
			fr := *flagFocusRange
			windowMin := m.focusCenter - fr
			windowMax := m.focusCenter + fr
			if windowMin < focusMin {
				windowMin = focusMin
			}
			if windowMax > focusMax {
				windowMax = focusMax
			}
			displayPos := m.lastFocusPos
			if displayPos < 0 {
				displayPos = m.focusCenter
			}
			fBar := centeredBar(displayPos, windowMin, windowMax, barWidth)
			content.WriteString(fmt.Sprintf(" %s %s %s %s\n",
				labelStyle.Render(""),
				green.Render(fBar),
				valueStyle.Render(fmt.Sprintf("0x%04X", displayPos)),
				dimStyle.Render(fmt.Sprintf("±%d", fr))))
		}
	} else {
		content.WriteString(headerStyle.Render("JOYSTICK") + "\n")
		content.WriteString(dimStyle.Render(" waiting for device...") + "\n")
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
	helpParts := " q quit  trigger AF  thumb+trigger latch  base 7-12 presets"
	if m.vmixCmd != nil {
		helpParts += "\n btn4 fade  btn5 cut  btn6 next preview"
	}
	content.WriteString(dimStyle.Render(helpParts))

	// Wrap in panel
	return "\n" + panelStyle.Render(content.String()) + "\n"
}

// --- Main ---

func main() {
	flag.Parse()

	if *flagVmix == "" && *flagCamera == "" {
		fmt.Fprintln(os.Stderr, "Usage: shim --vmix <host> or shim --camera <ip>")
		flag.PrintDefaults()
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := &router.CameraRouter{
		Cameras:   make(map[int]*visca.Connection),
		CameraIPs: make(map[int]string),
	}

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
				conn, err := visca.Dial(cam.IP, *flagViscaPort)
				if err != nil {
					log.Printf("  WARNING: failed to connect to %s: %v", cam.IP, err)
					continue
				}
				r.Cameras[cam.InputNumber] = conn
				r.CameraIPs[cam.InputNumber] = cam.IP
			}
		}
	}

	singleCam := false
	if len(r.Cameras) == 0 && *flagCamera != "" {
		// Single-camera fallback
		conn, err := visca.Dial(*flagCamera, *flagViscaPort)
		if err != nil {
			log.Fatalf("Failed to connect to camera %s: %v", *flagCamera, err)
		}
		r.Cameras[1] = conn
		r.CameraIPs[1] = *flagCamera
		r.Preview = 1 // always target this camera
		singleCam = true
		log.Printf("Single camera mode: %s", *flagCamera)
	}

	if len(r.Cameras) == 0 {
		log.Fatal("No cameras connected.")
	}

	// Set all cameras to manual focus
	for num, cam := range r.Cameras {
		mode, err := visca.FocusModeInquiry(cam)
		if err != nil {
			log.Printf("Camera %d focus mode inquiry failed: %v", num, err)
		} else {
			log.Printf("Camera %d focus mode: %s", num, mode)
		}
		if mode != "manual" {
			visca.SetManualFocus(cam)
			time.Sleep(100 * time.Millisecond)
		}
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

	// Run bubbletea TUI
	m := initialModel(ctx, cancel, r, tallyCh, joyCh, *flagVmix, vmixOK, vmixCmd, vmixNumInputs, singleCam)
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

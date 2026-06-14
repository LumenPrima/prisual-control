// viscacal: motor-characterization probe for a single Prisual VISCA camera.
//
// Takes cues from Frigate's autotracking calibration (sweep the control input,
// measure the response, fit a simple model by least squares, persist it). Frigate
// drives discrete absolute moves and models move_time = intercept + slope*magnitude
// using ONVIF MoveStatus to time each move. We drive continuous variable-speed
// jogs instead, so we model the inverse — angular velocity vs VISCA speed — and
// read pan/tilt/zoom position over time (our MoveStatus equivalent) to measure it.
//
// For each speed it jogs the axis, samples position every --sample ms, and fits
// position-vs-time by OLS to get steps/sec (dropping the first sample to exclude
// the accel ramp). It then fits velocity-vs-speed across all speeds to get a
// linear motor model. Results print as a table and write to a JSON file the shim
// can later load.
//
// WARNING: this physically moves the camera (pan, tilt, zoom). Do not run during
// a live program. Ctrl-C stops all motion. It recenters between trials and aborts
// a trial if the position nears a hardware limit.
//
//	go build -o viscacal ./cmd/viscacal
//	./viscacal --ip 192.168.1.50 --out calibration-192.168.1.50.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"prisual_control/internal/visca"
)

// Encoder position ranges (from docs/HTTPCGIList.pdf) and speed caps.
const (
	panMin, panMax   = -2448, 2448 // 0xF670 .. 0x0990
	tiltMin, tiltMax = -431, 1296  // 0xFE51 .. 0x0510
	zoomMin, zoomMax = 0, 0x4000

	maxPanSpeed  = 24
	maxTiltSpeed = 20
	maxZoomSpeed = 7

	// cmdMoveDelay must exceed the camera's command->first-motion latency
	// (measured ~100-120ms) so a settle check doesn't fire before the camera
	// has even started moving and falsely conclude it's already at rest/target.
	cmdMoveDelay = 250 * time.Millisecond
)

type speedVel struct {
	Speed       int     `json:"speed"`
	StepsPerSec float64 `json:"steps_per_sec"`
	Samples     int     `json:"n_samples"`
	Aborted     bool    `json:"aborted,omitempty"`
}

type linModel struct {
	Intercept float64 `json:"intercept"`       // a in velocity = a + b*speed
	Slope     float64 `json:"slope_per_speed"` // b
	R2        float64 `json:"r2"`
}

type axisResult struct {
	Axis    string     `json:"axis"`
	Samples []speedVel `json:"samples"`
	Model   linModel   `json:"model"`
}

type latencyResult struct {
	Reps     []float64 `json:"reps_ms"`
	MinMs    float64   `json:"min_ms"`
	MedianMs float64   `json:"median_ms"`
	Note     string    `json:"note"`
}

type calibration struct {
	Camera     string            `json:"camera"`
	MeasuredAt string            `json:"measured_at"`
	DwellMs    int               `json:"dwell_ms"`
	SampleMs   int               `json:"sample_ms"`
	Ranges     map[string][2]int `json:"ranges"`
	Pan        axisResult        `json:"pan"`
	Tilt       axisResult        `json:"tilt"`
	ZoomTele   axisResult        `json:"zoom_tele"`
	ZoomWide   axisResult        `json:"zoom_wide"`
	Latency    latencyResult     `json:"latency"`
}

var conn *visca.Connection

func main() {
	ip := flag.String("ip", "192.168.1.50", "camera IP")
	port := flag.Int("port", 5678, "VISCA TCP port")
	out := flag.String("out", "", "JSON output path (default: calibration-<ip>.json)")
	dwellMs := flag.Int("dwell", 1000, "jog duration per speed trial (ms)")
	settleMs := flag.Int("settle", 500, "settle wait after a move before sampling (ms)")
	sampleMs := flag.Int("sample", 80, "position sample interval during a trial (ms)")
	speedStep := flag.Int("speed-step", 1, "sweep every Nth speed (1 = all)")
	doPan := flag.Bool("pan", true, "run pan sweep")
	doTilt := flag.Bool("tilt", true, "run tilt sweep")
	doZoom := flag.Bool("zoom", true, "run zoom sweeps")
	doLat := flag.Bool("latency", true, "run latency measurement")
	flag.Parse()

	if *out == "" {
		*out = fmt.Sprintf("calibration-%s.json", *ip)
	}
	dwell := time.Duration(*dwellMs) * time.Millisecond
	settle := time.Duration(*settleMs) * time.Millisecond
	sample := time.Duration(*sampleMs) * time.Millisecond

	fmt.Printf("Dialing %s:%d ...\n", *ip, *port)
	c, err := visca.Dial(*ip, *port)
	if err != nil {
		fmt.Println("DIAL FAILED:", err)
		os.Exit(1)
	}
	conn = c
	defer conn.Close()
	fmt.Println("Connected.")
	fmt.Println("!! This moves the camera. Ctrl-C stops all motion and exits. !!")

	// Stop motion on Ctrl-C so an aborted run never leaves the camera driving.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println("\nInterrupted — stopping motion.")
		_ = visca.StopAllMotion(conn)
		conn.Close()
		os.Exit(1)
	}()

	cal := calibration{
		Camera:     *ip,
		MeasuredAt: time.Now().Format(time.RFC3339),
		DwellMs:    *dwellMs,
		SampleMs:   *sampleMs,
		Ranges: map[string][2]int{
			"pan":  {panMin, panMax},
			"tilt": {tiltMin, tiltMax},
			"zoom": {zoomMin, zoomMax},
		},
	}

	// Recenter before we start so trials begin from a known position.
	recenterPanTilt(settle)

	if *doPan {
		fmt.Println("\n=== Pan sweep ===")
		cal.Pan = sweepPanTilt("pan", true, +1, maxPanSpeed, *speedStep, dwell, settle, sample)
	}
	if *doTilt {
		fmt.Println("\n=== Tilt sweep ===")
		// Probe which jog sign increases the tilt position so we always drive
		// toward the roomy (+1296) end rather than the shallow (-431) end.
		tiltSign := detectTiltSign(settle)
		fmt.Printf("tilt up sign: %+d\n", tiltSign)
		cal.Tilt = sweepPanTilt("tilt", false, tiltSign, maxTiltSpeed, *speedStep, dwell, settle, sample)
	}
	if *doZoom {
		fmt.Println("\n=== Zoom tele sweep ===")
		cal.ZoomTele = sweepZoom("zoom_tele", +1, *speedStep, dwell, settle, sample)
		fmt.Println("\n=== Zoom wide sweep ===")
		cal.ZoomWide = sweepZoom("zoom_wide", -1, *speedStep, dwell, settle, sample)
	}
	if *doLat {
		fmt.Println("\n=== Latency (command -> first motion) ===")
		cal.Latency = measureLatency(settle, sample)
	}

	// Park the camera back at center and stop everything.
	recenterPanTilt(settle)
	_ = visca.StopAllMotion(conn)

	data, _ := json.MarshalIndent(cal, "", "  ")
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Println("write output failed:", err)
	} else {
		fmt.Printf("\nWrote %s\n", *out)
	}
}

// sweepPanTilt sweeps one pan/tilt axis across speeds. usePan selects which axis
// of the inquiry to read; dirSign is the jog polarity that drives toward the
// roomy end of that axis.
func sweepPanTilt(name string, usePan bool, dirSign, maxSpeed, step int,
	dwell, settle, sample time.Duration) axisResult {

	lo, hi := safeWindow(usePan)
	res := axisResult{Axis: name}
	for s := step; s <= maxSpeed; s += step {
		recenterPanTilt(settle)
		jog := func() error {
			if usePan {
				return visca.PanTiltVariable(conn, dirSign*s, 0)
			}
			return visca.PanTiltVariable(conn, 0, dirSign*s)
		}
		read := func() (float64, error) {
			pan, tilt, err := visca.PanTiltInquiry(conn)
			if err != nil {
				return 0, err
			}
			if usePan {
				return float64(pan), nil
			}
			return float64(tilt), nil
		}
		vel, n, aborted := measureVelocity(jog, stopPanTilt, read, lo, hi, dwell, sample)
		res.Samples = append(res.Samples, speedVel{Speed: s, StepsPerSec: vel, Samples: n, Aborted: aborted})
		fmt.Printf("  speed %2d: %8.1f steps/s  (n=%d%s)\n", s, vel, n, abortTag(aborted))
	}
	res.Model = fitModel(res.Samples)
	printModel(res.Model)
	return res
}

// sweepZoom sweeps the zoom axis. dir +1 = tele (toward zoomMax), -1 = wide.
func sweepZoom(name string, dir, step int, dwell, settle, sample time.Duration) axisResult {
	// Start at the far end from the travel direction and guard only the end we
	// drive toward — otherwise the start position trips the abort window on the
	// first sample.
	var lo, hi float64
	var startPos uint16
	if dir > 0 { // tele: start wide, abort near max
		lo, hi = float64(zoomMin), float64(zoomMax-200)
		startPos = uint16(zoomMin)
	} else { // wide: start tele, abort near min
		lo, hi = float64(zoomMin+200), float64(zoomMax)
		startPos = uint16(zoomMax)
	}
	res := axisResult{Axis: name}
	for s := step; s <= maxZoomSpeed; s += step {
		recenterZoom(startPos, settle)
		jog := func() error { return visca.ZoomVariable(conn, dir*s) }
		read := func() (float64, error) {
			z, err := visca.ZoomInquiry(conn)
			return float64(z), err
		}
		vel, n, aborted := measureVelocity(jog, stopZoom, read, lo, hi, dwell, sample)
		res.Samples = append(res.Samples, speedVel{Speed: s, StepsPerSec: vel, Samples: n, Aborted: aborted})
		fmt.Printf("  speed %2d: %8.1f counts/s  (n=%d%s)\n", s, vel, n, abortTag(aborted))
	}
	res.Model = fitModel(res.Samples)
	printModel(res.Model)
	return res
}

// measureVelocity jogs, samples position over dwell, and OLS-fits position-vs-time
// to get steps/sec. It aborts early (and reports it) if position leaves [lo,hi].
// The first post-jog sample is dropped to exclude the acceleration ramp.
func measureVelocity(jog, stop func() error, read func() (float64, error),
	lo, hi float64, dwell, sample time.Duration) (vel float64, n int, aborted bool) {

	var ts, ps []float64
	t0 := time.Now()
	for time.Since(t0) < dwell {
		// Re-assert the jog every tick, exactly as the real control loop does.
		// A single VISCA pan/tilt drive can be dropped if it lands right after
		// an absolute-position move (recenter); re-asserting makes the
		// measurement immune to that instead of reading a phantom zero.
		if err := jog(); err != nil {
			fmt.Println("  jog send err:", err)
		}
		time.Sleep(sample)
		pos, err := read()
		if err != nil {
			continue // transient inquiry miss; next sample covers it
		}
		ts = append(ts, time.Since(t0).Seconds())
		ps = append(ps, pos)
		if pos < lo || pos > hi {
			aborted = true
			break
		}
	}
	_ = stop()
	// Trim the leading command-latency dead time and accel ramp: drop samples
	// until the position has clearly moved off its starting value. If it never
	// moves (a genuine sub-threshold speed), this reports ~0 steps/s correctly.
	if len(ps) > 0 {
		start := 0
		for start < len(ps) && math.Abs(ps[start]-ps[0]) < 5 {
			start++
		}
		ts, ps = ts[start:], ps[start:]
	}
	if len(ts) < 2 {
		return 0, len(ts), aborted
	}
	slope, _, _ := linreg(ts, ps)
	return math.Abs(slope), len(ts), aborted
}

// measureLatency times command-send to first detected position change. Resolution
// is bounded by the inquiry round-trip, so this is an upper bound on true latency.
func measureLatency(settle, sample time.Duration) latencyResult {
	const reps = 8
	res := latencyResult{Note: "upper bound; granularity = inquiry round-trip"}
	for i := 0; i < reps; i++ {
		recenterPanTilt(settle)
		p0, _, err := visca.PanTiltInquiry(conn)
		if err != nil {
			continue
		}
		dir := 1
		if i%2 == 1 {
			dir = -1 // alternate so the camera stays near center across reps
		}
		t0 := time.Now()
		_ = visca.PanTiltVariable(conn, dir*(maxPanSpeed/2), 0)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			pan, _, err := visca.PanTiltInquiry(conn)
			if err == nil && math.Abs(float64(pan-p0)) > 3 {
				res.Reps = append(res.Reps, float64(time.Since(t0).Microseconds())/1000.0)
				break
			}
			// The first jog can be dropped right after the recenter move; if
			// nothing has moved after 300ms, re-assert and restart the clock so
			// we time the jog that actually took effect.
			if time.Since(t0) > 300*time.Millisecond {
				t0 = time.Now()
				_ = visca.PanTiltVariable(conn, dir*(maxPanSpeed/2), 0)
			}
		}
		_ = stopPanTilt()
	}
	if len(res.Reps) > 0 {
		res.MinMs = minF(res.Reps)
		res.MedianMs = medianF(res.Reps)
		fmt.Printf("  latency min=%.1fms median=%.1fms (n=%d)\n", res.MinMs, res.MedianMs, len(res.Reps))
	}
	return res
}

// detectTiltSign returns the PanTiltVariable tilt sign that increases the tilt
// position (toward the roomy +1296 end). Probes at speed 1 from center.
func detectTiltSign(settle time.Duration) int {
	recenterPanTilt(settle)
	_, t0, err := visca.PanTiltInquiry(conn)
	if err != nil {
		return -1 // our convention: tilt<0 = up; reasonable default
	}
	_ = visca.PanTiltVariable(conn, 0, +1)
	time.Sleep(400 * time.Millisecond)
	_ = stopPanTilt()
	time.Sleep(200 * time.Millisecond)
	_, t1, err := visca.PanTiltInquiry(conn)
	if err != nil {
		return -1
	}
	if t1 > t0 {
		return +1 // positive jog value increased position
	}
	return -1
}

// safeWindow returns the [lo,hi] position bounds a trial must stay within.
func safeWindow(usePan bool) (lo, hi float64) {
	if usePan {
		return panMin + 150, panMax - 150
	}
	return tiltMin + 100, tiltMax - 150
}

func recenterPanTilt(settle time.Duration) {
	_ = visca.PanTiltAbsolute(conn, 0, 0, maxPanSpeed, maxTiltSpeed)
	waitSettled(func() (float64, error) {
		pan, tilt, err := visca.PanTiltInquiry(conn)
		// Track the larger of the two axes so we wait for both to settle.
		return math.Max(math.Abs(float64(pan)), math.Abs(float64(tilt))), err
	}, settle*6)
}

func recenterZoom(target uint16, settle time.Duration) {
	_ = visca.ZoomDirect(conn, target)
	// Zoom traverse is slow (seconds at the extremes), so allow a longer cap.
	waitSettled(func() (float64, error) {
		z, err := visca.ZoomInquiry(conn)
		return float64(z), err
	}, 12*time.Second)
}

// waitSettled waits until motion has stopped: the read value holds within 2 units
// for ~250ms. It must wait for a true stop (not just "near target") — firing the
// next jog while a recenter move is still in flight gets the jog dropped, which
// previously caused alternating zero/nonzero trials. The initial cmdMoveDelay
// covers command latency so we don't declare "settled" before the move begins.
func waitSettled(read func() (float64, error), maxWait time.Duration) {
	time.Sleep(cmdMoveDelay)
	deadline := time.Now().Add(maxWait)
	var last float64
	have := false
	stableSince := time.Now()
	for time.Now().Before(deadline) {
		v, err := read()
		if err != nil {
			continue
		}
		if have && math.Abs(v-last) <= 2 {
			if time.Since(stableSince) > 250*time.Millisecond {
				return
			}
		} else {
			stableSince = time.Now()
		}
		last = v
		have = true
		time.Sleep(20 * time.Millisecond)
	}
}

func stopPanTilt() error { return visca.PanTiltVariable(conn, 0, 0) }
func stopZoom() error    { return visca.ZoomVariable(conn, 0) }

// linreg returns slope, intercept and R^2 of a least-squares line fit.
func linreg(xs, ys []float64) (slope, intercept, r2 float64) {
	n := float64(len(xs))
	if n < 2 {
		return 0, 0, 0
	}
	var sx, sy, sxx, sxy, syy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		sxy += xs[i] * ys[i]
		syy += ys[i] * ys[i]
	}
	denom := n*sxx - sx*sx
	if denom == 0 {
		return 0, sy / n, 0
	}
	slope = (n*sxy - sx*sy) / denom
	intercept = (sy - slope*sx) / n
	ssTot := syy - sy*sy/n
	ssRes := 0.0
	for i := range xs {
		pred := slope*xs[i] + intercept
		d := ys[i] - pred
		ssRes += d * d
	}
	if ssTot > 0 {
		r2 = 1 - ssRes/ssTot
	}
	return slope, intercept, r2
}

// fitModel fits velocity = intercept + slope*speed over the (non-aborted) samples.
func fitModel(samples []speedVel) linModel {
	var xs, ys []float64
	for _, s := range samples {
		if s.Aborted || s.Samples < 2 {
			continue
		}
		xs = append(xs, float64(s.Speed))
		ys = append(ys, s.StepsPerSec)
	}
	slope, intercept, r2 := linreg(xs, ys)
	return linModel{Intercept: intercept, Slope: slope, R2: r2}
}

func printModel(m linModel) {
	fmt.Printf("  model: vel = %.2f + %.2f*speed   (R2=%.4f)\n", m.Intercept, m.Slope, m.R2)
}

func abortTag(aborted bool) string {
	if aborted {
		return " ABORTED@limit"
	}
	return ""
}

func minF(v []float64) float64 {
	m := v[0]
	for _, x := range v {
		if x < m {
			m = x
		}
	}
	return m
}

func medianF(v []float64) float64 {
	c := append([]float64(nil), v...)
	// simple insertion sort — small n
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j-1] > c[j]; j-- {
			c[j-1], c[j] = c[j], c[j-1]
		}
	}
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

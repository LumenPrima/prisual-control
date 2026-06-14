// polltest: measures how fast we can poll a camera's pan/tilt position over
// VISCA TCP — both while idle and while the camera is jogging (which adds
// ACK/Completion traffic that SendRecv's drain must skip). Throwaway probe.
//
//	go run ./cmd/polltest --ip 192.168.1.50
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"prisual_control/internal/visca"
)

func main() {
	ip := flag.String("ip", "192.168.1.50", "camera IP")
	port := flag.Int("port", 5678, "VISCA TCP port")
	n := flag.Int("n", 100, "inquiries per phase")
	flag.Parse()

	conn, err := visca.Dial(*ip, *port)
	if err != nil {
		fmt.Println("DIAL FAILED:", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Printf("Connected %s:%d\n", *ip, *port)

	run := func(label string) {
		durs := make([]float64, 0, *n)
		fails := 0
		t0 := time.Now()
		for i := 0; i < *n; i++ {
			t := time.Now()
			_, _, err := visca.PanTiltInquiry(conn)
			d := float64(time.Since(t).Microseconds()) / 1000.0
			if err != nil {
				fails++
				continue
			}
			durs = append(durs, d)
		}
		wall := time.Since(t0).Seconds()
		report(label, durs, fails, wall, *n)
	}

	fmt.Println("\n--- idle ---")
	run("idle")

	fmt.Println("\n--- during jog ---")
	_ = visca.PanTiltVariable(conn, 6, 0) // slow pan so it doesn't run far
	run("jog")
	_ = visca.PanTiltVariable(conn, 0, 0)

	fmt.Println("\n--- command -> motion latency ---")
	measureLatency(conn, 10)

	_ = visca.StopAllMotion(conn)
}

// measureLatency times command-send to first detected motion, repeated reps
// times, alternating direction to stay near center. Relies on the now-fast
// polling: it settles to a verified stop, reads the baseline, fires a jog, then
// polls back-to-back until the position moves. Reported value = true latency +
// up to one poll interval (~11ms) + the time to accrue the >2-step threshold.
func measureLatency(conn *visca.Connection, reps int) {
	var out []float64
	for i := 0; i < reps; i++ {
		settleToStop(conn)
		p0, _, err := visca.PanTiltInquiry(conn)
		if err != nil {
			continue
		}
		dir := 1
		if i%2 == 1 {
			dir = -1
		}
		t0 := time.Now()
		_ = visca.PanTiltVariable(conn, dir*12, 0)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			pan, _, err := visca.PanTiltInquiry(conn)
			if err == nil && abs(float64(pan-p0)) > 2 {
				out = append(out, float64(time.Since(t0).Microseconds())/1000.0)
				break
			}
		}
		_ = visca.PanTiltVariable(conn, 0, 0)
	}
	if len(out) == 0 {
		fmt.Println("[latency] no measurements")
		return
	}
	sort.Float64s(out)
	var sum float64
	for _, d := range out {
		sum += d
	}
	fmt.Printf("[latency] n=%d  min=%.1f median=%.1f max=%.1f mean=%.1f ms\n",
		len(out), out[0], out[len(out)/2], out[len(out)-1], sum/float64(len(out)))
}

// settleToStop sends a stop and polls until the position holds steady (≤2 steps)
// for ~300ms, so the latency baseline isn't read mid-motion.
func settleToStop(conn *visca.Connection) {
	_ = visca.PanTiltVariable(conn, 0, 0)
	deadline := time.Now().Add(3 * time.Second)
	var last float64
	have := false
	stableSince := time.Now()
	for time.Now().Before(deadline) {
		pan, _, err := visca.PanTiltInquiry(conn)
		if err != nil {
			continue
		}
		pos := float64(pan)
		if have && abs(pos-last) <= 2 {
			if time.Since(stableSince) > 300*time.Millisecond {
				return
			}
		} else {
			stableSince = time.Now()
		}
		last = pos
		have = true
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func report(label string, durs []float64, fails int, wall float64, n int) {
	if len(durs) == 0 {
		fmt.Printf("[%s] all %d inquiries failed\n", label, n)
		return
	}
	sort.Float64s(durs)
	p := func(q float64) float64 { return durs[int(q*float64(len(durs)-1))] }
	var sum float64
	for _, d := range durs {
		sum += d
	}
	fmt.Printf("[%s] n=%d fails=%d  per-call ms: min=%.1f median=%.1f p95=%.1f max=%.1f mean=%.1f\n",
		label, len(durs), fails, durs[0], p(0.5), p(0.95), durs[len(durs)-1], sum/float64(len(durs)))
	fmt.Printf("[%s] effective rate: %.1f polls/sec (%.0f over %.2fs wall)\n",
		label, float64(n)/wall, float64(n), wall)
}

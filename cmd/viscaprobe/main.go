// viscaprobe: quick diagnostic — exercises the same VISCA TCP path the shim
// uses against a single camera. Useful for diagnosing "settling..." wedges.
//
// Steps:
//  1) Dial.
//  2) Cold focus inquiry — what the shim's anchorInitial does.
//  3) Spam a burst of pan/tilt jog commands (generates ACK/Completion traffic
//     that the shim's drain() must skip past).
//  4) Focus inquiry again — the realistic "user just moved the stick, now
//     release" scenario.
//  5) Repeat 3+4 a few times to see if the camera's reply pipeline gets stuck.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"prisual_control/internal/visca"
)

func main() {
	ip := flag.String("ip", "192.168.1.50", "camera IP")
	port := flag.Int("port", 5678, "VISCA TCP port")
	rounds := flag.Int("rounds", 5, "load test rounds")
	flag.Parse()

	fmt.Printf("Dialing %s:%d ...\n", *ip, *port)
	conn, err := visca.Dial(*ip, *port)
	if err != nil {
		fmt.Println("DIAL FAILED:", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Println("Connected.")

	// 1) Cold inquiry — mirror anchorInitial.
	t0 := time.Now()
	pos, err := visca.FocusInquiry(conn)
	if err != nil {
		fmt.Printf("[cold] FocusInquiry FAILED after %v: %v\n", time.Since(t0), err)
	} else {
		fmt.Printf("[cold] FocusInquiry ok: 0x%04X (%v)\n", pos, time.Since(t0))
	}

	mode, err := visca.FocusModeInquiry(conn)
	if err != nil {
		fmt.Printf("[cold] FocusModeInquiry FAILED: %v\n", err)
	} else {
		fmt.Printf("[cold] FocusMode = %s\n", mode)
	}

	// Loop: send a flurry of jogs (generates ACK/Completion in the camera's
	// reply stream) then issue a focus inquiry. Tests whether drain() correctly
	// clears stale replies before the inquiry.
	for r := 1; r <= *rounds; r++ {
		fmt.Printf("\n--- Round %d ---\n", r)

		// pan/tilt jog burst — same packet shape the shim sends when stick is moving
		for i := 0; i < 8; i++ {
			// pan/tilt drive at low speed: 81 01 06 01 vv ww 03 03 FF (right)
			if err := conn.Send([]byte{0x81, 0x01, 0x06, 0x01, 0x04, 0x04, 0x03, 0x03, 0xFF}); err != nil {
				fmt.Printf("  jog %d send err: %v\n", i, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		// stop
		_ = conn.Send([]byte{0x81, 0x01, 0x06, 0x01, 0x00, 0x00, 0x03, 0x03, 0xFF})
		time.Sleep(50 * time.Millisecond)

		t1 := time.Now()
		pos, err := visca.FocusInquiry(conn)
		if err != nil {
			fmt.Printf("  FocusInquiry FAILED after %v: %v\n", time.Since(t1), err)
		} else {
			fmt.Printf("  FocusInquiry ok: 0x%04X (%v)\n", pos, time.Since(t1))
		}
	}
}

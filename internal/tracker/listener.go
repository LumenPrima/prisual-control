// Package tracker receives auto-tracker error packets from the Python
// tracker (tracker/track.py) over UDP. Each packet is one JSON object
// per locked stream, sent at ~5 Hz.
package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"
)

// Event is one aim-point sample from the tracker, identifying the source
// camera by IP and the locked track ID within that stream. The aim point
// is in normalized frame coordinates ([0,1], top-left origin); the shim
// subtracts its own per-mode framing target to derive the PD error.
type Event struct {
	Cam  string    `json:"cam"`
	ID   int       `json:"id"`
	Mode string    `json:"mode"`
	Ax   float64   `json:"ax"`
	Ay   float64   `json:"ay"`
	Ts   float64   `json:"ts"`
	At   time.Time `json:"-"`
}

// Listen binds a UDP socket on 127.0.0.1:port and forwards parsed events
// to ch until ctx is cancelled. Bad packets are logged and dropped.
func Listen(ctx context.Context, port int, ch chan<- Event) {
	addr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Printf("tracker listener: bind %s failed: %v", addr, err)
		return
	}
	defer conn.Close()
	log.Printf("tracker listener: bound %s", addr)

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	buf := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("tracker listener: read: %v", err)
			continue
		}
		var ev Event
		if err := json.Unmarshal(buf[:n], &ev); err != nil {
			log.Printf("tracker listener: bad packet: %v (%q)", err, string(buf[:n]))
			continue
		}
		ev.At = time.Now()
		select {
		case ch <- ev:
		case <-ctx.Done():
			return
		default:
			// channel full — drop; next packet ~200ms away
		}
	}
}

// String is a debug helper used by the shim's log line.
func (e Event) String() string {
	return fmt.Sprintf("cam=%s id=%d mode=%s aim=(%.3f,%.3f)", e.Cam, e.ID, e.Mode, e.Ax, e.Ay)
}

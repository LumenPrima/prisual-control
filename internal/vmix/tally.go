package vmix

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

// TallyUpdate carries the current program and preview input numbers.
type TallyUpdate struct {
	Program int // 1-based input number, 0 = none
	Preview int
}

// SubscribeTally connects to vMix's TCP API, subscribes to tally updates,
// and sends parsed updates on ch. Auto-reconnects with backoff on failure.
// Blocks until ctx is cancelled.
func SubscribeTally(ctx context.Context, host string, port int, ch chan<- TallyUpdate) {
	addr := fmt.Sprintf("%s:%d", host, port)
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := tallySession(ctx, addr, ch); err != nil {
			log.Printf("tally: %v (reconnecting in %v)", err, backoff)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		// Increase backoff up to 10s
		backoff = backoff * 2
		if backoff > 10*time.Second {
			backoff = 10 * time.Second
		}
	}
}

func tallySession(ctx context.Context, addr string, ch chan<- TallyUpdate) error {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	// Subscribe to tally
	if _, err := fmt.Fprintf(conn, "SUBSCRIBE TALLY\r\n"); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		line := scanner.Text()
		update, ok := parseTallyLine(line)
		if ok {
			select {
			case ch <- update:
			default:
				// Drop if channel full (non-blocking)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	return fmt.Errorf("connection closed")
}

// parseTallyLine parses a vMix tally response line.
// Format: "TALLY OK <tally_string>" where each character is the tally state
// for that input (0=off, 1=program, 2=preview).
func parseTallyLine(line string) (TallyUpdate, bool) {
	if !strings.HasPrefix(line, "TALLY OK ") {
		return TallyUpdate{}, false
	}

	tally := strings.TrimPrefix(line, "TALLY OK ")
	var update TallyUpdate

	for i, ch := range tally {
		inputNum := i + 1 // 1-based
		switch ch {
		case '1':
			update.Program = inputNum
		case '2':
			update.Preview = inputNum
		}
	}

	return update, true
}

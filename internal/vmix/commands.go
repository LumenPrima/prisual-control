package vmix

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// Commander sends commands to vMix via TCP API (port 8099).
// Safe for use from a single goroutine (the main goroutine).
type Commander struct {
	addr string
	conn net.Conn
	mu   sync.Mutex
}

// NewCommander creates a Commander for the given vMix host.
func NewCommander(host string, port int) *Commander {
	return &Commander{addr: fmt.Sprintf("%s:%d", host, port)}
}

// connect establishes or re-establishes the TCP connection.
func (c *Commander) connect() error {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	conn, err := net.DialTimeout("tcp", c.addr, 3*time.Second)
	if err != nil {
		return fmt.Errorf("vmix connect %s: %w", c.addr, err)
	}
	c.conn = conn
	return nil
}

// Send sends a raw command string to vMix. Auto-connects/reconnects.
func (c *Commander) Send(cmd string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Try sending, reconnect once on failure
	for attempt := 0; attempt < 2; attempt++ {
		if c.conn == nil {
			if err := c.connect(); err != nil {
				return err
			}
		}
		c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, err := fmt.Fprintf(c.conn, "%s\r\n", cmd)
		if err == nil {
			return nil
		}
		// Connection broken, retry
		c.conn.Close()
		c.conn = nil
	}
	return fmt.Errorf("vmix send failed after retry")
}

// Close closes the connection.
func (c *Commander) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// --- vMix transition/switching commands ---

// Cut performs an instant cut from program to preview.
func (c *Commander) Cut() error {
	return c.Send("FUNCTION Cut")
}

// Fade performs a fade transition with the given duration in milliseconds.
func (c *Commander) Fade(durationMs int) error {
	return c.Send(fmt.Sprintf("FUNCTION Fade Duration=%d", durationMs))
}

// SetPreview sets the preview input by number.
func (c *Commander) SetPreview(input int) error {
	return c.Send(fmt.Sprintf("FUNCTION PreviewInput Input=%d", input))
}

// CutDirect instantly cuts to the given input (bypasses preview).
func (c *Commander) CutDirect(input int) error {
	return c.Send(fmt.Sprintf("FUNCTION CutDirect Input=%d", input))
}

// Transition performs a named transition (Cut, Fade, Zoom, Wipe, etc.).
func (c *Commander) Transition(name string, durationMs int) error {
	if durationMs > 0 {
		return c.Send(fmt.Sprintf("FUNCTION %s Duration=%d", name, durationMs))
	}
	return c.Send(fmt.Sprintf("FUNCTION %s", name))
}

// NextPreview cycles the preview to the next input.
func (c *Commander) NextPreview(numInputs, currentPreview int) error {
	next := currentPreview + 1
	if next > numInputs {
		next = 1
	}
	return c.SetPreview(next)
}

// PrevPreview cycles the preview to the previous input.
func (c *Commander) PrevPreview(numInputs, currentPreview int) error {
	prev := currentPreview - 1
	if prev < 1 {
		prev = numInputs
	}
	return c.SetPreview(prev)
}

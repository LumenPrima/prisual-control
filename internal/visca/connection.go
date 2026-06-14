package visca

import (
	"fmt"
	"net"
	"time"
)

// Connection is a non-blocking VISCA TCP connection.
// Only accessed from the main goroutine — no mutex needed.
type Connection struct {
	conn net.Conn
	addr string
}

// Dial connects to a camera's VISCA TCP port.
func Dial(ip string, port int) (*Connection, error) {
	addr := fmt.Sprintf("%s:%d", ip, port)
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("visca dial %s: %w", addr, err)
	}
	return &Connection{conn: conn, addr: addr}, nil
}

// drain reads and discards any pending responses without blocking.
func (c *Connection) drain() {
	c.conn.SetReadDeadline(time.Now())
	buf := make([]byte, 1024)
	for {
		_, err := c.conn.Read(buf)
		if err != nil {
			break
		}
	}
	c.conn.SetReadDeadline(time.Time{})
}

// Send drains stale responses then sends a command (fire-and-forget).
func (c *Connection) Send(cmd []byte) error {
	c.drain()
	c.conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
	_, err := c.conn.Write(cmd)
	c.conn.SetWriteDeadline(time.Time{})
	if err != nil {
		return fmt.Errorf("visca send to %s: %w", c.addr, err)
	}
	return nil
}

// SendRecv drains, sends a command, then waits for a response (for inquiries).
// It reads in a loop until a complete VISCA inquiry reply frame (yX 50 ... FF)
// is seen, or the overall deadline expires. A single Read is unreliable because
// TCP can fragment the reply — slower cameras may deliver the header and payload
// in separate packets, so a fixed sleep-then-read misses part of the frame.
func (c *Connection) SendRecv(cmd []byte) ([]byte, error) {
	c.drain()
	c.conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
	_, err := c.conn.Write(cmd)
	c.conn.SetWriteDeadline(time.Time{})
	if err != nil {
		return nil, fmt.Errorf("visca send to %s: %w", c.addr, err)
	}

	deadline := time.Now().Add(50 * time.Millisecond)
	var buf []byte
	tmp := make([]byte, 256)
	for {
		c.conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		n, rerr := c.conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if containsInquiryReply(buf) {
				c.conn.SetReadDeadline(time.Time{})
				return buf, nil
			}
		}
		if rerr != nil {
			if ne, ok := rerr.(net.Error); !ok || !ne.Timeout() {
				c.conn.SetReadDeadline(time.Time{})
				return buf, fmt.Errorf("visca recv from %s: %w", c.addr, rerr)
			}
		}
		if time.Now().After(deadline) {
			c.conn.SetReadDeadline(time.Time{})
			if len(buf) == 0 {
				return nil, fmt.Errorf("visca recv from %s: timeout", c.addr)
			}
			return nil, fmt.Errorf("visca recv from %s: incomplete reply (%d bytes)", c.addr, len(buf))
		}
	}
}

// containsInquiryReply reports whether buf contains a complete VISCA inquiry
// reply frame: yX 50 <data...> FF. ACK frames (yX 4Y FF) and command
// completions (yX 5Y FF with Y != 0) are ignored.
func containsInquiryReply(buf []byte) bool {
	for i := 0; i+1 < len(buf); i++ {
		if buf[i]&0xF0 != 0x90 || buf[i+1] != 0x50 {
			continue
		}
		for j := i + 2; j < len(buf); j++ {
			if buf[j] == 0xFF {
				return true
			}
		}
		return false
	}
	return false
}

// Close closes the TCP connection.
func (c *Connection) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

// Addr returns the remote address string.
func (c *Connection) Addr() string {
	return c.addr
}

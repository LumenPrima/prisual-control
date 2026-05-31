package visca

import (
	"fmt"
	"sync/atomic"
	"time"
)

// Sender is the minimal interface for fire-and-forget VISCA writes.
// Both *Connection and *Camera satisfy it, so command helpers in
// commands.go work with either: *Connection blocks the caller while it
// writes, *Camera queues the work to its own goroutine and returns.
type Sender interface {
	Send(cmd []byte) error
}

// Reply is an inquiry result delivered back to the main goroutine via the
// shared replies channel. Tag is opaque to the worker — the caller uses it
// to identify the inquiry it issued.
type Reply struct {
	CamAddr string
	Tag     interface{}
	Result  interface{}
	Err     error
}

// Camera wraps a Connection in a per-camera worker goroutine. The main loop
// queues commands via Send and inquiries via Inquire*; the worker owns the
// Connection and serializes all I/O. A wedged camera blocks only its own
// worker — main, the UI, and other cameras stay responsive.
//
// The worker tracks consecutive I/O failures and rebuilds the Connection
// after several failures, rate-limited so a permanently-down camera doesn't
// thrash. Healthy reflects whether the most recent operation succeeded.
type Camera struct {
	addr string
	ip   string
	port int

	cmds    chan workItem
	replies chan<- Reply
	done    chan struct{}

	healthy atomic.Bool
}

const (
	cmdQueueSize            = 64
	failuresBeforeReconnect = 3
	minReconnectInterval    = 5 * time.Second
)

type workItem struct {
	fn        func(*Connection) (interface{}, error)
	tag       interface{}
	isInquiry bool
}

// PanTiltResult bundles a pan/tilt inquiry response.
type PanTiltResult struct {
	Pan, Tilt int16
}

// NewCamera starts a worker goroutine for the given camera. Replies from
// inquiries are posted to the shared replies channel. The Camera is returned
// even if the initial dial fails — the worker will keep retrying.
func NewCamera(ip string, port int, replies chan<- Reply) *Camera {
	c := &Camera{
		addr:    fmt.Sprintf("%s:%d", ip, port),
		ip:      ip,
		port:    port,
		cmds:    make(chan workItem, cmdQueueSize),
		replies: replies,
		done:    make(chan struct{}),
	}
	go c.run()
	return c
}

func (c *Camera) Addr() string  { return c.addr }
func (c *Camera) IP() string    { return c.ip }
func (c *Camera) Healthy() bool { return c.healthy.Load() }

// Close stops the worker. Safe to call multiple times.
func (c *Camera) Close() {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
}

// Send queues a fire-and-forget command. Returns immediately. Drops the
// command (returning an error) if the queue is full — a sick camera can fill
// its queue, and silent dropping would mask the problem.
func (c *Camera) Send(cmd []byte) error {
	item := workItem{
		fn: func(conn *Connection) (interface{}, error) {
			return nil, conn.Send(cmd)
		},
	}
	select {
	case c.cmds <- item:
		return nil
	case <-c.done:
		return fmt.Errorf("camera %s: closed", c.addr)
	default:
		return fmt.Errorf("camera %s: queue full", c.addr)
	}
}

func (c *Camera) inquire(tag interface{}, fn func(*Connection) (interface{}, error)) {
	item := workItem{fn: fn, tag: tag, isInquiry: true}
	select {
	case c.cmds <- item:
	case <-c.done:
		c.postReply(Reply{CamAddr: c.addr, Tag: tag, Err: fmt.Errorf("camera %s: closed", c.addr)})
	default:
		c.postReply(Reply{CamAddr: c.addr, Tag: tag, Err: fmt.Errorf("camera %s: queue full", c.addr)})
	}
}

func (c *Camera) InquireFocus(tag interface{}) {
	c.inquire(tag, func(conn *Connection) (interface{}, error) { return FocusInquiry(conn) })
}

func (c *Camera) InquireZoom(tag interface{}) {
	c.inquire(tag, func(conn *Connection) (interface{}, error) { return ZoomInquiry(conn) })
}

func (c *Camera) InquirePanTilt(tag interface{}) {
	c.inquire(tag, func(conn *Connection) (interface{}, error) {
		pan, tilt, err := PanTiltInquiry(conn)
		if err != nil {
			return nil, err
		}
		return PanTiltResult{Pan: pan, Tilt: tilt}, nil
	})
}

func (c *Camera) InquireShutter(tag interface{}) {
	c.inquire(tag, func(conn *Connection) (interface{}, error) { return ShutterInquiry(conn) })
}

func (c *Camera) InquireGain(tag interface{}) {
	c.inquire(tag, func(conn *Connection) (interface{}, error) { return GainInquiry(conn) })
}

func (c *Camera) InquireFocusMode(tag interface{}) {
	c.inquire(tag, func(conn *Connection) (interface{}, error) { return FocusModeInquiry(conn) })
}

func (c *Camera) postReply(r Reply) {
	select {
	case c.replies <- r:
	case <-c.done:
	}
}

func (c *Camera) run() {
	var conn *Connection
	var lastReconnect time.Time
	failures := 0

	closeConn := func() {
		if conn != nil {
			conn.Close()
			conn = nil
		}
	}

	tryReconnect := func() {
		if time.Since(lastReconnect) < minReconnectInterval {
			return
		}
		lastReconnect = time.Now()
		closeConn()
		if cn, err := Dial(c.ip, c.port); err == nil {
			conn = cn
			failures = 0
			c.healthy.Store(true)
		} else {
			c.healthy.Store(false)
		}
	}

	if cn, err := Dial(c.ip, c.port); err == nil {
		conn = cn
		c.healthy.Store(true)
	}
	lastReconnect = time.Now()

	for {
		select {
		case <-c.done:
			closeConn()
			return
		case item := <-c.cmds:
			if conn == nil {
				tryReconnect()
				if conn == nil {
					if item.isInquiry {
						c.postReply(Reply{
							CamAddr: c.addr, Tag: item.tag,
							Err: fmt.Errorf("camera %s: not connected", c.addr),
						})
					}
					continue
				}
			}

			result, err := item.fn(conn)
			if err != nil {
				failures++
				c.healthy.Store(false)
				if failures >= failuresBeforeReconnect {
					tryReconnect()
				}
			} else {
				failures = 0
				c.healthy.Store(true)
			}

			if item.isInquiry {
				c.postReply(Reply{CamAddr: c.addr, Tag: item.tag, Result: result, Err: err})
			}
		}
	}
}

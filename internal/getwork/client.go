package getwork

import (
	"context"
	"crypto/tls"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	handshakeTimeout = 10 * time.Second
	// Jobs arrive ~every 500ms; 20s of silence means the link is dead.
	readTimeout    = 20 * time.Second
	writeTimeout   = 10 * time.Second
	initialBackoff = time.Second
	maxBackoff     = 15 * time.Second
)

// Client maintains one GETWORK websocket connection to a DERO daemon or pool,
// delivering pushed jobs via OnJob and draining Submits into the socket.
type Client struct {
	Endpoint string // [ws://|wss://]host:port ; bare host:port implies wss
	Wallet   string
	OnJob    func(Job)     // called from the reader goroutine
	Submits  <-chan Submit // drained by the writer goroutine
	Logf     func(format string, args ...interface{})
	// Errorf receives connection-failure records. Unlike Debugf chatter these
	// are always-on in the family CLIs (a phone user cannot tell a slow
	// handshake from a hang without them). May be nil.
	Errorf func(format string, args ...interface{})
	// Debugf receives retry/loss/submit-failure chatter that the family CLIs
	// keep silent by default (the zig miner reconnects without a word). May be
	// nil.
	Debugf func(format string, args ...interface{})

	Connected atomic.Bool
	SendFails atomic.Uint64
	// Shares discarded at redial because they were found on a connection that
	// no longer exists. See discardStaleSubmits.
	Discarded atomic.Uint64
}

// discardStaleSubmits empties the submit channel before a new session starts
// using it.
//
// The channel is created once (main.go) and outlives every connection, while
// workers gate only on "a job exists" — not on being connected. So during an
// outage they keep mining the last job and keep filling the buffer, and every
// one of those shares belongs to a session that is gone. Without this they are
// flushed onto the *fresh* connection the moment it comes up: reproduced
// against a test daemon, a share was accepted into the queue and submitted 12
// seconds later on a different connection, against a job the server had long
// since replaced.
//
// Submit carries no epoch, so the writer cannot tell a stale entry from a live
// one — draining at the session boundary is what makes the distinction, and it
// is where the whole accumulated backlog is discarded in one place. Rust does
// the same thing at the same point (main.rs, "drop shares queued while
// disconnected — their jobs are stale anyway"); C++ and Zig are covered instead
// by a per-item epoch re-check on dequeue.
//
// This does not close the window entirely: workers keep mining the old job
// until the first push of the new session arrives (~500ms), so a share found in
// that gap is still submitted stale. That residue is bounded by job cadence
// rather than by reconnect backoff, which is the part that actually mattered.
func (c *Client) discardStaleSubmits() {
	n := uint64(0)
	for {
		select {
		case _, ok := <-c.Submits:
			if !ok {
				return
			}
			n++
		default:
			if n > 0 {
				c.Discarded.Add(n)
				c.debugf("discarded %d share(s) queued while disconnected", n)
			}
			return
		}
	}
}

func (c *Client) logf(format string, args ...interface{}) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

func (c *Client) debugf(format string, args ...interface{}) {
	if c.Debugf != nil {
		c.Debugf(format, args...)
	}
}

func (c *Client) errorf(format string, args ...interface{}) {
	if c.Errorf != nil {
		c.Errorf(format, args...)
	}
}

// HostPort is the endpoint without any ws:// / wss:// scheme, for display.
func (c *Client) HostPort() string {
	if i := strings.Index(c.Endpoint, "://"); i >= 0 {
		return c.Endpoint[i+3:]
	}
	return c.Endpoint
}

// URL returns the getwork endpoint: wss://host:port/ws/<wallet>.
func (c *Client) URL() string {
	ep := c.Endpoint
	if !strings.Contains(ep, "://") {
		ep = "wss://" + ep
	}
	return strings.TrimSuffix(ep, "/") + "/ws/" + c.Wallet
}

// Run dials and serves the connection until ctx is cancelled, reconnecting
// with capped exponential backoff. Backoff resets after any connection that
// delivered at least one job.
func (c *Client) Run(ctx context.Context) {
	backoff := initialBackoff
	for ctx.Err() == nil {
		jobs, err := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			return
		}
		if jobs > 0 {
			backoff = initialBackoff
			continue // a live link dropped: redial immediately
		}
		if err != nil {
			c.errorf("Connection failed (%s): %v", c.HostPort(), err)
		}
		c.logf("Retrying in %ds", int(backoff.Seconds()))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// connectAndServe runs one connection to completion, reporting how many jobs
// it delivered and the dial error if the connection never came up (read-loop
// losses on an established link stay Debugf chatter).
func (c *Client) connectAndServe(ctx context.Context) (jobs uint64, err error) {
	dialer := websocket.Dialer{
		HandshakeTimeout: handshakeTimeout,
		// The daemon presents a random self-signed certificate; verification
		// must be off (same as the official miner and every family miner).
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		// Resolve through public-DNS fallbacks when the system resolver is
		// unusable (Termux/Android has no /etc/resolv.conf) — dns.go.
		NetDialContext: dialContextDNSFallback,
	}
	c.logf("Connecting (%s)", c.HostPort())
	dialStart := time.Now()
	conn, _, err := dialer.DialContext(ctx, c.URL(), nil)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	c.Connected.Store(true)
	defer c.Connected.Store(false)
	c.logf("Connected (%s) (%d ms)", c.HostPort(), time.Since(dialStart).Milliseconds())

	// Anything still buffered was found on the previous connection. Drain it
	// before the writer starts, or the new session opens with a burst of
	// submissions the daemon can only reject.
	c.discardStaleSubmits()

	// Writer: the sole goroutine writing data frames (gorilla allows exactly
	// one concurrent writer; control frames from the default ping handler are
	// safe alongside it). quit is closed by the reader when the connection
	// dies — without it the writer sits parked on ctx/Submits after a link
	// drop, the reader blocks on writerDone, and the client never redials
	// (with no pending submit, e.g. --dry-run, the stall was permanent).
	writerDone := make(chan struct{})
	quit := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-ctx.Done():
				conn.Close() // unblocks the reader
				return
			case <-quit:
				return
			case s, ok := <-c.Submits:
				if !ok {
					return
				}
				conn.SetWriteDeadline(time.Now().Add(writeTimeout))
				if err := conn.WriteJSON(s); err != nil {
					c.SendFails.Add(1)
					c.debugf("submit write failed: %v", err)
					conn.Close()
					return
				}
			}
		}
	}()

	for {
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		var j Job
		if err := conn.ReadJSON(&j); err != nil {
			if ctx.Err() == nil {
				c.debugf("connection to %s lost: %v", c.Endpoint, err)
			}
			conn.Close()
			close(quit)
			<-writerDone
			return jobs, nil
		}
		jobs++
		c.OnJob(j)
	}
}

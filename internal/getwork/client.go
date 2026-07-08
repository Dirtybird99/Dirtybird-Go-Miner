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
	// Debugf receives retry/loss/submit-failure chatter that the family CLIs
	// keep silent by default (the zig miner reconnects without a word). May be
	// nil.
	Debugf func(format string, args ...interface{})

	Connected atomic.Bool
	SendFails atomic.Uint64
}

func (c *Client) debugf(format string, args ...interface{}) {
	if c.Debugf != nil {
		c.Debugf(format, args...)
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
		jobs := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			return
		}
		if jobs > 0 {
			backoff = initialBackoff
			continue // a live link dropped: redial immediately
		}
		c.debugf("connect to %s failed, retrying in %v", c.Endpoint, backoff)
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

// connectAndServe runs one connection to completion and reports how many jobs
// it delivered.
func (c *Client) connectAndServe(ctx context.Context) (jobs uint64) {
	dialer := websocket.Dialer{
		HandshakeTimeout: handshakeTimeout,
		// The daemon presents a random self-signed certificate; verification
		// must be off (same as the official miner and every family miner).
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	c.Logf("Connecting (%s)", c.HostPort())
	dialStart := time.Now()
	conn, _, err := dialer.DialContext(ctx, c.URL(), nil)
	if err != nil {
		return 0
	}
	defer conn.Close()

	c.Connected.Store(true)
	defer c.Connected.Store(false)
	c.Logf("Connected (%s) (%d ms)", c.HostPort(), time.Since(dialStart).Milliseconds())

	// Writer: the sole goroutine writing data frames (gorilla allows exactly
	// one concurrent writer; control frames from the default ping handler are
	// safe alongside it).
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-ctx.Done():
				conn.Close() // unblocks the reader
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
			<-writerDone
			return jobs
		}
		jobs++
		c.OnJob(j)
	}
}

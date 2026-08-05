package getwork

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestReconnectAfterServerCloseWithoutSubmits pins the redial path when the
// server drops an established link and no submit is pending (the --dry-run
// shape): the reader used to block on writerDone while the writer sat parked
// on ctx/Submits, so the client never redialed until the next share — hours
// at phone hashrates, forever in dry-run.
func TestReconnectAfterServerCloseWithoutSubmits(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var mu sync.Mutex
	conns := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		conns++
		n := conns
		mu.Unlock()
		_ = conn.WriteJSON(Job{JobID: fmt.Sprintf("job-%d", n), Height: uint64(n)})
		if n == 1 {
			conn.Close() // server drops the first link right after its job
			return
		}
		for { // hold the second link open until the client goes away
			if _, _, err := conn.ReadMessage(); err != nil {
				conn.Close()
				return
			}
		}
	}))
	defer srv.Close()

	jobs := make(chan Job, 4)
	disconnected := make(chan struct{}, 4)
	client := &Client{
		Endpoint:     "ws://" + srv.Listener.Addr().String(),
		Wallet:       "w",
		Submits:      make(chan Submit), // never fed: the stall precondition
		OnJob:        func(j Job) bool { jobs <- j; return true },
		OnDisconnect: func() { disconnected <- struct{}{} },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		client.Run(ctx)
		close(done)
	}()

	waitJob := func(want string) {
		t.Helper()
		select {
		case j := <-jobs:
			if j.JobID != want {
				t.Fatalf("got job %q, want %q", j.JobID, want)
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %q — client never redialed", want)
		}
	}
	waitJob("job-1")
	select {
	case <-disconnected:
		if client.Connected.Load() {
			t.Fatal("disconnect callback ran while Connected was still true")
		}
	case <-ctx.Done():
		t.Fatal("disconnect callback did not run after the first session closed")
	}
	waitJob("job-2")

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// A share found on a connection that has since died must not be flushed onto
// the next one. The submit channel is created once and outlives every session,
// while workers gate only on "a job exists" -- not on being connected -- so an
// outage fills the buffer with shares the daemon can only reject.
//
// Measured against a test daemon before this drain existed: an 18s outage put
// 17 stale submissions on the fresh connection (the 16-deep buffer plus one
// found in the window before the first new job), all for a job the server had
// long since replaced. After: 0.
func TestDiscardStaleSubmitsEmptiesTheBacklog(t *testing.T) {
	ch := make(chan Submit, 16)
	c := &Client{Submits: ch}

	for i := 0; i < 16; i++ {
		ch <- Submit{JobID: "old-job", Blob: "deadbeef"}
	}
	c.discardStaleSubmits()

	if len(ch) != 0 {
		t.Fatalf("drain left %d share(s) queued; the next session would submit them", len(ch))
	}
	if got := c.Discarded.Load(); got != 16 {
		t.Fatalf("Discarded = %d, want 16 -- the loss has to be countable, not silent", got)
	}
}

// The counter must mean "a share was thrown away", not "a reconnect happened":
// a redial with nothing buffered is the healthy case and must stay at zero, or
// the number is useless for spotting the unhealthy one.
func TestDiscardStaleSubmitsIsQuietWhenEmpty(t *testing.T) {
	c := &Client{Submits: make(chan Submit, 16)}
	c.discardStaleSubmits()
	if got := c.Discarded.Load(); got != 0 {
		t.Fatalf("Discarded = %d on an empty channel, want 0", got)
	}
}

// Draining must not block or spin when the channel is closed at shutdown.
func TestDiscardStaleSubmitsHandlesClosedChannel(t *testing.T) {
	ch := make(chan Submit, 4)
	ch <- Submit{JobID: "old-job"}
	close(ch)
	c := &Client{Submits: ch}
	done := make(chan struct{})
	go func() { c.discardStaleSubmits(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("discardStaleSubmits did not return on a closed channel")
	}
	if got := c.Discarded.Load(); got != 1 {
		t.Fatalf("Discarded = %d, want 1", got)
	}
}

func TestSubmitValidationDropsStaleEpoch(t *testing.T) {
	c := &Client{SubmitValid: func(s Submit) bool { return s.Epoch == 7 }}
	if !c.submitIsCurrent(Submit{Epoch: 7}) {
		t.Fatal("current submit was rejected")
	}
	if c.submitIsCurrent(Submit{JobID: "old", Epoch: 6}) {
		t.Fatal("stale submit was accepted")
	}
	if got := c.Discarded.Load(); got != 1 {
		t.Fatalf("Discarded = %d, want 1", got)
	}
}

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
	client := &Client{
		Endpoint: "ws://" + srv.Listener.Addr().String(),
		Wallet:   "w",
		Submits:  make(chan Submit), // never fed: the stall precondition
		OnJob:    func(j Job) { jobs <- j },
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
	waitJob("job-2")

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

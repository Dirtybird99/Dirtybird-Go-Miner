// Copyright 2017-2026 DERO Project. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.
package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"go-miner/internal/getwork"
)

// validBlob is a real 48-byte miniblock (version nibble 1), the shape a
// getwork daemon pushes.
const validBlob = "419ebb000000001bbdc9bf2200000000635d6e4e24829b4249fe0e67878ad4350000000043f53e5436cf610000086b00"

func TestStartRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"no endpoint", Config{Wallet: "dero1qytest"}},
		{"no wallet", Config{Endpoint: "localhost:10100"}},
		{"neither", Config{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, err := Start(context.Background(), c.cfg)
			if err == nil {
				e.Stop()
				t.Fatal("Start succeeded; want ErrInvalidConfig")
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestStartRejectsUnknownBackend(t *testing.T) {
	_, err := Start(context.Background(), Config{
		Endpoint: "localhost:10100",
		Wallet:   "dero1qytest",
		Backend:  "bogus",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown backend") {
		t.Fatalf("err = %v, want unknown-backend error", err)
	}
}

func TestStartBrokenHashRefusesToMine(t *testing.T) {
	// Both real backends must pass the KAT; a wrong constant simulates a
	// broken pipeline.
	old := katHash
	katHash = "0000000000000000000000000000000000000000000000000000000000000000"
	defer func() { katHash = old }()
	_, err := Start(context.Background(), Config{
		Endpoint: "localhost:10100",
		Wallet:   "dero1qytest",
		Threads:  1,
	})
	if err == nil || !errors.Is(err, ErrBrokenHash) {
		t.Fatalf("err = %v, want ErrBrokenHash", err)
	}
}

func TestNormalizeThreads(t *testing.T) {
	if got := NormalizeThreads(0); got < 1 || got > MaxThreads {
		t.Fatalf("NormalizeThreads(0) = %d, want in [1,%d]", got, MaxThreads)
	}
	if got := NormalizeThreads(MaxThreads + 100); got != MaxThreads {
		t.Fatalf("NormalizeThreads(over) = %d, want %d", got, MaxThreads)
	}
	if got := NormalizeThreads(-5); got < 1 {
		t.Fatalf("NormalizeThreads(-5) = %d, want >= 1", got)
	}
}

// TestStartStopConnectsAndStats is the happy path against a real getwork
// websocket server: the client dials, receives a valid job, workers mine it,
// and Stats reports connected/height/hashrate before Stop tears everything
// down.
func TestStartStopConnectsAndStats(t *testing.T) {
	upgrader := websocket.Upgrader{}
	submits := make(chan getwork.Submit, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for i := 0; i < 3; i++ { // push a few jobs so workers re-snapshot
			if err := conn.WriteJSON(getwork.Job{
				JobID:             fmt.Sprintf("job-%d", i),
				Blockhashing_blob: validBlob,
				Difficultyuint64:  1000,
				Height:            uint64(1000 + i),
			}); err != nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		for { // drain submits until the client goes away
			var s getwork.Submit
			if err := conn.ReadJSON(&s); err != nil {
				return
			}
			select {
			case submits <- s:
			default:
			}
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	e, err := Start(ctx, Config{
		Endpoint: "ws://" + srv.Listener.Addr().String(),
		Wallet:   "dero1qytest",
		Threads:  1,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait until a job is installed and hashing starts.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s := e.Stats()
		if s.Connected && s.Height >= 1000 && s.Hashes > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	st := e.Stats()
	if !st.Connected {
		t.Fatal("client never connected")
	}
	if st.Height < 1000 || st.Height > 1002 {
		t.Fatalf("Height = %d, want in [1000,1002]", st.Height)
	}
	if st.Hashes == 0 {
		t.Fatal("no hashes counted while mining")
	}
	if st.Running != true {
		t.Fatal("Running = false, want true")
	}
	if st.Threads != 1 {
		t.Fatalf("Threads = %d, want 1", st.Threads)
	}
	if !strings.Contains(st.Endpoint, srv.Listener.Addr().String()) {
		t.Fatalf("Endpoint = %q, want host %q", st.Endpoint, srv.Listener.Addr().String())
	}
	if st.Wallet != "dero1qytest" {
		t.Fatalf("Wallet = %q", st.Wallet)
	}

	done := make(chan struct{})
	go func() { e.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return within 5s")
	}

	// Stop is idempotent.
	e.Stop()
}

// TestKeepaliveFrameDoesNotKillMining pins the CLI rejection policy: a
// non-version rejection (short blob / empty jobid) is a keepalive/status
// frame and must NOT invalidate the last good job.
func TestKeepaliveFrameDoesNotKillMining(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(getwork.Job{JobID: "job-0", Blockhashing_blob: validBlob, Difficultyuint64: 1000, Height: 1000})
		// keepalive frame with no blob
		_ = conn.WriteJSON(getwork.Job{JobID: "ka"})
		time.Sleep(2 * time.Second)
		_ = conn.WriteJSON(getwork.Job{JobID: "job-1", Blockhashing_blob: validBlob, Difficultyuint64: 1000, Height: 1001})
		for {
			var s getwork.Submit
			if err := conn.ReadJSON(&s); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	e, err := Start(ctx, Config{
		Endpoint: "ws://" + srv.Listener.Addr().String(),
		Wallet:   "dero1qytest",
		Threads:  1,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// After the keepalive, hashing must continue (job-0 still active).
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		s := e.Stats()
		if s.Connected && s.Hashes > 10 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := e.Stats().Hashes; got == 0 {
		t.Fatal("mining stopped after a keepalive frame; want last job to stay active")
	}
	e.Stop()
}

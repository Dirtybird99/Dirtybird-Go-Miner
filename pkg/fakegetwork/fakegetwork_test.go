// Copyright 2017-2026 DERO Project. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.
package fakegetwork

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"go-miner/internal/getwork"
)

func TestValidBlobShape(t *testing.T) {
	b, err := hex.DecodeString(validBlobHex)
	if err != nil {
		t.Fatalf("validBlobHex not hex: %v", err)
	}
	if len(b) != 48 {
		t.Fatalf("blob length = %d, want 48", len(b))
	}
	if b[0]&0xf != 1 {
		t.Fatalf("version nibble = %d, want 1 (Go miner refuses other values)", b[0]&0xf)
	}
}

func TestValidJob(t *testing.T) {
	j := ValidJob("abc", 42)
	if j.JobID != "abc" || j.Height != 42 || j.Difficulty < 2 {
		t.Fatalf("ValidJob = %+v", j)
	}
	if j.BlockhashingBlob != validBlobHex {
		t.Fatal("ValidJob does not carry ValidBlob")
	}
}

// TestServerPushesJobsAndCapturesSubmissions drives the server with a raw
// websocket client: it must receive the configured jobs in order and record a
// submission the client sends, including the OnSubmit callback.
func TestServerPushesJobsAndCapturesSubmissions(t *testing.T) {
	onSubmitCh := make(chan Submission, 1)
	srv := Start(Config{
		Jobs: []Job{
			ValidJob("job-0", 1000),
			{JobID: "ka"},
			ValidJob("job-1", 1001),
		},
		PushInterval: 10 * time.Millisecond,
		OnSubmit:     func(s Submission) { onSubmitCh <- s },
	})
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(srv.URL(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	want := []string{"job-0", "ka", "job-1"}
	for i, id := range want {
		var frame getwork.Job
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if frame.JobID != id {
			t.Fatalf("frame %d JobID = %q, want %q", i, frame.JobID, id)
		}
		if id == "ka" && frame.Blockhashing_blob != "" {
			t.Fatalf("keepalive frame carried a blob: %q", frame.Blockhashing_blob)
		}
	}

	if err := conn.WriteJSON(getwork.Submit{JobID: "job-1", Blob: "deadbeef"}); err != nil {
		t.Fatalf("write submit: %v", err)
	}

	select {
	case cb := <-onSubmitCh:
		if cb.JobID != "job-1" || cb.Blob != "deadbeef" {
			t.Fatalf("OnSubmit = %+v, want job-1/deadbeef", cb)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnSubmit never fired")
	}

	subs := srv.Submissions()
	if len(subs) != 1 || subs[0].JobID != "job-1" || subs[0].Blob != "deadbeef" {
		t.Fatalf("Submissions = %+v, want one job-1/deadbeef", subs)
	}
}

// TestEmptyConfigDefaultsToOneJob pins the zero-value behavior.
func TestEmptyConfigDefaultsToOneJob(t *testing.T) {
	srv := Start(Config{})
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(srv.URL(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	var frame getwork.Job
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatalf("read: %v", err)
	}
	if frame.JobID != "job-0" {
		t.Fatalf("JobID = %q, want job-0", frame.JobID)
	}
	if frame.Difficultyuint64 != 1000 {
		t.Fatalf("Difficultyuint64 = %d, want 1000", frame.Difficultyuint64)
	}
}

func TestServerURLShape(t *testing.T) {
	srv := Start(Config{})
	defer srv.Close()
	if !strings.HasPrefix(srv.URL(), "ws://") {
		t.Fatalf("URL() = %q, want ws:// prefix", srv.URL())
	}
	if srv.Addr() != strings.TrimPrefix(srv.URL(), "ws://") {
		t.Fatalf("URL() and Addr() disagree: %q vs %q", srv.URL(), srv.Addr())
	}
}

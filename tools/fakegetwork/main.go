// fakegetwork is a stand-in DERO getwork daemon for lifecycle testing.
//
// # WHY THIS EXISTS
//
// The four Dirtybird miners carry ~215 unit tests between them and all four CI
// pipelines are green, yet none of them exercises the miner's LIFECYCLE:
// connect, mine, submit, lose the connection, redial. Every defect found in the
// 2026-07 sweep lived in that gap -- a miner that died with SIGPIPE on any
// disconnect, a submit mailbox that destroyed ~86% of found shares, and two
// miners replaying shares from a dead session onto the next connection. Unit
// tests could not have caught any of them, because the miner never ran.
//
// This server closes that gap. It speaks enough of the protocol to drive a real
// miner through a full session and to COUNT WHAT ACTUALLY ARRIVES, which is the
// only trustworthy measure -- a miner's own counters cannot be evidence for a
// bug in its own submit path.
//
// TRAPS ENCODED HERE, each of which cost a wrong measurement to discover:
//
//   - Difficulty 1 is REJECTED. The C++ miner computes target = 2^256/difficulty
//     into 32 bytes; at difficulty 1 that needs 33 and the low 32 are all zero,
//     so check_hash rejects everything and the miner finds nothing at all. And
//     even where it works, difficulty 1 makes every hash a winner, so workers
//     regenerate stale shares faster than any queue can drain -- which inverts
//     reconnect measurements and makes a correct fix look like a regression.
//
//   - blob[0]&0xf must be 1. The Go miner enforces derohe's miniblock version
//     nibble and rejects anything else as "unsupported miniblock version"; Zig
//     and Rust do not check it and will happily mine a malformed job. A blob
//     built without this is silently refused by exactly one of the four.
//
//   - Both plaintext and TLS listeners are served. Rust and C++ dial TLS
//     unconditionally; Zig and Go accept ws://. A plaintext-only harness simply
//     cannot reach half the family. The certificate is self-signed, which is
//     what every miner expects: a real derod mints a fresh self-signed cert at
//     every start, and all four miners dial with verification disabled.
//
// Stats are written as JSON on shutdown so tests can assert on them without
// scraping logs.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// job mirrors the fields internal/getwork.Job decodes. Field names and JSON
// tags must stay in step with that type or the miner silently sees zeroes.
type job struct {
	JobID             string `json:"jobid"`
	Blockhashing_blob string `json:"blockhashing_blob"`
	Difficulty        string `json:"difficulty"`
	Difficultyuint64  uint64 `json:"difficultyuint64"`
	Height            uint64 `json:"height"`
	Blocks            uint64 `json:"blocks"`
	MiniBlocks        uint64 `json:"miniblocks"`
	Rejected          uint64 `json:"rejected"`
	LastError         string `json:"lasterror"`
}

// submit mirrors getwork.Submit.
type submit struct {
	JobID string `json:"jobid"`
	Blob  string `json:"mbl_blob"`
}

// Stats is the machine-readable result of a run. Everything here is measured
// server-side: what the miner CLAIMS it sent is not evidence when the thing
// under test is the miner's submit path.
type Stats struct {
	Upgrades  int64 `json:"upgrades"`   // completed WebSocket handshakes
	JobsSent  int64 `json:"jobs_sent"`  // job pushes written
	Submits   int64 `json:"submits"`    // submissions received
	Stale     int64 `json:"stale"`      // submissions for a jobid this connection is not issuing
	BadJSON   int64 `json:"bad_json"`   // submissions that failed to parse
	DurationS int64 `json:"duration_s"` // wall seconds served
}

type server struct {
	difficulty uint64
	jobEvery   time.Duration
	height     uint64
	verbose    bool

	upgrades atomic.Int64
	jobsSent atomic.Int64
	submits  atomic.Int64
	stale    atomic.Int64
	badJSON  atomic.Int64

	mu sync.Mutex
}

var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

// selfSigned builds a throwaway certificate. A real derod mints a fresh
// self-signed ECDSA P-256 cert at every daemon start with no subject and no
// SANs, and every miner in this family dials with verification disabled -- so
// this is exactly the shape they expect. Do not "improve" it into a CA-valid
// cert: that would make the harness less representative, not more.
func selfSigned() tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatalf("keygen: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		log.Fatalf("cert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// newBlob returns a 48-byte miniblock blob with a VALID version nibble.
// blob[0]&0xf must equal 1 or the Go miner refuses the job outright.
func newBlob() []byte {
	b := make([]byte, 48)
	for i := range b {
		b[i] = byte(i)
	}
	b[0] = 0x01
	return b
}

func (s *server) handle(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade failed: %v", err)
		return
	}
	defer c.Close()
	s.upgrades.Add(1)
	log.Printf("miner connected: %s", r.URL.Path)

	// The jobid this connection is currently issuing. A submission carrying
	// anything else was found against a job THIS session never sent -- i.e. it
	// survived a reconnect, which is the defect class C3 tests for.
	var current atomic.Value
	current.Store("")

	blob := newBlob()
	hexBlob := hex.EncodeToString(blob)

	// Submissions are read by a DEDICATED goroutine with no read deadline.
	//
	// The obvious shape -- a short SetReadDeadline + ReadMessage poll wedged
	// into the job-push loop -- silently counts ZERO. gorilla puts a connection
	// into a permanent failed state after any read error, and a 5ms deadline
	// expires long before the first submission arrives, so every later read
	// fails instantly. That produced a run where the miner reported 2269
	// submissions and this server recorded 0: exactly the false-negative the
	// README warns about, in the tool meant to detect it.
	//
	// gorilla permits one concurrent reader and one concurrent writer, so a
	// reader here plus the job writer below is the supported arrangement.
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			s.submits.Add(1)
			var sub submit
			if json.Unmarshal(msg, &sub) != nil {
				s.badJSON.Add(1)
				continue
			}
			if live, _ := current.Load().(string); sub.JobID != live {
				n := s.stale.Add(1)
				if s.verbose {
					log.Printf("  STALE SUBMIT #%d jobid=%s (issuing %s)", n, sub.JobID, live)
				}
			}
		}
	}()

	tick := time.NewTicker(s.jobEvery)
	defer tick.Stop()

	for range tick.C {
		s.mu.Lock()
		s.height++
		h := s.height
		s.mu.Unlock()

		j := job{
			JobID:             fmt.Sprintf("%d.0.0", time.Now().UnixNano()),
			Blockhashing_blob: hexBlob,
			Difficulty:        fmt.Sprintf("%d", s.difficulty),
			Difficultyuint64:  s.difficulty,
			Height:            h,
			Blocks:            93,
			MiniBlocks:        962,
			Rejected:          25,
		}
		payload, _ := json.Marshal(j)
		if err := c.WriteMessage(websocket.TextMessage, payload); err != nil {
			log.Printf("write failed (miner gone?): %v", err)
			return
		}
		s.jobsSent.Add(1)
		current.Store(j.JobID)

		// Stop pushing once the reader has seen the connection die, rather than
		// waiting for the next write to fail.
		select {
		case <-readerDone:
			return
		default:
		}
	}
}

func main() {
	var (
		plainAddr = flag.String("plain", ":31500", "plaintext ws:// listen address (Zig, Go)")
		tlsAddr   = flag.String("tls", ":31501", "TLS wss:// listen address (Rust, C++ -- they dial TLS unconditionally)")
		diff      = flag.Uint64("difficulty", 20, "job difficulty; 20 floods shares, ~8000 spaces them ~3s apart")
		jobMS     = flag.Int("job-ms", 500, "milliseconds between job pushes (derod pushes ~every 500ms)")
		height    = flag.Uint64("height", 7368356, "starting block height")
		statsPath = flag.String("stats", "", "write run stats as JSON to this path on shutdown")
		duration  = flag.Duration("duration", 0, "exit after this long (0 = run until signalled)")
		verbose   = flag.Bool("v", false, "log every stale submission")
	)
	flag.Parse()

	// Difficulty 1 is not a valid test setting; see the package comment. Failing
	// loudly here beats a run that silently measures nothing.
	if *diff < 2 {
		fmt.Fprintln(os.Stderr,
			"difficulty must be >= 2: at 1 the C++ miner's target computation\n"+
				"overflows to all-zero and it finds NO shares, and every hash winning\n"+
				"makes workers outrun any submit queue, which inverts reconnect results.")
		os.Exit(2)
	}

	s := &server{
		difficulty: *diff,
		jobEvery:   time.Duration(*jobMS) * time.Millisecond,
		height:     *height,
		verbose:    *verbose,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/", s.handle)

	go func() {
		srv := &http.Server{
			Addr:      *tlsAddr,
			Handler:   mux,
			TLSConfig: &tls.Config{Certificates: []tls.Certificate{selfSigned()}},
		}
		log.Printf("fakegetwork TLS  listening on %s", *tlsAddr)
		if err := srv.ListenAndServeTLS("", ""); err != nil {
			log.Printf("tls listener: %v", err)
		}
	}()

	go func() {
		log.Printf("fakegetwork plain listening on %s (difficulty %d, job every %v)",
			*plainAddr, *diff, s.jobEvery)
		if err := http.ListenAndServe(*plainAddr, mux); err != nil {
			log.Printf("plain listener: %v", err)
		}
	}()

	started := time.Now()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	if *duration > 0 {
		select {
		case <-sig:
		case <-time.After(*duration):
		}
	} else {
		<-sig
	}

	st := Stats{
		Upgrades:  s.upgrades.Load(),
		JobsSent:  s.jobsSent.Load(),
		Submits:   s.submits.Load(),
		Stale:     s.stale.Load(),
		BadJSON:   s.badJSON.Load(),
		DurationS: int64(time.Since(started).Seconds()),
	}
	out, _ := json.MarshalIndent(st, "", "  ")
	fmt.Println(string(out))
	if *statsPath != "" {
		if err := os.WriteFile(*statsPath, out, 0o644); err != nil {
			log.Printf("stats write: %v", err)
		}
	}
}

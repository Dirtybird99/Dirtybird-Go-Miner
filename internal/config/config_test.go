package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileIsNil(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if f != nil || err != nil {
		t.Fatalf("got %v, %v; want nil, nil", f, err)
	}
}

func TestLoadMalformedIsError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte("{oops"), 0o644)
	if _, err := Load(p); err == nil {
		t.Fatal("want error for malformed JSON")
	}
}

func TestLoadPartialAndUnknownKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"daemon-address":"host:1234","lock-threads":true,"period":10}`), 0o644)
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if f.DaemonAddress == nil || *f.DaemonAddress != "host:1234" {
		t.Fatalf("daemon-address = %v", f.DaemonAddress)
	}
	if f.Wallet != nil || f.Threads != nil {
		t.Fatalf("absent keys must stay nil, got wallet=%v threads=%v", f.Wallet, f.Threads)
	}
}

func TestLoadAllKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"daemon-address":"h:1","wallet":"dero1qx","threads":12}`), 0o644)
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if *f.DaemonAddress != "h:1" || *f.Wallet != "dero1qx" || *f.Threads != 12 {
		t.Fatalf("got %+v", f)
	}
}

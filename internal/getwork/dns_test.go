package getwork

import (
	"context"
	"errors"
	"net"
	"testing"
)

func ipAddrs(ips ...string) []net.IPAddr {
	out := make([]net.IPAddr, 0, len(ips))
	for _, s := range ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out
}

func TestResolveHostSystemFirst(t *testing.T) {
	fallbackCalled := false
	lookups := []lookupFunc{
		func(ctx context.Context, host string) ([]net.IPAddr, error) {
			return ipAddrs("192.0.2.1"), nil
		},
		func(ctx context.Context, host string) ([]net.IPAddr, error) {
			fallbackCalled = true
			return ipAddrs("192.0.2.2"), nil
		},
	}
	addrs, err := resolveHost(context.Background(), "pool.example", lookups)
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 1 || addrs[0].IP.String() != "192.0.2.1" {
		t.Fatalf("addrs = %v", addrs)
	}
	if fallbackCalled {
		t.Fatal("fallback must not run when the system resolver answers")
	}
}

func TestResolveHostFallsBack(t *testing.T) {
	sysErr := errors.New("lookup pool.example on 127.0.0.1:53: connection refused")
	lookups := []lookupFunc{
		func(ctx context.Context, host string) ([]net.IPAddr, error) {
			return nil, sysErr
		},
		func(ctx context.Context, host string) ([]net.IPAddr, error) {
			return ipAddrs("10.0.0.1"), nil
		},
	}
	addrs, err := resolveHost(context.Background(), "pool.example", lookups)
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 1 || addrs[0].IP.String() != "10.0.0.1" {
		t.Fatalf("addrs = %v", addrs)
	}
}

func TestResolveHostAllFail(t *testing.T) {
	first := errors.New("system resolver down")
	second := errors.New("public resolver down")
	lookups := []lookupFunc{
		func(ctx context.Context, host string) ([]net.IPAddr, error) { return nil, first },
		func(ctx context.Context, host string) ([]net.IPAddr, error) { return nil, second },
	}
	_, err := resolveHost(context.Background(), "pool.example", lookups)
	if !errors.Is(err, first) {
		t.Fatalf("want the first (system) error back, got %v", err)
	}

	// Success with zero addresses is also a failure, not a nil slice win.
	empty := []lookupFunc{
		func(ctx context.Context, host string) ([]net.IPAddr, error) { return nil, nil },
	}
	_, err = resolveHost(context.Background(), "pool.example", empty)
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Fatalf("want a DNSError for empty results, got %v", err)
	}
}

// TestDialFallbackIPLiteral runs offline: an IP-literal address must dial
// directly with no resolution attempt.
func TestDialFallbackIPLiteral(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			conn.Close()
		}
	}()
	conn, err := dialContextDNSFallback(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	if _, err := dialContextDNSFallback(context.Background(), "tcp", "no-port-here"); err == nil {
		t.Fatal("malformed address must error")
	}
}

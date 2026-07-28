package getwork

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
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

// The IPv6 zone must survive formatting — ip.IP.String() drops it, which
// broke link-local endpoints when NetDialContext replaced the default dialer.
func TestJoinIPPortPreservesZone(t *testing.T) {
	zoned := net.IPAddr{IP: net.ParseIP("fe80::1"), Zone: "eth0"}
	if got := joinIPPort(zoned, "10300"); got != "[fe80::1%eth0]:10300" {
		t.Fatalf("zoned = %q, want [fe80::1%%eth0]:10300", got)
	}
	plain := net.IPAddr{IP: net.ParseIP("192.0.2.7")}
	if got := joinIPPort(plain, "10300"); got != "192.0.2.7:10300" {
		t.Fatalf("plain = %q", got)
	}
}

// The resolver retries over "tcp" after a truncated UDP answer; the fallback
// dial hook must honor that instead of forcing UDP.
func TestFallbackDialHonorsNetwork(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- struct{}{}
			conn.Close()
		}
	}()

	dial := fallbackDial(ln.Addr().String())
	conn, err := dial(context.Background(), "tcp", "ignored:53")
	if err != nil {
		t.Fatalf("tcp dial: %v", err)
	}
	if got := conn.RemoteAddr().Network(); got != "tcp" {
		t.Fatalf("network = %q, want tcp", got)
	}
	conn.Close()
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("TCP listener never saw the connection")
	}

	udpConn, err := dial(context.Background(), "udp", "ignored:53")
	if err != nil {
		t.Fatalf("udp dial: %v", err)
	}
	defer udpConn.Close()
	if got := udpConn.RemoteAddr().Network(); got != "udp" {
		t.Fatalf("network = %q, want udp", got)
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

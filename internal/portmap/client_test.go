package portmap

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// fakeMapper is a mapper this test fully controls, for exercising Client's
// orchestration logic (renewal cadence, Close's unmap, ExternalPort
// tracking) without any real network protocol involved.
type fakeMapper struct {
	addCalls    atomic.Int32
	deleteCalls atomic.Int32
	grantPort   atomic.Uint32
}

func (f *fakeMapper) name() string { return "fake" }

func (f *fakeMapper) addMapping(ctx context.Context, protocol string, internalPort uint16, lease time.Duration) (Mapping, error) {
	f.addCalls.Add(1)
	port := f.grantPort.Load()
	if port == 0 {
		port = uint32(internalPort)
	}
	return Mapping{ExternalIP: net.IPv4(203, 0, 113, 1), ExternalPort: uint16(port), Protocol: protocol}, nil
}

func (f *fakeMapper) deleteMapping(ctx context.Context, protocol string, internalPort, externalPort uint16) error {
	f.deleteCalls.Add(1)
	return nil
}

// newTestClient builds a Client around a fake mapper with a short renew
// interval, bypassing Start's real discovery entirely.
func newTestClient(m *fakeMapper, renewInterval time.Duration) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		m: m, protocol: "TCP", internalPort: 6881,
		renewInterval: renewInterval,
		cancel:        cancel, done: make(chan struct{}),
	}
	c.externalPort.Store(6881)
	go c.renewLoop(ctx)
	return c
}

func TestClientRenewsOnItsInterval(t *testing.T) {
	m := &fakeMapper{}
	c := newTestClient(m, 10*time.Millisecond)
	defer c.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m.addCalls.Load() >= 3 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("addMapping was called %d times in 2s at a 10ms interval, want at least 3", m.addCalls.Load())
}

func TestClientRenewalUpdatesExternalPort(t *testing.T) {
	m := &fakeMapper{}
	m.grantPort.Store(7000)
	c := newTestClient(m, 10*time.Millisecond)
	defer c.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.ExternalPort() == 7000 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("ExternalPort() = %d after renewal, want 7000", c.ExternalPort())
}

func TestClientCloseWithdrawsTheMapping(t *testing.T) {
	m := &fakeMapper{}
	// A long interval so Close's own unmap is what's being tested, not an
	// incidental renewal tick landing at the same time.
	c := newTestClient(m, time.Hour)

	c.Close()

	if m.deleteCalls.Load() != 1 {
		t.Fatalf("deleteMapping was called %d times, want exactly 1", m.deleteCalls.Load())
	}
}

func TestClientCloseIsIdempotent(t *testing.T) {
	m := &fakeMapper{}
	c := newTestClient(m, time.Hour)
	c.Close()
	c.Close() // must not panic or block forever
}

func TestClientGatewayKindReportsTheMapperName(t *testing.T) {
	c := newTestClient(&fakeMapper{}, time.Hour)
	defer c.Close()
	if got := c.GatewayKind(); got != "fake" {
		t.Fatalf("GatewayKind() = %q, want %q", got, "fake")
	}
}

// TestStartReturnsErrNoGatewayWhenNoneIsReachable exercises the real
// discovery path end to end. It assumes — as any CI environment or most
// sandboxed dev environments do — that no actual UPnP or NAT-PMP gateway is
// reachable, so both attempts fail and Start reports ErrNoGateway. On a real
// LAN with a working router this assumption does not hold and the test
// would need skipping; it is deliberately written to fail loudly rather
// than silently pass either way, so that would show up immediately.
func TestStartReturnsErrNoGatewayWhenNoneIsReachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _, err := Start(ctx, "TCP", 6881)
	if !errors.Is(err, ErrNoGateway) {
		t.Fatalf("Start returned err=%v, want ErrNoGateway", err)
	}
}

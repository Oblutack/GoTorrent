package portmap

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
)

func TestMain(m *testing.M) {
	logger.Init(false)
	m.Run()
}

// fakeNATPMPGateway plays just enough of RFC 6886's server side to exercise
// a real natPMPMapper round trip: external-address and mapping requests,
// each validated for the fields a real gateway would check, with a fixed
// external IP and a granted port equal to whatever internal port was asked
// for.
type fakeNATPMPGateway struct {
	t    *testing.T
	conn *net.UDPConn

	externalIP net.IP
	// refuseResult, if non-zero, makes every mapping request fail with this
	// result code instead of succeeding — for testing error handling.
	refuseResult uint16
}

func newFakeNATPMPGateway(t *testing.T, externalIP net.IP) *fakeNATPMPGateway {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	g := &fakeNATPMPGateway{t: t, conn: conn, externalIP: externalIP}
	t.Cleanup(func() { conn.Close() })
	go g.serve()
	return g
}

func (g *fakeNATPMPGateway) port() int {
	return g.conn.LocalAddr().(*net.UDPAddr).Port
}

func (g *fakeNATPMPGateway) serve() {
	buf := make([]byte, 16)
	for {
		n, addr, err := g.conn.ReadFromUDP(buf)
		if err != nil {
			return // closed
		}
		req := append([]byte(nil), buf[:n]...)
		go g.handle(req, addr)
	}
}

func (g *fakeNATPMPGateway) handle(req []byte, addr *net.UDPAddr) {
	if len(req) < 2 {
		return
	}
	opcode := req[1]
	switch opcode {
	case natPMPOpExternalAddress:
		resp := make([]byte, 12)
		resp[1] = 0x80
		copy(resp[8:12], g.externalIP.To4())
		g.conn.WriteToUDP(resp, addr)

	case natPMPOpMapUDP, natPMPOpMapTCP:
		if len(req) < 12 {
			return
		}
		internalPort := binary.BigEndian.Uint16(req[4:6])
		lifetime := binary.BigEndian.Uint32(req[8:12])

		resp := make([]byte, 16)
		resp[1] = opcode | 0x80
		if g.refuseResult != 0 {
			binary.BigEndian.PutUint16(resp[2:4], g.refuseResult)
			g.conn.WriteToUDP(resp, addr)
			return
		}
		binary.BigEndian.PutUint16(resp[8:10], internalPort)
		if lifetime == 0 {
			// Deletion: RFC 6886 grants a 0-lifetime response.
			binary.BigEndian.PutUint16(resp[10:12], 0)
		} else {
			binary.BigEndian.PutUint16(resp[10:12], internalPort) // grant the same port asked for
			binary.BigEndian.PutUint32(resp[12:16], lifetime)
		}
		g.conn.WriteToUDP(resp, addr)
	}
}

func testMapper(t *testing.T, gw *fakeNATPMPGateway) *natPMPMapper {
	t.Helper()
	return &natPMPMapper{gateway: net.IPv4(127, 0, 0, 1), port: gw.port()}
}

func TestNATPMPAddMapping(t *testing.T) {
	gw := newFakeNATPMPGateway(t, net.IPv4(203, 0, 113, 9))
	m := testMapper(t, gw)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mapping, err := m.addMapping(ctx, "TCP", 6881, time.Hour)
	if err != nil {
		t.Fatalf("addMapping: %v", err)
	}
	if mapping.ExternalPort != 6881 {
		t.Fatalf("ExternalPort = %d, want 6881", mapping.ExternalPort)
	}
	if !mapping.ExternalIP.Equal(net.IPv4(203, 0, 113, 9)) {
		t.Fatalf("ExternalIP = %s, want 203.0.113.9", mapping.ExternalIP)
	}
	if mapping.Protocol != "TCP" {
		t.Fatalf("Protocol = %q, want TCP", mapping.Protocol)
	}
}

func TestNATPMPAddMappingSurfacesRefusal(t *testing.T) {
	gw := newFakeNATPMPGateway(t, net.IPv4(203, 0, 113, 9))
	gw.refuseResult = 3 // "network failure", per RFC 6886's result codes
	m := testMapper(t, gw)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := m.addMapping(ctx, "TCP", 6881, time.Hour); err == nil {
		t.Fatal("addMapping succeeded despite the gateway refusing, want an error")
	}
}

func TestNATPMPDeleteMapping(t *testing.T) {
	gw := newFakeNATPMPGateway(t, net.IPv4(203, 0, 113, 9))
	m := testMapper(t, gw)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := m.addMapping(ctx, "TCP", 6881, time.Hour); err != nil {
		t.Fatalf("addMapping: %v", err)
	}
	if err := m.deleteMapping(ctx, "TCP", 6881, 6881); err != nil {
		t.Fatalf("deleteMapping: %v", err)
	}
}

func TestNATPMPUnreachableGatewayTimesOutQuickly(t *testing.T) {
	// Nothing is listening on this loopback port.
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("binding a throwaway socket: %v", err)
	}
	deadPort := probe.LocalAddr().(*net.UDPAddr).Port
	probe.Close()

	m := &natPMPMapper{gateway: net.IPv4(127, 0, 0, 1), port: deadPort}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := m.addMapping(ctx, "TCP", 6881, time.Hour); err == nil {
		t.Fatal("addMapping to a dead gateway succeeded, want an error")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("addMapping to a dead gateway took %s, want it to give up well under 10s", elapsed)
	}
}

func TestGuessGatewayReturnsAPrivateLookingIPv4(t *testing.T) {
	gw, err := guessGateway()
	if err != nil {
		t.Fatalf("guessGateway: %v", err)
	}
	if gw.To4() == nil {
		t.Fatalf("guessGateway returned a non-IPv4 address: %s", gw)
	}
	if gw[len(gw)-1] != 1 {
		t.Fatalf("guessGateway did not end in .1: %s", gw)
	}
}

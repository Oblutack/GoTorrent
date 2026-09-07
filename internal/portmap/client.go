package portmap

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Oblutack/GoTorrent/internal/logger"
)

// renewBefore is how far ahead of leaseDuration Client renews a mapping —
// well before a lease could actually lapse, so a slow renewal round trip or
// one missed cycle (a transient network blip, say) still leaves margin
// before the mapping itself expires.
const renewBefore = 10 * time.Minute

// Client holds one active port mapping alive for as long as it runs,
// renewing it on a timer and withdrawing it on Close.
type Client struct {
	m            mapper
	protocol     string
	internalPort uint16
	externalPort atomic.Uint32
	// renewInterval is leaseDuration-renewBefore in production; tests
	// override it to something short rather than waiting on a real 50
	// minutes to prove renewal actually happens.
	renewInterval time.Duration

	cancel context.CancelFunc
	done   chan struct{}
}

// Start discovers a gateway — trying UPnP first, then NAT-PMP — and maps
// internalPort for protocol ("TCP" or "UDP"), then keeps renewing that
// mapping in the background until ctx is cancelled or Close is called. It
// returns the mapping actually granted; a caller must use Mapping's
// ExternalPort, not the port it asked for, since a gateway is free to grant
// a different one. If neither protocol finds a usable gateway, it returns
// ErrNoGateway — not fatal to the caller, since inbound connections still
// work fine on a network without any NAT to traverse, or with the port
// forwarded by hand.
func Start(ctx context.Context, protocol string, internalPort uint16) (*Client, Mapping, error) {
	m, mapping, err := discoverAndMap(ctx, protocol, internalPort)
	if err != nil {
		return nil, Mapping{}, err
	}

	loopCtx, cancel := context.WithCancel(ctx)
	c := &Client{
		m: m, protocol: protocol, internalPort: internalPort,
		renewInterval: leaseDuration - renewBefore,
		cancel:        cancel, done: make(chan struct{}),
	}
	c.externalPort.Store(uint32(mapping.ExternalPort))

	go c.renewLoop(loopCtx)
	return c, mapping, nil
}

func discoverAndMap(ctx context.Context, protocol string, internalPort uint16) (mapper, Mapping, error) {
	if upnp, err := discoverUPnP(ctx); err != nil {
		logger.Logf("portmap: UPnP discovery failed: %v\n", err)
	} else if mapping, err := upnp.addMapping(ctx, protocol, internalPort, leaseDuration); err != nil {
		logger.Logf("portmap: UPnP gateway found but mapping failed: %v\n", err)
	} else {
		return upnp, mapping, nil
	}

	natpmp, err := newNATPMPMapper()
	if err != nil {
		return nil, Mapping{}, fmt.Errorf("%w (NAT-PMP: %v)", ErrNoGateway, err)
	}
	mapping, err := natpmp.addMapping(ctx, protocol, internalPort, leaseDuration)
	if err != nil {
		return nil, Mapping{}, fmt.Errorf("%w (NAT-PMP: %v)", ErrNoGateway, err)
	}
	return natpmp, mapping, nil
}

func (c *Client) renewLoop(ctx context.Context) {
	defer close(c.done)
	ticker := time.NewTicker(c.renewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := c.m.deleteMapping(delCtx, c.protocol, c.internalPort, c.ExternalPort()); err != nil {
				logger.Logf("portmap: %s: withdrawing mapping on shutdown: %v\n", c.m.name(), err)
			}
			cancel()
			return
		case <-ticker.C:
			mapping, err := c.m.addMapping(ctx, c.protocol, c.internalPort, leaseDuration)
			if err != nil {
				logger.Warning.Printf("portmap: %s: renewal failed, mapping may lapse: %v\n", c.m.name(), err)
				continue
			}
			c.externalPort.Store(uint32(mapping.ExternalPort))
		}
	}
}

// ExternalPort is the port currently mapped — the one from Start's Mapping
// until the first renewal, and whatever the gateway granted most recently
// after that (a renewal is not guaranteed to keep the same external port,
// though in practice it almost always does).
func (c *Client) ExternalPort() uint16 { return uint16(c.externalPort.Load()) }

// GatewayKind names which protocol is actually in use ("UPnP" or
// "NAT-PMP"), for logging.
func (c *Client) GatewayKind() string { return c.m.name() }

// Close withdraws the mapping and stops renewal, blocking until both are
// done. Safe to call once; safe to call from any goroutine.
func (c *Client) Close() {
	c.cancel()
	<-c.done
}

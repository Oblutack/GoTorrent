package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

const (
	socks5Version = 0x05

	socks5AuthNone         = 0x00
	socks5AuthUsernamePass = 0x02
	socks5AuthNoAcceptable = 0xff

	socks5CmdConnect = 0x01

	socks5ATYPIPv4   = 0x01
	socks5ATYPDomain = 0x03
	socks5ATYPIPv6   = 0x04
)

func (d *Dialer) dialSOCKS5(ctx context.Context, addr string) (net.Conn, error) {
	var nd net.Dialer
	conn, err := nd.DialContext(ctx, "tcp", d.cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("proxy: connecting to SOCKS5 proxy %s: %w", d.cfg.Address, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	if err := d.socks5Handshake(conn, addr); err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return conn, nil
}

func (d *Dialer) socks5Handshake(conn net.Conn, targetAddr string) error {
	methods := []byte{socks5AuthNone}
	if d.cfg.Username != "" {
		methods = []byte{socks5AuthUsernamePass}
	}
	greeting := append([]byte{socks5Version, byte(len(methods))}, methods...)
	if _, err := conn.Write(greeting); err != nil {
		return fmt.Errorf("proxy: SOCKS5 greeting: %w", err)
	}

	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("proxy: SOCKS5 greeting reply: %w", err)
	}
	if reply[0] != socks5Version {
		return fmt.Errorf("proxy: SOCKS5 proxy replied with version %d", reply[0])
	}
	switch reply[1] {
	case socks5AuthNone:
	case socks5AuthUsernamePass:
		if d.cfg.Username == "" {
			return errors.New("proxy: SOCKS5 proxy requires authentication, none configured")
		}
		if err := socks5Authenticate(conn, d.cfg.Username, d.cfg.Password); err != nil {
			return err
		}
	case socks5AuthNoAcceptable:
		return errors.New("proxy: SOCKS5 proxy rejected every offered authentication method")
	default:
		return fmt.Errorf("proxy: SOCKS5 proxy selected an unsupported auth method %d", reply[1])
	}

	return d.socks5Connect(conn, targetAddr)
}

func socks5Authenticate(conn net.Conn, username, password string) error {
	if len(username) > 255 || len(password) > 255 {
		return errors.New("proxy: SOCKS5 username/password must each be under 256 bytes")
	}
	req := []byte{0x01, byte(len(username))}
	req = append(req, username...)
	req = append(req, byte(len(password)))
	req = append(req, password...)
	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("proxy: SOCKS5 auth request: %w", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("proxy: SOCKS5 auth reply: %w", err)
	}
	if reply[1] != 0x00 {
		return errors.New("proxy: SOCKS5 authentication failed")
	}
	return nil
}

func (d *Dialer) socks5Connect(conn net.Conn, targetAddr string) error {
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return fmt.Errorf("proxy: bad target address %q: %w", targetAddr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return fmt.Errorf("proxy: bad target port in %q", targetAddr)
	}

	req := []byte{socks5Version, socks5CmdConnect, 0x00}
	switch {
	case d.cfg.ProxyDNS && net.ParseIP(host) == nil:
		// Domain-name addressing: let the proxy resolve, so the query
		// never leaves this machine outside the proxy tunnel.
		if len(host) > 255 {
			return fmt.Errorf("proxy: hostname %q too long for SOCKS5", host)
		}
		req = append(req, socks5ATYPDomain, byte(len(host)))
		req = append(req, host...)
	default:
		ip := net.ParseIP(host)
		if ip == nil {
			resolved, err := net.ResolveIPAddr("ip", host)
			if err != nil {
				return fmt.Errorf("proxy: resolving %s locally: %w", host, err)
			}
			ip = resolved.IP
		}
		if ip4 := ip.To4(); ip4 != nil {
			req = append(req, socks5ATYPIPv4)
			req = append(req, ip4...)
		} else {
			req = append(req, socks5ATYPIPv6)
			req = append(req, ip.To16()...)
		}
	}
	req = append(req, byte(port>>8), byte(port))

	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("proxy: SOCKS5 connect request: %w", err)
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return fmt.Errorf("proxy: SOCKS5 connect reply: %w", err)
	}
	if header[0] != socks5Version {
		return fmt.Errorf("proxy: SOCKS5 proxy replied with version %d", header[0])
	}
	if header[1] != 0x00 {
		return fmt.Errorf("proxy: SOCKS5 CONNECT to %s failed: %s", targetAddr, socks5ReplyError(header[1]))
	}

	var addrLen int
	switch header[3] {
	case socks5ATYPIPv4:
		addrLen = net.IPv4len
	case socks5ATYPIPv6:
		addrLen = net.IPv6len
	case socks5ATYPDomain:
		lenByte := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenByte); err != nil {
			return fmt.Errorf("proxy: SOCKS5 connect reply address: %w", err)
		}
		addrLen = int(lenByte[0])
	default:
		return fmt.Errorf("proxy: SOCKS5 connect reply has an unknown address type %d", header[3])
	}
	// The bound address + port that follows is unneeded here; discard it.
	if _, err := io.CopyN(io.Discard, conn, int64(addrLen+2)); err != nil {
		return fmt.Errorf("proxy: SOCKS5 connect reply address: %w", err)
	}
	return nil
}

func socks5ReplyError(code byte) string {
	switch code {
	case 0x01:
		return "general SOCKS server failure"
	case 0x02:
		return "connection not allowed by ruleset"
	case 0x03:
		return "network unreachable"
	case 0x04:
		return "host unreachable"
	case 0x05:
		return "connection refused"
	case 0x06:
		return "TTL expired"
	case 0x07:
		return "command not supported"
	case 0x08:
		return "address type not supported"
	default:
		return fmt.Sprintf("unknown error code %d", code)
	}
}

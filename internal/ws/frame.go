package ws

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
)

// maxFramePayload bounds a single frame's payload, and (via maxMessage in
// conn.go) a whole reassembled message — this is a control-API push
// stream, not a file transfer protocol; nothing this project ever sends or
// expects to receive over it should approach this size. Guards against a
// malicious or buggy peer claiming an enormous length and exhausting
// memory before any of that data has even been read.
const maxFramePayload = 1 << 20 // 1 MiB

// frame is one parsed WebSocket frame (RFC 6455 section 5.2).
type frame struct {
	fin     bool
	opcode  Opcode
	payload []byte
}

// readFrame parses exactly one frame from r. requireMask is the reader's
// own RFC 6455 section 5.1 role: a server reading from a client requires
// true (a client frame MUST be masked), while a client reading from a
// server requires false (a server MUST NOT mask what it sends, and RFC
// 6455 section 5.1 says a client MUST close the connection upon receiving
// a masked frame from the server — readFrame enforces that strictly in
// both directions rather than leniently accepting either). A masked frame
// is unmasked before returning, so every other piece of this package only
// ever deals in plain bytes regardless of which role read it.
func readFrame(r io.Reader, requireMask bool) (frame, error) {
	var head [2]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return frame{}, err
	}

	fin := head[0]&0x80 != 0
	rsv := head[0] & 0x70
	opcode := Opcode(head[0] & 0x0F)
	if rsv != 0 {
		return frame{}, fmt.Errorf("ws: reserved bits set (%#x) but no extension negotiated", rsv)
	}

	masked := head[1]&0x80 != 0
	if masked != requireMask {
		if requireMask {
			return frame{}, fmt.Errorf("ws: client frame is not masked")
		}
		return frame{}, fmt.Errorf("ws: server frame is masked")
	}
	length := uint64(head[1] & 0x7F)

	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return frame{}, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return frame{}, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if length > maxFramePayload {
		return frame{}, fmt.Errorf("ws: frame payload %d exceeds the %d byte limit", length, maxFramePayload)
	}
	if opcode.isControl() && (length > 125 || !fin) {
		return frame{}, fmt.Errorf("ws: control frame (opcode %#x) must be unfragmented and <=125 bytes, got fin=%v length=%d", opcode, fin, length)
	}

	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(r, maskKey[:]); err != nil {
			return frame{}, err
		}
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return frame{}, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}

	return frame{fin: fin, opcode: opcode, payload: payload}, nil
}

// writeFrame serializes and writes one frame to w. mask is the writer's
// own RFC 6455 section 5.1 role: a server MUST NOT mask frames it sends
// (mask=false, the only case this package needed before it could dial as
// a client too), while a client MUST mask every frame it sends (mask=true)
// — this is the entire reason readFrame and writeFrame are not the same
// function despite the format otherwise being symmetric. Masking is not a
// security mechanism (RFC 6455 itself only requires it to keep
// naively-caching intermediary proxies from misinterpreting client traffic
// as cacheable HTTP), so the mask key is unpredictable but doesn't need to
// be cryptographically unguessable — crypto/rand is used anyway since it's
// a cheap 4-byte read and avoids adding a second, harder-to-justify
// math/rand use site to this codebase's own gosec G404 exclusion list.
func writeFrame(w io.Writer, fin bool, opcode Opcode, payload []byte, mask bool) error {
	if len(payload) > maxFramePayload {
		return fmt.Errorf("ws: payload %d exceeds the %d byte limit", len(payload), maxFramePayload)
	}

	var head [10]byte
	n := 2
	if fin {
		head[0] = 0x80
	}
	head[0] |= byte(opcode)

	switch {
	case len(payload) < 126:
		head[1] = byte(len(payload))
	case len(payload) <= 0xFFFF:
		head[1] = 126
		binary.BigEndian.PutUint16(head[2:4], uint16(len(payload)))
		n = 4
	default:
		head[1] = 127
		binary.BigEndian.PutUint64(head[2:10], uint64(len(payload)))
		n = 10
	}

	if mask {
		head[1] |= 0x80
	}
	if _, err := w.Write(head[:n]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}

	if !mask {
		_, err := w.Write(payload)
		return err
	}

	var maskKey [4]byte
	if _, err := rand.Read(maskKey[:]); err != nil {
		return fmt.Errorf("ws: generating mask key: %w", err)
	}
	if _, err := w.Write(maskKey[:]); err != nil {
		return err
	}
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ maskKey[i%4]
	}
	_, err := w.Write(masked)
	return err
}

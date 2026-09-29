package mse

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"time"
)

// handshakeTimeout bounds the whole negotiation (both directions), set as
// a single net.Conn deadline at the start and cleared at the end — the
// same shape internal/peer's own classic-handshake code already uses,
// just under this package's own name since the two packages share no
// constants.
const handshakeTimeout = 10 * time.Second

// CryptoMethod is a crypto_provide/crypto_select bitmask value.
type CryptoMethod uint32

const (
	CryptoPlaintext CryptoMethod = 1 << 0
	CryptoRC4       CryptoMethod = 1 << 1
)

// CryptoSelector picks one method from the set an initiator offered
// (crypto_provide) — called by the receiving side of a handshake.
type CryptoSelector func(offered CryptoMethod) (CryptoMethod, error)

// DefaultSelector prefers RC4 — the whole point of encryption is
// obfuscation — and falls back to plaintext only if the peer didn't offer
// RC4 at all.
func DefaultSelector(offered CryptoMethod) (CryptoMethod, error) {
	if offered&CryptoRC4 != 0 {
		return CryptoRC4, nil
	}
	if offered&CryptoPlaintext != 0 {
		return CryptoPlaintext, nil
	}
	return 0, errors.New("mse: peer offered no crypto method we understand")
}

const (
	maxPad = 512
	vcLen  = 8
	maxIA  = 65535
)

var vc = make([]byte, vcLen) // 8 zero bytes, per spec

// InitiateHandshake performs the "A" (initiator) side of an MSE/PE
// handshake on conn, which must be a freshly dialed, otherwise untouched
// connection — nothing written or read yet. skey is the torrent's raw
// 20-byte infohash. provide is the set of methods we're willing to use.
// ia, if non-nil, is bundled as the optional initial-payload field to
// save a round trip — this package's own production callers pass nil
// (see doc.go for why), but the parameter exists so tests can exercise
// the interop-critical "peer bundled its handshake" path the receiver
// side must still handle correctly for real peers that do bundle it.
//
// On success it returns a net.Conn ready for the caller's own (classic
// BitTorrent) handshake to proceed over, and the negotiated method.
func InitiateHandshake(conn net.Conn, skey []byte, provide CryptoMethod, ia []byte) (net.Conn, CryptoMethod, error) {
	if len(skey) != 20 {
		return nil, 0, fmt.Errorf("mse: skey must be 20 bytes, got %d", len(skey))
	}
	if len(ia) > maxIA {
		return nil, 0, fmt.Errorf("mse: IA too large (%d bytes, max %d)", len(ia), maxIA)
	}
	if err := conn.SetDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		return nil, 0, err
	}
	defer conn.SetDeadline(time.Time{})

	kp, err := generateKeypair()
	if err != nil {
		return nil, 0, fmt.Errorf("mse: generating keypair: %w", err)
	}
	padA, err := randomPad(maxPad)
	if err != nil {
		return nil, 0, err
	}
	if _, err := conn.Write(append(encodeFixed(kp.pub), padA...)); err != nil {
		return nil, 0, fmt.Errorf("mse: sending Ya: %w", err)
	}

	br := bufio.NewReader(conn)
	ybBytes := make([]byte, dhByteLen)
	if _, err := io.ReadFull(br, ybBytes); err != nil {
		return nil, 0, fmt.Errorf("mse: reading Yb: %w", err)
	}
	peerPub := new(big.Int).SetBytes(ybBytes)
	s := sharedSecret(kp.priv, peerPub)

	kA, kB := keyA(s, skey), keyB(s, skey)
	encryptCipher, err := newCipher(kA)
	if err != nil {
		return nil, 0, err
	}
	decryptCipher, err := newCipher(kB)
	if err != nil {
		return nil, 0, err
	}

	padC, err := randomPad(maxPad)
	if err != nil {
		return nil, 0, err
	}
	var payload bytes.Buffer
	payload.Write(vc)
	writeUint32(&payload, uint32(provide))
	writeUint16(&payload, uint16(len(padC)))
	payload.Write(padC)
	writeUint16(&payload, uint16(len(ia)))
	payload.Write(ia)

	encrypted := make([]byte, payload.Len())
	encryptCipher.XORKeyStream(encrypted, payload.Bytes())

	var out bytes.Buffer
	out.Write(req1(s))
	out.Write(req2Xor3(skey, s))
	out.Write(encrypted)
	if _, err := conn.Write(out.Bytes()); err != nil {
		return nil, 0, fmt.Errorf("mse: sending packet 3: %w", err)
	}

	// Locate packet 4's VC by searching for the exact ciphertext pattern
	// it will appear as — computed by feeding 8 zero bytes through the
	// very cipher instance that will go on decrypting the rest of packet
	// 4, so its keystream position is correctly consumed and aligned by
	// the time the search below finds the matching bytes on the wire.
	eVC := make([]byte, vcLen)
	decryptCipher.XORKeyStream(eVC, vc)
	if err := readUntil(br, eVC, maxPad+vcLen); err != nil {
		return nil, 0, fmt.Errorf("mse: could not locate peer's VC: %w", err)
	}

	rest := make([]byte, 4+2) // crypto_select + len(PadD)
	if _, err := io.ReadFull(br, rest); err != nil {
		return nil, 0, fmt.Errorf("mse: reading crypto_select: %w", err)
	}
	decryptCipher.XORKeyStream(rest, rest)
	selected := CryptoMethod(binary.BigEndian.Uint32(rest[:4]))
	padDLen := int(binary.BigEndian.Uint16(rest[4:6]))
	if padDLen > 0 {
		padD := make([]byte, padDLen)
		if _, err := io.ReadFull(br, padD); err != nil {
			return nil, 0, fmt.Errorf("mse: reading PadD: %w", err)
		}
		decryptCipher.XORKeyStream(padD, padD)
	}

	if selected != CryptoPlaintext && selected != CryptoRC4 {
		return nil, 0, fmt.Errorf("mse: peer selected an invalid crypto method %#x", uint32(selected))
	}
	if selected&provide == 0 {
		return nil, 0, fmt.Errorf("mse: peer selected method %#x we did not offer (%#x)", uint32(selected), uint32(provide))
	}

	wrapped := &Conn{Conn: conn, r: br}
	if selected == CryptoRC4 {
		wrapped.encrypt, wrapped.decrypt = encryptCipher, decryptCipher
	}
	return wrapped, selected, nil
}

// ReceiveHandshake performs the "B" (receiver) side. r must already be
// wrapping conn — the caller may have peeked bytes off it via
// LooksLikeHandshake before deciding to call this at all, and those
// peeked-but-unconsumed bytes are exactly what this function needs to see
// too, which is why it takes the caller's own *bufio.Reader rather than
// building a fresh one that would silently miss them. skeys is every
// candidate infohash this receiver currently manages, tried in order
// until one matches; selector picks crypto_select from whatever the
// initiator offered (nil uses DefaultSelector).
//
// On success it returns a net.Conn ready for the caller's own classic
// handshake read to proceed over (transparently yielding any IA the
// initiator bundled first), the negotiated method, and the matched SKEY.
func ReceiveHandshake(conn net.Conn, r *bufio.Reader, skeys [][]byte, selector CryptoSelector) (net.Conn, CryptoMethod, []byte, error) {
	if selector == nil {
		selector = DefaultSelector
	}
	if err := conn.SetDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		return nil, 0, nil, err
	}
	defer conn.SetDeadline(time.Time{})

	kp, err := generateKeypair()
	if err != nil {
		return nil, 0, nil, fmt.Errorf("mse: generating keypair: %w", err)
	}
	padB, err := randomPad(maxPad)
	if err != nil {
		return nil, 0, nil, err
	}
	if _, err := conn.Write(append(encodeFixed(kp.pub), padB...)); err != nil {
		return nil, 0, nil, fmt.Errorf("mse: sending Yb: %w", err)
	}

	yaBytes := make([]byte, dhByteLen)
	if _, err := io.ReadFull(r, yaBytes); err != nil {
		return nil, 0, nil, fmt.Errorf("mse: reading Ya: %w", err)
	}
	peerPub := new(big.Int).SetBytes(yaBytes)
	s := sharedSecret(kp.priv, peerPub)

	if err := readUntil(r, req1(s), maxPad+20); err != nil {
		return nil, 0, nil, fmt.Errorf("mse: could not locate req1: %w", err)
	}

	gotXor := make([]byte, 20)
	if _, err := io.ReadFull(r, gotXor); err != nil {
		return nil, 0, nil, fmt.Errorf("mse: reading req2^req3: %w", err)
	}

	var matched []byte
	for _, cand := range skeys {
		if bytes.Equal(req2Xor3(cand, s), gotXor) {
			matched = cand
			break
		}
	}
	if matched == nil {
		return nil, 0, nil, errors.New("mse: no known infohash matches this connection's SKEY")
	}

	kA, kB := keyA(s, matched), keyB(s, matched)
	decryptCipher, err := newCipher(kA) // B decrypts A's stream with keyA
	if err != nil {
		return nil, 0, nil, err
	}
	encryptCipher, err := newCipher(kB) // B encrypts its own stream with keyB
	if err != nil {
		return nil, 0, nil, err
	}

	head := make([]byte, vcLen+4+2) // VC + crypto_provide + len(PadC)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, 0, nil, fmt.Errorf("mse: reading packet 3 header: %w", err)
	}
	decryptCipher.XORKeyStream(head, head)
	if !bytes.Equal(head[:vcLen], vc) {
		return nil, 0, nil, errors.New("mse: VC mismatch in packet 3")
	}
	provided := CryptoMethod(binary.BigEndian.Uint32(head[vcLen : vcLen+4]))
	padCLen := int(binary.BigEndian.Uint16(head[vcLen+4:]))
	if padCLen > 0 {
		padC := make([]byte, padCLen)
		if _, err := io.ReadFull(r, padC); err != nil {
			return nil, 0, nil, fmt.Errorf("mse: reading PadC: %w", err)
		}
		decryptCipher.XORKeyStream(padC, padC)
	}

	iaLenBuf := make([]byte, 2)
	if _, err := io.ReadFull(r, iaLenBuf); err != nil {
		return nil, 0, nil, fmt.Errorf("mse: reading len(IA): %w", err)
	}
	decryptCipher.XORKeyStream(iaLenBuf, iaLenBuf)
	iaLen := int(binary.BigEndian.Uint16(iaLenBuf))
	if iaLen > maxIA {
		return nil, 0, nil, fmt.Errorf("mse: IA too large (%d bytes)", iaLen)
	}
	var ia []byte
	if iaLen > 0 {
		ia = make([]byte, iaLen)
		if _, err := io.ReadFull(r, ia); err != nil {
			return nil, 0, nil, fmt.Errorf("mse: reading IA: %w", err)
		}
		decryptCipher.XORKeyStream(ia, ia)
	}

	selected, err := selector(provided)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("mse: %w", err)
	}
	if selected != CryptoPlaintext && selected != CryptoRC4 {
		return nil, 0, nil, fmt.Errorf("mse: selector returned invalid method %#x", uint32(selected))
	}

	padD, err := randomPad(maxPad)
	if err != nil {
		return nil, 0, nil, err
	}
	var reply bytes.Buffer
	reply.Write(vc)
	writeUint32(&reply, uint32(selected))
	writeUint16(&reply, uint16(len(padD)))
	reply.Write(padD)
	encReply := make([]byte, reply.Len())
	encryptCipher.XORKeyStream(encReply, reply.Bytes())
	if _, err := conn.Write(encReply); err != nil {
		return nil, 0, nil, fmt.Errorf("mse: sending packet 4: %w", err)
	}

	wrapped := &Conn{Conn: conn, r: r, pending: ia}
	if selected == CryptoRC4 {
		wrapped.encrypt, wrapped.decrypt = encryptCipher, decryptCipher
	}
	return wrapped, selected, matched, nil
}

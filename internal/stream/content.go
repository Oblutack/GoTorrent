// Package stream implements Phase 8's streaming mode: an HTTP byte-range
// server over a managed torrent's own storage, with a picker that follows
// wherever a client is actually reading rather than downloading strictly
// in the background. "Point VLC at it and watch while downloading"
// (ROADMAP.md) — no separate download-then-play step.
package stream

import (
	"context"
	"errors"
	"io"

	"github.com/Oblutack/GoTorrent/internal/torrent"
)

// Content adapts one file of a torrent into an io.ReadSeeker for
// http.ServeContent, which already implements RFC 7233 Range parsing,
// conditional requests, and content-type sniffing — no reason to hand-roll
// any of that against something the standard library already provides.
// Read reprioritizes the torrent's picker toward wherever playback
// currently is whenever it crosses into a new piece, waits for that piece
// to actually be verified, then reads the real bytes straight off disk.
type Content struct {
	ctx         context.Context
	tr          *torrent.Torrent
	fileOffset  int64 // this file's start within the torrent's flat content space
	size        int64 // this file's length
	pieceLength int64

	pos         int64
	lastBoosted int
	everBoosted bool
}

// NewContent builds a Content for one file. fileOffset/size/pieceLength are
// resolved by the caller from the torrent's real metadata (see
// filesOf in server.go) — Content itself never touches mi again.
func NewContent(ctx context.Context, tr *torrent.Torrent, fileOffset, size, pieceLength int64) *Content {
	return &Content{ctx: ctx, tr: tr, fileOffset: fileOffset, size: size, pieceLength: pieceLength}
}

func (c *Content) Seek(offset int64, whence int) (int64, error) {
	var newPos int64
	switch whence {
	case io.SeekStart:
		newPos = offset
	case io.SeekCurrent:
		newPos = c.pos + offset
	case io.SeekEnd:
		newPos = c.size + offset
	default:
		return 0, errors.New("stream: invalid whence")
	}
	if newPos < 0 {
		return 0, errors.New("stream: negative position")
	}
	c.pos = newPos
	return c.pos, nil
}

// Read reprioritizes the picker only when crossing into a new piece, not on
// every call — SetStreamPosition is a real control-channel round trip that
// recomputes every piece's priority, and http.ServeContent's own internal
// copy loop calls Read many times per piece (32 KiB at a time).
func (c *Content) Read(p []byte) (int, error) {
	if c.pos >= c.size {
		return 0, io.EOF
	}
	abs := c.fileOffset + c.pos
	pieceIndex := int(abs / c.pieceLength)
	if !c.everBoosted || pieceIndex != c.lastBoosted {
		if err := c.tr.SetStreamPosition(abs); err != nil {
			return 0, err
		}
		c.lastBoosted = pieceIndex
		c.everBoosted = true
	}
	if err := c.tr.WaitForOffset(c.ctx, abs); err != nil {
		return 0, err
	}

	remaining := c.size - c.pos
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := c.tr.ReadAt(p, abs)
	c.pos += int64(n)
	return n, err
}

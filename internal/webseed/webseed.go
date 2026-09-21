// Package webseed implements BEP 19 (WebSeed — HTTP/FTP Seeding, the
// "url-list" / GetRight-style variant): fetching a torrent's content over
// plain HTTP(S) range requests instead of the BitTorrent wire protocol.
// Perfect for the "Linux ISO with zero real peers" case — a torrent with a
// web seed can complete at full HTTP speed even if not a single BitTorrent
// peer is ever found.
//
// Scoped to single-file torrents only for v1 (see ErrMultiFileNotSupported):
// BEP 19's multi-file rules (a web seed URL treated as a directory, each
// file appended to it by path, and a single piece potentially straddling
// two files' worth of separate HTTP requests to satisfy) are real added
// complexity beyond one Range GET per piece — a documented, deliberate gap
// rather than a half-implemented version of it, the same "precisely scoped,
// not silently incomplete" shape this project uses for its other real
// simplifications (ipfilter's non-overlapping-ranges assumption, proxy's
// unproxied UDP trackers/DHT, and so on).
package webseed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/Oblutack/GoTorrent/internal/metainfo"
)

// ErrMultiFileNotSupported is FetchPiece's answer for any multi-file
// torrent — see the package doc comment for why.
var ErrMultiFileNotSupported = errors.New("webseed: multi-file torrents are not supported yet")

// Client fetches individual pieces of a torrent's content from one BEP 19
// web seed URL.
type Client struct {
	url        string
	httpClient *http.Client
}

// New returns a Client for baseURL. A nil httpClient defaults to
// http.DefaultClient.
func New(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{url: baseURL, httpClient: httpClient}
}

// FetchPiece downloads piece index's bytes from this web seed via a single
// HTTP Range GET. mi is passed per call, not cached at construction, since
// a torrent's metadata can still be nil (a magnet) when a Client is first
// built — the caller (internal/torrent's webSeedLoop) always has the
// freshest metainfo available and passing it through keeps this type from
// needing to reason about metadata arriving asynchronously at all.
//
// A response status other than 206 Partial Content is treated as a hard
// failure, not silently accepted as "the whole file, from byte 0" — a
// server that ignores the Range header would otherwise hand back the wrong
// bytes for every piece but the first, silently. Every real static-file
// server this was tested against (Go's own net/http.FileServer/ServeContent
// included) honors Range correctly, so this is a real, not hypothetical,
// safety check rather than an unnecessary one.
func (c *Client) FetchPiece(ctx context.Context, mi *metainfo.MetaInfo, index int) ([]byte, error) {
	if mi.Info.IsMultiFile() {
		return nil, ErrMultiFileNotSupported
	}
	if index < 0 || index >= mi.NumPieces() {
		return nil, fmt.Errorf("webseed: piece index %d out of range (%d pieces)", index, mi.NumPieces())
	}

	start := int64(index) * mi.Info.PieceLength
	length := mi.PieceLen(index)
	end := start + length - 1

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, fmt.Errorf("webseed: building request: %w", err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webseed: request to %s: %w", c.url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("webseed: %s returned status %d for a range request, want 206", c.url, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, length))
	if err != nil {
		return nil, fmt.Errorf("webseed: reading response body: %w", err)
	}
	if int64(len(data)) != length {
		return nil, fmt.Errorf("webseed: got %d bytes for piece %d, want %d", len(data), index, length)
	}
	return data, nil
}

// URL returns the web seed's own URL, for logging.
func (c *Client) URL() string { return c.url }

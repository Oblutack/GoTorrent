package tracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Oblutack/GoTorrent/internal/bencode"
	"github.com/Oblutack/GoTorrent/internal/version"
)

func TestGeneratePeerIDCarriesTheVersionPrefix(t *testing.T) {
	id, err := GeneratePeerID()
	if err != nil {
		t.Fatalf("GeneratePeerID: %v", err)
	}
	if !strings.HasPrefix(string(id[:]), version.PeerIDPrefix) {
		t.Fatalf("peer ID %q does not start with %q", id, version.PeerIDPrefix)
	}
}

func TestGenerateAnonymousPeerIDHasNoPrefix(t *testing.T) {
	id, err := GenerateAnonymousPeerID()
	if err != nil {
		t.Fatalf("GenerateAnonymousPeerID: %v", err)
	}
	if strings.HasPrefix(string(id[:]), version.PeerIDPrefix) {
		t.Fatalf("anonymous peer ID %q still carries the client prefix", id)
	}
	// Two calls must not produce the same ID - it has to actually be random
	// across the whole 20 bytes, not just the tail after a fixed prefix.
	id2, err := GenerateAnonymousPeerID()
	if err != nil {
		t.Fatalf("GenerateAnonymousPeerID (2nd): %v", err)
	}
	if id == id2 {
		t.Fatal("two GenerateAnonymousPeerID calls returned the same ID")
	}
}

func TestAnnounceHTTPSendsUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		data, _ := bencode.Marshal(struct {
			Interval int64 `bencode:"interval"`
		}{Interval: 1800})
		w.Write(data)
	}))
	defer srv.Close()

	c := NewClient(nil)
	if _, err := c.Announce(context.Background(), srv.URL+"/announce", AnnounceRequest{Port: 6881}); err != nil {
		t.Fatalf("Announce: %v", err)
	}
	if gotUA != version.UserAgent {
		t.Fatalf("User-Agent = %q, want %q", gotUA, version.UserAgent)
	}
}

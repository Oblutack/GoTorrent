package debugserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func startServer(t *testing.T) (*Server, string) {
	t.Helper()
	s, err := New("127.0.0.1:0")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	go s.Serve()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.Shutdown(ctx)
	})
	return s, "http://" + s.Addr()
}

// TestPprofIndexServesRealProfileList proves the pprof handlers are
// really wired up and answering — not just that this package compiles
// against net/http/pprof's function signatures.
func TestPprofIndexServesRealProfileList(t *testing.T) {
	_, base := startServer(t)

	resp, err := http.Get(base + "/debug/pprof/")
	if err != nil {
		t.Fatalf("GET /debug/pprof/: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	// The real pprof index lists real, known profile names — proof this
	// is genuinely net/http/pprof's own handler, not a stub.
	for _, want := range []string{"goroutine", "heap", "allocs"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("pprof index does not mention %q; got:\n%s", want, body)
		}
	}
}

// TestPprofGoroutineProfileReturnsRealData proves an actual profile
// endpoint (not just the index page) returns real pprof-format output.
func TestPprofGoroutineProfileReturnsRealData(t *testing.T) {
	_, base := startServer(t)

	resp, err := http.Get(base + "/debug/pprof/goroutine?debug=1")
	if err != nil {
		t.Fatalf("GET /debug/pprof/goroutine: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if !strings.Contains(string(body), "goroutine profile:") {
		t.Fatalf("goroutine profile output doesn't look real; got:\n%s", body)
	}
}

// TestPublishAppearsOnDebugVars proves a caller's published stats are
// real, current values served as real JSON — not a static example.
func TestPublishAppearsOnDebugVars(t *testing.T) {
	s, base := startServer(t)

	calls := 0
	s.Publish("counter", func() any {
		calls++
		return calls
	})

	for want := 1; want <= 3; want++ {
		resp, err := http.Get(base + "/debug/vars")
		if err != nil {
			t.Fatalf("GET /debug/vars: %v", err)
		}
		var got map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decoding /debug/vars: %v", err)
		}
		resp.Body.Close()

		counter, ok := got["counter"].(float64) // JSON numbers decode as float64
		if !ok {
			t.Fatalf("/debug/vars has no numeric \"counter\" key: %v", got)
		}
		if int(counter) != want {
			t.Fatalf("request %d: counter = %v, want %d — Publish's callback isn't being re-invoked per request", want, counter, want)
		}
	}
}

// TestTwoServersDoNotShareRegisteredNames proves Publish is scoped to
// one *Server, not a process-wide registry like the standard library's
// own expvar.Publish (which panics on a duplicate name) — two servers in
// the same test binary publishing the same name must not conflict.
func TestTwoServersDoNotShareRegisteredNames(t *testing.T) {
	a, baseA := startServer(t)
	b, baseB := startServer(t)

	a.Publish("who", func() any { return "a" })
	b.Publish("who", func() any { return "b" })

	getWho := func(base string) string {
		t.Helper()
		resp, err := http.Get(base + "/debug/vars")
		if err != nil {
			t.Fatalf("GET %s/debug/vars: %v", base, err)
		}
		defer resp.Body.Close()
		var got map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		who, _ := got["who"].(string)
		return who
	}

	if got := getWho(baseA); got != "a" {
		t.Fatalf("server A's \"who\" = %q, want \"a\"", got)
	}
	if got := getWho(baseB); got != "b" {
		t.Fatalf("server B's \"who\" = %q, want \"b\"", got)
	}
}

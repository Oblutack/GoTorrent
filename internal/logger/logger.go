package logger

import (
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"
)

var (
	Info    *log.Logger
	Warning *log.Logger
	Error   *log.Logger

	verbose bool
)

// init gives every logger a usable value before main runs. Without it, any
// code path that logs before logger.Init — including tests — dereferences a
// nil *log.Logger and panics.
func init() {
	Init(false)
}

// Init's own verbose gate only ever controlled whether Info/Warning/Logf
// reach this process's stderr — it never affects Tail/Subscribe below,
// which always record regardless of verbosity. A headless daemon staying
// quiet on stderr by default is a separate concern from a control-API
// client (Stage 5's log-streaming route) being able to see what the
// process is actually doing without needing a restart under -verbose.
func Init(isVerbose bool) {
	verbose = isVerbose

	infoHandle := io.Discard
	warnHandle := io.Discard
	if verbose {
		infoHandle = log.Writer()
		warnHandle = log.Writer()
	}

	Info = log.New(io.MultiWriter(infoHandle, recordingWriter{"info"}), "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	Warning = log.New(io.MultiWriter(warnHandle, recordingWriter{"warning"}), "WARNING: ", log.Ldate|log.Ltime|log.Lshortfile)
	Error = log.New(io.MultiWriter(log.Writer(), recordingWriter{"error"}), "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
}

func Logf(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	record("debug", strings.TrimRight(msg, "\n"))
	if verbose {
		log.Print(msg)
	}
}

// Entry is one recorded log line — kept in a bounded in-memory history
// (Tail) and fanned out live to every Subscribe caller. Level is one of
// "info", "warning", "error" (Info/Warning/Error's own three loggers) or
// "debug" (Logf, the verbose-only detail chatter scattered through most
// of this codebase's packages — by far the most common source in
// practice). Message is the fully-formatted line each *log.Logger would
// otherwise have written to stderr (including its own date/time/file
// prefix), not just the caller's raw format string — Time is this
// package's own capture moment, kept as a separate structured field for a
// caller that wants to sort or filter without parsing Message's text.
type Entry struct {
	Time    time.Time
	Level   string
	Message string
}

// historyLimit bounds Tail's in-memory buffer — generous enough to cover
// a real troubleshooting session's worth of recent activity without
// growing without bound over a long-running daemon's lifetime.
const historyLimit = 500

var (
	historyMu sync.Mutex
	history   []Entry

	subMu   sync.Mutex
	subs    map[int]chan Entry
	nextSub int
)

// record appends e to the bounded history and fans it out to every current
// Subscribe caller — called from recordingWriter.Write (Info/Warning/Error)
// and Logf directly.
func record(level, message string) {
	e := Entry{Time: time.Now(), Level: level, Message: message}

	historyMu.Lock()
	history = append(history, e)
	if len(history) > historyLimit {
		history = history[len(history)-historyLimit:]
	}
	historyMu.Unlock()

	subMu.Lock()
	for _, ch := range subs {
		select {
		case ch <- e:
		default:
			// A slow subscriber must never stall every other subscriber or
			// the logging call site itself — the same non-blocking-send-
			// under-backpressure shape peer.Client.Events and
			// engine.Engine.broadcast already use elsewhere in this
			// codebase. A dropped live entry is still in Tail's history.
		}
	}
	subMu.Unlock()
}

// recordingWriter is one of MultiWriter's writers in Init — it never
// forwards p anywhere itself (MultiWriter already writes to every listed
// writer independently), purely a hook to also call record for whatever a
// *log.Logger's own Output call just wrote.
type recordingWriter struct{ level string }

func (w recordingWriter) Write(p []byte) (int, error) {
	record(w.level, strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// Tail returns up to the n most recently recorded entries, oldest first.
// n == 0 means none (not "everything" — a caller that explicitly asks for
// zero, e.g. internal/api's "?tail=0" meaning "skip history entirely,"
// must actually get zero); n greater than what's kept returns everything
// kept; a negative n is treated the same as "no limit," matching this
// codebase's usual "negative means unlimited" convention (see
// internal/ratelimit) for a caller with no specific count in mind at all.
func Tail(n int) []Entry {
	historyMu.Lock()
	defer historyMu.Unlock()
	if n < 0 || n > len(history) {
		n = len(history)
	}
	out := make([]Entry, n)
	copy(out, history[len(history)-n:])
	return out
}

// Subscribe returns a channel of every Entry recorded from this call
// onward, plus a cancel func that stops delivery and releases the
// channel — the same Subscribe/cancel shape engine.Engine.Subscribe
// already uses for fleet-wide events, applied here to log lines instead.
func Subscribe() (<-chan Entry, func()) {
	ch := make(chan Entry, 64)

	subMu.Lock()
	id := nextSub
	nextSub++
	if subs == nil {
		subs = make(map[int]chan Entry)
	}
	subs[id] = ch
	subMu.Unlock()

	cancel := func() {
		subMu.Lock()
		delete(subs, id)
		subMu.Unlock()
	}
	return ch, cancel
}

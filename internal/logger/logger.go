package logger

import (
	"io"
	"log"
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

func Init(isVerbose bool) {
	verbose = isVerbose

	infoHandle := io.Discard
	warnHandle := io.Discard
	if verbose {
		infoHandle = log.Writer()
		warnHandle = log.Writer()
	}

	Info = log.New(infoHandle, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	Warning = log.New(warnHandle, "WARNING: ", log.Ldate|log.Ltime|log.Lshortfile)
	Error = log.New(log.Writer(), "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
}

func Logf(format string, v ...interface{}) {
	if verbose {
		log.Printf(format, v...)
	}
}

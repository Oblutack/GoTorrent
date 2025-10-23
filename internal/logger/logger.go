package logger

import (
	"io/ioutil"
	"log"
)

var (
	Info *log.Logger
	Warning *log.Logger
	Error *log.Logger

	verbose bool
)


func Init(isVerbose bool) {
	verbose = isVerbose
	
	infoHandle := ioutil.Discard 
	warnHandle := ioutil.Discard 
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
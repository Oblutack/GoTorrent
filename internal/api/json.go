package api

import (
	"encoding/json"
	"net/http"
)

// errorBody is every error response's shape: {"error": "message"}. One
// envelope for the whole API, so a client never has to branch on which
// route it called to find the message.
type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// A write failure here means the client already went away - there is
	// nothing left to tell it, so the error is deliberately dropped rather
	// than logged on every disconnected client.
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorBody{Error: message})
}

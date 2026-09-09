package api

import (
	"fmt"
	"net/http"
)

// HealthHandler is the one route that exists before 4.2's real REST
// surface does - proof the security chain around it actually works, and
// the same handler a later version's /api/v1/session-shaped health check
// can grow out of. userAgent is this daemon's own version.UserAgent-style
// identifier, echoed back so a caller can tell which build answered.
func HealthHandler(userAgent string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "%s ok\n", userAgent)
	}
}

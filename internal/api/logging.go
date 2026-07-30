package api

import (
	"log"
	"net/http"
	"time"
)

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Logging writes one access log line per request. Static UI assets are logged at
// a lower verbosity so hook and API traffic stays readable.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		if isAsset(r.URL.Path) && sw.status < 400 {
			return
		}
		log.Printf("%s %s %d %dB %s %s",
			r.Method, r.URL.RequestURI(), sw.status, sw.bytes,
			time.Since(start).Round(time.Millisecond), clientIP(r))
	})
}

func isAsset(p string) bool {
	switch {
	case len(p) > 4 && p[len(p)-4:] == ".css",
		len(p) > 3 && p[len(p)-3:] == ".js",
		len(p) > 4 && p[len(p)-4:] == ".ico",
		len(p) > 4 && p[len(p)-4:] == ".svg",
		len(p) > 5 && p[len(p)-5:] == ".html",
		p == "/":
		return true
	}
	return false
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return v
	}
	return r.RemoteAddr
}

package feed

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (m *Manager) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/calendar.ics", m.serveCalendar)
	mux.HandleFunc("/healthz", m.serveHealth)
	mux.HandleFunc("/readyz", m.serveReady)
	return mux
}

func (m *Manager) serveCalendar(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshot := m.Snapshot()
	if snapshot == nil {
		response.Header().Set("Cache-Control", "no-store")
		http.Error(response, "calendar is not ready", http.StatusServiceUnavailable)
		return
	}

	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	response.Header().Set("Content-Disposition", `inline; filename="airtrail-flights.ics"`)
	response.Header().Set("ETag", snapshot.ETag)
	response.Header().Set("Last-Modified", snapshot.LastModified.Format(http.TimeFormat))

	if etagMatches(request.Header.Get("If-None-Match"), snapshot.ETag) {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	if request.Header.Get("If-None-Match") == "" {
		if modifiedSince, err := http.ParseTime(request.Header.Get("If-Modified-Since")); err == nil {
			if !snapshot.LastModified.After(modifiedSince) {
				response.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}

	response.Header().Set("Content-Length", strconv.Itoa(len(snapshot.Data)))
	response.WriteHeader(http.StatusOK)
	if request.Method == http.MethodHead {
		return
	}
	_, _ = response.Write(snapshot.Data)
}

func (m *Manager) serveHealth(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintln(response, "ok")
}

func (m *Manager) serveReady(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if !m.HasSnapshot() {
		response.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintln(response, "not ready")
		return
	}
	response.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintln(response, "ready")
}

func etagMatches(header, current string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == current || strings.TrimPrefix(candidate, "W/") == current {
			return true
		}
	}
	return false
}

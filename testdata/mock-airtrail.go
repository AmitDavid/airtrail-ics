package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync/atomic"
)

var requests atomic.Int64

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/flight/list", serveFlights)
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func serveFlights(response http.ResponseWriter, _ *http.Request) {
	requestNumber := requests.Add(1)
	if requestNumber == 1 {
		http.Error(response, "intentional first-request failure", http.StatusInternalServerError)
		return
	}

	response.Header().Set("Content-Type", "application/json")
	if requestNumber == 2 {
		writeJSON(response, map[string]any{
			"success": true,
			"flights": []any{
				flight(101, "CPH", "JFK", "2026-08-06T08:00:00+02:00", "2026-08-06T16:00:00-04:00"),
				flight(102, "JFK", "CPH", "2026-08-12T18:00:00-04:00", "2026-08-13T08:00:00+02:00"),
				map[string]any{
					"id":                 104,
					"from":               map[string]any{"iata": "AAA", "tz": "UTC"},
					"to":                 map[string]any{"iata": "BBB", "tz": "UTC"},
					"departureScheduled": "2026-09-01T10:00:00Z",
					"landingScheduled":   nil,
				},
			},
		})
		return
	}

	writeJSON(response, map[string]any{
		"success": true,
		"flights": []any{
			flight(101, "CPH", "JFK", "2026-08-06T08:00:00+02:00", "2026-08-06T17:00:00-04:00"),
			flight(103, "LHR", "CPH", "2026-10-02T09:00:00+01:00", "2026-10-02T12:00:00+02:00"),
			canonicalFlight(105, "CPH", "TLL", "2027-01-10T12:00:00Z", "2027-01-10T14:00:00Z"),
		},
	})
}

func flight(id int, from, to, departure, landing string) map[string]any {
	return map[string]any{
		"id":                 id,
		"from":               airport(from),
		"to":                 airport(to),
		"departureScheduled": departure,
		"landingScheduled":   landing,
	}
}

func canonicalFlight(id int, from, to, departure, arrival string) map[string]any {
	return map[string]any{
		"id":        id,
		"from":      airport(from),
		"to":        airport(to),
		"departure": departure,
		"arrival":   arrival,
	}
}

func airport(code string) map[string]any {
	timezones := map[string]string{
		"CPH": "Europe/Copenhagen",
		"JFK": "America/New_York",
		"LHR": "Europe/London",
		"TLL": "Europe/Tallinn",
	}
	return map[string]any{"iata": code, "tz": timezones[code]}
}

func writeJSON(response http.ResponseWriter, value any) {
	if err := json.NewEncoder(response).Encode(value); err != nil {
		log.Printf("encode response: %v", err)
	}
}

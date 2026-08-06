package calendar

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type Event struct {
	FlightID   int64     `json:"flightId"`
	UID        string    `json:"uid"`
	Summary    string    `json:"summary"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	StartLocal string    `json:"startLocal,omitempty"`
	EndLocal   string    `json:"endLocal,omitempty"`
	Signature  string    `json:"signature"`
	Sequence   int       `json:"sequence"`
	CreatedAt  time.Time `json:"createdAt"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

func Generate(name string, events map[int64]Event) []byte {
	ordered := make([]Event, 0, len(events))
	for _, event := range events {
		ordered = append(ordered, event)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Start.Equal(ordered[j].Start) {
			return ordered[i].FlightID < ordered[j].FlightID
		}
		return ordered[i].Start.Before(ordered[j].Start)
	})

	var output bytes.Buffer
	writeLine(&output, "BEGIN:VCALENDAR")
	writeLine(&output, "VERSION:2.0")
	writeLine(&output, "PRODID:-//airtrail-ics//AirTrail ICS//EN")
	writeLine(&output, "CALSCALE:GREGORIAN")
	writeLine(&output, "METHOD:PUBLISH")
	writeLine(&output, "X-WR-CALNAME:"+escapeText(name))

	for _, event := range ordered {
		writeLine(&output, "BEGIN:VEVENT")
		writeLine(&output, "UID:"+event.UID)
		writeLine(&output, "DTSTAMP:"+formatUTC(event.CreatedAt))
		writeLine(&output, "CREATED:"+formatUTC(event.CreatedAt))
		writeLine(&output, "LAST-MODIFIED:"+formatUTC(event.ModifiedAt))
		writeLine(&output, fmt.Sprintf("SEQUENCE:%d", event.Sequence))
		writeLine(&output, "DTSTART:"+formatEventTime(event.StartLocal, event.Start))
		writeLine(&output, "DTEND:"+formatEventTime(event.EndLocal, event.End))
		writeLine(&output, "SUMMARY:"+escapeText(event.Summary))
		writeLine(&output, "STATUS:CONFIRMED")
		writeLine(&output, "TRANSP:OPAQUE")
		writeLine(&output, "END:VEVENT")
	}

	writeLine(&output, "END:VCALENDAR")
	return output.Bytes()
}

func formatUTC(value time.Time) string {
	return value.UTC().Format("20060102T150405Z")
}

func formatEventTime(local string, fallback time.Time) string {
	if local != "" {
		return local
	}
	return formatUTC(fallback)
}

func escapeText(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, ";", "\\;")
	value = strings.ReplaceAll(value, ",", "\\,")
	value = strings.ReplaceAll(value, "\r\n", "\\n")
	value = strings.ReplaceAll(value, "\r", "\\n")
	value = strings.ReplaceAll(value, "\n", "\\n")
	return value
}

func writeLine(output *bytes.Buffer, line string) {
	const firstLimit = 75
	const continuationLimit = 74

	limit := firstLimit
	for len(line) > limit {
		cut := runeBoundary(line, limit)
		if cut == 0 {
			cut = limit
		}
		output.WriteString(line[:cut])
		output.WriteString("\r\n ")
		line = line[cut:]
		limit = continuationLimit
	}
	output.WriteString(line)
	output.WriteString("\r\n")
}

func runeBoundary(value string, maximum int) int {
	if len(value) <= maximum {
		return len(value)
	}
	cut := maximum
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return cut
}

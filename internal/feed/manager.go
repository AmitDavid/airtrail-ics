package feed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"airtrail-ics/internal/airtrail"
	"airtrail-ics/internal/calendar"
)

const stateVersion = 1

var iataPattern = regexp.MustCompile(`^[A-Z]{3}$`)

type FlightSource interface {
	ListFlights(ctx context.Context, scope, userID string) ([]airtrail.Flight, error)
}

type Snapshot struct {
	Data         []byte
	ETag         string
	LastModified time.Time
}

type persistedState struct {
	Version      int                      `json:"version"`
	CalendarName string                   `json:"calendarName"`
	LastChanged  time.Time                `json:"lastChanged"`
	Events       map[int64]calendar.Event `json:"events"`
}

type candidate struct {
	FlightID   int64
	Summary    string
	Start      time.Time
	End        time.Time
	StartLocal string
	EndLocal   string
	Signature  string
}

type Manager struct {
	source       FlightSource
	scope        string
	userID       string
	calendarName string
	retryCount   int
	dataDir      string
	uidNamespace string
	logger       *slog.Logger

	mu       sync.RWMutex
	state    persistedState
	snapshot *Snapshot
	running  atomic.Bool
	wg       sync.WaitGroup
}

func NewManager(
	source FlightSource,
	scope string,
	userID string,
	calendarName string,
	retryCount int,
	dataDir string,
	instanceIdentity string,
	logger *slog.Logger,
) (*Manager, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	identityHash := sha256.Sum256([]byte(instanceIdentity))
	manager := &Manager{
		source:       source,
		scope:        scope,
		userID:       userID,
		calendarName: calendarName,
		retryCount:   retryCount,
		dataDir:      dataDir,
		uidNamespace: hex.EncodeToString(identityHash[:12]),
		logger:       logger,
		state: persistedState{
			Version:      stateVersion,
			CalendarName: calendarName,
			Events:       make(map[int64]calendar.Event),
		},
	}
	manager.load()
	return manager, nil
}

func (m *Manager) HasSnapshot() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot != nil
}

func (m *Manager) Snapshot() *Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot
}

func (m *Manager) RunCycle(ctx context.Context, trigger string) {
	if !m.running.CompareAndSwap(false, true) {
		m.logger.Warn("synchronization skipped because another cycle is running", "trigger", trigger)
		return
	}
	m.wg.Add(1)
	defer func() {
		m.running.Store(false)
		m.wg.Done()
	}()

	cycleStart := time.Now()
	m.logger.Info("calendar synchronization started", "trigger", trigger, "retry_count", m.retryCount)

	for attempt := 0; attempt <= m.retryCount; attempt++ {
		if attempt > 0 {
			delay := retryDelay(attempt)
			m.logger.Warn("calendar synchronization retry scheduled", "attempt", attempt, "delay", delay)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				m.logger.Info("calendar synchronization stopped", "reason", ctx.Err())
				return
			case <-timer.C:
			}
		}

		result, err := m.syncOnce(ctx)
		if err == nil {
			m.logger.Info(
				"calendar synchronization completed",
				"trigger", trigger,
				"attempt", attempt,
				"flights", result.flights,
				"events", result.events,
				"added", result.added,
				"updated", result.updated,
				"deleted", result.deleted,
				"skipped", result.skipped,
				"duration", time.Since(cycleStart).Round(time.Millisecond),
			)
			return
		}

		if errors.Is(err, context.Canceled) {
			m.logger.Info("calendar synchronization stopped", "reason", err)
			return
		}
		if attempt < m.retryCount {
			m.logger.Warn("calendar synchronization failed", "attempt", attempt, "error", err)
			continue
		}
		m.logger.Error(
			"calendar synchronization failed after retries",
			"attempts", m.retryCount+1,
			"error", err,
			"serving_cached_feed", m.HasSnapshot(),
			"duration", time.Since(cycleStart).Round(time.Millisecond),
		)
	}
}

func (m *Manager) Wait() {
	m.wg.Wait()
}

type syncResult struct {
	flights int
	events  int
	added   int
	updated int
	deleted int
	skipped int
}

func (m *Manager) syncOnce(ctx context.Context) (syncResult, error) {
	var result syncResult
	flights, err := m.source.ListFlights(ctx, m.scope, m.userID)
	if err != nil {
		return result, err
	}
	result.flights = len(flights)

	candidates := make(map[int64]candidate, len(flights))
	seen := make(map[int64]struct{}, len(flights))
	for _, flight := range flights {
		if flight.ID > 0 {
			if _, exists := seen[flight.ID]; exists {
				return result, fmt.Errorf("AirTrail returned duplicate flight ID %d", flight.ID)
			}
			seen[flight.ID] = struct{}{}
		}

		converted, reason := toCandidate(flight)
		if reason != "" {
			result.skipped++
			m.logger.Warn("flight skipped", "flight_id", flight.ID, "reason", reason)
			continue
		}
		candidates[flight.ID] = converted
	}

	now := time.Now().UTC().Truncate(time.Second)
	m.mu.RLock()
	previousEvents := m.state.Events
	hadSnapshot := m.snapshot != nil
	previousLastChanged := m.state.LastChanged
	m.mu.RUnlock()

	nextEvents := make(map[int64]calendar.Event, len(candidates))
	ids := make([]int64, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		item := candidates[id]
		previous, exists := previousEvents[id]
		if !exists {
			result.added++
			nextEvents[id] = calendar.Event{
				FlightID:   id,
				UID:        fmt.Sprintf("airtrail-%s-%d@airtrail-ics", m.uidNamespace, id),
				Summary:    item.Summary,
				Start:      item.Start,
				End:        item.End,
				StartLocal: item.StartLocal,
				EndLocal:   item.EndLocal,
				Signature:  item.Signature,
				Sequence:   0,
				CreatedAt:  now,
				ModifiedAt: now,
			}
			continue
		}

		if previous.Signature != item.Signature {
			result.updated++
			previous.Summary = item.Summary
			previous.Start = item.Start
			previous.End = item.End
			previous.StartLocal = item.StartLocal
			previous.EndLocal = item.EndLocal
			previous.Signature = item.Signature
			previous.Sequence++
			previous.ModifiedAt = now
		}
		nextEvents[id] = previous
	}

	for id := range previousEvents {
		if _, exists := nextEvents[id]; !exists {
			result.deleted++
		}
	}
	result.events = len(nextEvents)

	changed := !hadSnapshot || result.added > 0 || result.updated > 0 || result.deleted > 0
	lastChanged := previousLastChanged
	if changed || lastChanged.IsZero() {
		lastChanged = now
	}

	nextState := persistedState{
		Version:      stateVersion,
		CalendarName: m.calendarName,
		LastChanged:  lastChanged,
		Events:       nextEvents,
	}
	data := calendar.Generate(m.calendarName, nextEvents)
	nextSnapshot := makeSnapshot(data, lastChanged)

	if changed || m.state.CalendarName != m.calendarName {
		if err := m.persist(nextState); err != nil {
			return result, err
		}
	}

	m.mu.Lock()
	m.state = nextState
	m.snapshot = nextSnapshot
	m.mu.Unlock()
	return result, nil
}

func toCandidate(flight airtrail.Flight) (candidate, string) {
	if flight.ID <= 0 {
		return candidate{}, "missing or invalid flight id"
	}

	from, fromLocation, reason := strictAirport(flight.From, "origin")
	if reason != "" {
		return candidate{}, reason
	}
	to, toLocation, reason := strictAirport(flight.To, "destination")
	if reason != "" {
		return candidate{}, reason
	}

	departureScheduled, hasDepartureScheduled := nonEmpty(flight.DepartureScheduled)
	arrivalScheduled, hasArrivalScheduled := nonEmpty(flight.ArrivalScheduled)

	var startValue, endValue, startLabel, endLabel string
	if hasDepartureScheduled && hasArrivalScheduled {
		startValue = departureScheduled
		endValue = arrivalScheduled
		startLabel = "scheduled departure"
		endLabel = "scheduled arrival"
	} else {
		departure, hasDeparture := nonEmpty(flight.Departure)
		arrival, hasArrival := nonEmpty(flight.Arrival)
		if !hasDeparture || !hasArrival {
			return candidate{}, "missing scheduled and canonical time pair"
		}
		startValue = departure
		endValue = arrival
		startLabel = "canonical departure"
		endLabel = "canonical arrival"
	}

	start, err := time.Parse(time.RFC3339Nano, startValue)
	if err != nil {
		return candidate{}, "invalid " + startLabel + " timestamp"
	}
	end, err := time.Parse(time.RFC3339Nano, endValue)
	if err != nil {
		return candidate{}, "invalid " + endLabel + " timestamp"
	}
	if !end.After(start) {
		return candidate{}, endLabel + " is not after " + startLabel
	}

	start = start.UTC()
	end = end.UTC()
	startLocal := start.In(fromLocation).Format("20060102T150405")
	endLocal := end.In(toLocation).Format("20060102T150405")

	summary := from + " -> " + to
	signatureInput := fmt.Sprintf("%s\x00%s\x00%s", summary, startLocal, endLocal)
	signatureHash := sha256.Sum256([]byte(signatureInput))
	return candidate{
		FlightID:   flight.ID,
		Summary:    summary,
		Start:      start,
		End:        end,
		StartLocal: startLocal,
		EndLocal:   endLocal,
		Signature:  hex.EncodeToString(signatureHash[:]),
	}, ""
}

func strictAirport(airport *airtrail.Airport, role string) (string, *time.Location, string) {
	if airport == nil || airport.IATA == nil {
		return "", nil, "missing or invalid " + role + " IATA code"
	}
	code := strings.ToUpper(strings.TrimSpace(*airport.IATA))
	if !iataPattern.MatchString(code) {
		return "", nil, "missing or invalid " + role + " IATA code"
	}
	if airport.TZ == nil || strings.TrimSpace(*airport.TZ) == "" {
		return "", nil, "missing " + role + " airport timezone"
	}
	location, err := time.LoadLocation(strings.TrimSpace(*airport.TZ))
	if err != nil {
		return "", nil, "invalid " + role + " airport timezone"
	}
	return code, location, ""
}

func nonEmpty(value *string) (string, bool) {
	if value == nil {
		return "", false
	}
	trimmed := strings.TrimSpace(*value)
	return trimmed, trimmed != ""
}

func retryDelay(retryNumber int) time.Duration {
	if retryNumber <= 0 {
		return 0
	}
	delay := time.Minute
	for current := 1; current < retryNumber && delay < 15*time.Minute; current++ {
		delay *= 2
		if delay > 15*time.Minute {
			delay = 15 * time.Minute
		}
	}
	return delay
}

func makeSnapshot(data []byte, lastModified time.Time) *Snapshot {
	digest := sha256.Sum256(data)
	return &Snapshot{
		Data:         data,
		ETag:         `"` + hex.EncodeToString(digest[:]) + `"`,
		LastModified: lastModified.UTC().Truncate(time.Second),
	}
}

func (m *Manager) load() {
	path := filepath.Join(m.dataDir, "state.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		m.logger.Warn("cached calendar state could not be read", "error", err)
		return
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		m.logger.Warn("cached calendar state is invalid", "error", err)
		return
	}
	if state.Version != stateVersion || state.Events == nil || state.LastChanged.IsZero() {
		m.logger.Warn("cached calendar state has an unsupported or incomplete format")
		return
	}
	for id, event := range state.Events {
		if id <= 0 || event.FlightID != id || event.UID == "" || event.Signature == "" || !event.End.After(event.Start) {
			m.logger.Warn("cached calendar state contains an invalid event", "flight_id", id)
			return
		}
	}

	if state.CalendarName != m.calendarName {
		state.CalendarName = m.calendarName
		state.LastChanged = time.Now().UTC().Truncate(time.Second)
	}
	generated := calendar.Generate(m.calendarName, state.Events)
	m.mu.Lock()
	m.state = state
	m.snapshot = makeSnapshot(generated, state.LastChanged)
	m.mu.Unlock()
	m.logger.Info("cached calendar loaded", "events", len(state.Events), "last_modified", state.LastChanged)
}

func (m *Manager) persist(state persistedState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode calendar state: %w", err)
	}
	data = append(data, '\n')

	temporary, err := os.CreateTemp(m.dataDir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary state: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		temporary.Close()
		os.Remove(temporaryPath)
	}

	if err := temporary.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("set temporary state permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temporary state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync temporary state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		os.Remove(temporaryPath)
		return fmt.Errorf("close temporary state: %w", err)
	}

	statePath := filepath.Join(m.dataDir, "state.json")
	if err := os.Rename(temporaryPath, statePath); err != nil {
		os.Remove(temporaryPath)
		return fmt.Errorf("replace calendar state: %w", err)
	}
	if directory, err := os.Open(m.dataDir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

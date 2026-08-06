package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

const (
	defaultCalendarName = "AirTrail Flights"
	defaultSyncCron     = "0 */6 * * *"
	defaultSyncTimezone = "UTC"
	defaultRetryCount   = 3
	defaultHTTPTimeout  = 30 * time.Second
)

type Config struct {
	AirTrailURL    *url.URL
	AirTrailAPIKey string
	AirTrailScope  string
	AirTrailUserID string
	CalendarName   string
	SyncCron       string
	SyncLocation   *time.Location
	SyncTimezone   string
	SyncRetryCount int
	HTTPTimeout    time.Duration
	ListenAddress  string
	DataDir        string
}

func Load() (Config, error) {
	var cfg Config

	baseURL := strings.TrimSpace(os.Getenv("AIRTRAIL_URL"))
	if baseURL == "" {
		return cfg, errors.New("AIRTRAIL_URL is required")
	}
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return cfg, fmt.Errorf("parse AIRTRAIL_URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return cfg, errors.New("AIRTRAIL_URL must use http or https")
	}
	if parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return cfg, errors.New("AIRTRAIL_URL must be an origin or base path without query or fragment")
	}
	parsedURL.Path = strings.TrimRight(parsedURL.Path, "/")

	apiKey := strings.TrimSpace(os.Getenv("AIRTRAIL_API_KEY"))
	if apiKey == "" {
		return cfg, errors.New("AIRTRAIL_API_KEY is required")
	}

	scope := envOrDefault("AIRTRAIL_SCOPE", "mine")
	if scope != "mine" && scope != "user" && scope != "all" {
		return cfg, errors.New("AIRTRAIL_SCOPE must be mine, user, or all")
	}
	userID := strings.TrimSpace(os.Getenv("AIRTRAIL_USER_ID"))
	if scope == "user" && userID == "" {
		return cfg, errors.New("AIRTRAIL_USER_ID is required when AIRTRAIL_SCOPE=user")
	}

	calendarName := envOrDefault("CALENDAR_NAME", defaultCalendarName)
	syncSpec := envOrDefault("SYNC_CRON", defaultSyncCron)
	if _, err := cron.ParseStandard(syncSpec); err != nil {
		return cfg, fmt.Errorf("parse SYNC_CRON: %w", err)
	}

	timezone := envOrDefault("SYNC_TIMEZONE", defaultSyncTimezone)
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return cfg, fmt.Errorf("load SYNC_TIMEZONE: %w", err)
	}

	retryCount, err := intEnv("SYNC_RETRY_COUNT", defaultRetryCount)
	if err != nil {
		return cfg, err
	}
	if retryCount < 0 || retryCount > 20 {
		return cfg, errors.New("SYNC_RETRY_COUNT must be between 0 and 20")
	}

	httpTimeout, err := durationEnv("HTTP_TIMEOUT", defaultHTTPTimeout)
	if err != nil {
		return cfg, err
	}
	if httpTimeout <= 0 || httpTimeout > 10*time.Minute {
		return cfg, errors.New("HTTP_TIMEOUT must be greater than zero and no more than 10m")
	}

	cfg = Config{
		AirTrailURL:    parsedURL,
		AirTrailAPIKey: apiKey,
		AirTrailScope:  scope,
		AirTrailUserID: userID,
		CalendarName:   calendarName,
		SyncCron:       syncSpec,
		SyncLocation:   location,
		SyncTimezone:   timezone,
		SyncRetryCount: retryCount,
		HTTPTimeout:    httpTimeout,
		ListenAddress:  ":8080",
		DataDir:        "/data",
	}
	return cfg, nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func intEnv(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

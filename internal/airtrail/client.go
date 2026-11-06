package airtrail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 64 << 20

type Airport struct {
	IATA *string `json:"iata"`
	TZ   *string `json:"tz"`
}

type Flight struct {
	ID                 int64    `json:"id"`
	From               *Airport `json:"from"`
	To                 *Airport `json:"to"`
	Departure          *string  `json:"departure"`
	Arrival            *string  `json:"arrival"`
	DepartureScheduled *string  `json:"departureScheduled"`
	ArrivalScheduled   *string  `json:"arrivalScheduled"`
}

type listResponse struct {
	Success *bool    `json:"success"`
	Message string   `json:"message"`
	Flights []Flight `json:"flights"`
}

type Client struct {
	endpoint *url.URL
	apiKey   string
	http     *http.Client
}

func NewClient(baseURL *url.URL, apiKey string, timeout time.Duration) *Client {
	endpoint := *baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/flight/list"

	return &Client{
		endpoint: &endpoint,
		apiKey:   apiKey,
		http: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return errors.New("too many redirects")
				}
				if len(via) > 0 {
					original := via[0].URL
					if req.URL.Scheme != original.Scheme || req.URL.Host != original.Host {
						return errors.New("refusing cross-origin redirect")
					}
				}
				return nil
			},
		},
	}
}

func (c *Client) ListFlights(ctx context.Context, scope, userID string) ([]Flight, error) {
	requestURL := *c.endpoint
	query := requestURL.Query()
	query.Set("scope", scope)
	if scope == "user" {
		query.Set("userId", userID)
	}
	requestURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "airtrail-ics")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request AirTrail: %w", err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, maxResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read AirTrail response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, errors.New("AirTrail response exceeds 64 MiB")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("AirTrail returned HTTP %d", resp.StatusCode)
	}

	var payload listResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode AirTrail response: %w", err)
	}
	if payload.Success != nil && !*payload.Success {
		if payload.Message == "" {
			payload.Message = "request was not successful"
		}
		return nil, fmt.Errorf("AirTrail error: %s", payload.Message)
	}
	if payload.Flights == nil {
		return []Flight{}, nil
	}
	return payload.Flights, nil
}

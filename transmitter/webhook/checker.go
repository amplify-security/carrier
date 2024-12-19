package webhook

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"time"
)

const (
	// EndpointStateOnline represents an online endpoint.
	EndpointStateOnline EndpointState = iota
	// EndpointStateOffline represents an offline endpoint.
	EndpointStateOffline
)

type (
	// EndpointState represents the state of a webhook endpoint.
	EndpointState int

	// HealthChecker is a utility for checking the health of a webhook endpoint.
	HealthChecker struct {
		log                   *slog.Logger
		webhookEndpoint       string
		endpoint              string
		client                HTTPRequestDoer
		currentState          EndpointState
		offlineThresholdCount int
		offlineCount          int
		ticker                *time.Ticker
		ctrl                  chan EndpointState
		ctx                   context.Context
	}

	// HealthCheckerConfig encapsulates all configuration settings for the HealthChecker.
	HealthCheckerConfig struct {
		LogHandler            slog.Handler
		WebhookEndpoint       string
		HealthCheckEndpoint   string
		Interval              time.Duration
		Timeout               time.Duration
		OfflineThresholdCount int
		TLSInsecureSkipVerify bool
		Ctrl                  chan EndpointState
		Ctx                   context.Context
	}
)

// NewHealthChecker initializes and returns a new HealthChecker.
func NewHealthChecker(c *HealthCheckerConfig) *HealthChecker {
	ticker := time.NewTicker(c.Interval)
	checker := &HealthChecker{
		log:             slog.New(c.LogHandler).With("source", "webhook.HealthChecker"),
		endpoint:        c.HealthCheckEndpoint,
		webhookEndpoint: c.WebhookEndpoint,
		client: &http.Client{
			Timeout: c.Timeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: c.TLSInsecureSkipVerify,
					MinVersion:         tls.VersionTLS13,
				},
			},
		},
		currentState:          EndpointStateOffline,
		offlineThresholdCount: c.OfflineThresholdCount,
		ticker:                ticker,
		ctrl:                  c.Ctrl,
		ctx:                   c.Ctx,
	}
	return checker
}

// checkEndpoint sends a GET request to the webhook endpoint and returns the endpoint state.
func (c *HealthChecker) checkEndpoint() (EndpointState, error) {
	req, err := http.NewRequest(http.MethodGet, c.endpoint, nil)
	if err != nil {
		return EndpointStateOffline, err
	}
	resp, err := c.client.Do(req)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return EndpointStateOffline, err
	}
	if resp.StatusCode == http.StatusOK {
		return EndpointStateOnline, nil
	}
	return EndpointStateOffline, nil
}

// Run starts the execution loop for HealthChecker.
func (c *HealthChecker) Run() {
	for {
		select {
		case <-c.ctx.Done():
			// stop the ticker and return
			c.ticker.Stop()
			close(c.ctrl)
			return
		case <-c.ticker.C:
			state, _ := c.checkEndpoint()
			if c.currentState == EndpointStateOffline {
				// waiting for endpoint to initialize
				if state == EndpointStateOnline {
					c.log.Info("webhook online", "endpoint", c.webhookEndpoint)
					c.currentState = state
					c.offlineCount = 0
					c.ctrl <- state
					continue
				}
			}
			// endpoint is already online
			if state == EndpointStateOnline {
				// reset the current offline count
				c.offlineCount = 0
				continue
			}
			c.offlineCount++
			if c.offlineCount >= c.offlineThresholdCount {
				c.log.Warn("webhook offline", "endpoint", c.webhookEndpoint)
				c.currentState = state
				c.ctrl <- state
			}
		}
	}
}

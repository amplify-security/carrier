package webhook

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func getMockHTTPResponse(status int) *http.Response {
	res := &http.Response{
		StatusCode: status,
	}
	body := &MockReadCloser{}
	body.On("Close").Return(nil)
	res.Body = body
	return res
}

func resetHealthCheckerState(checker *HealthChecker, res *http.Response, err error, interval time.Duration) (chan EndpointState, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ctrl := make(chan EndpointState)
	m := &MockHTTPRequestDoer{}
	m.On("Do", mock.Anything, mock.Anything).Return(res, err)
	checker.client = m
	checker.ctx = ctx
	checker.ctrl = ctrl
	checker.ticker = time.NewTicker(interval)
	return ctrl, cancel
}

func TestNewHealthChecker(t *testing.T) {
	ctrl := make(chan EndpointState)
	m := &MockHTTPRequestDoer{}
	res := &http.Response{
		StatusCode: http.StatusOK,
	}
	body := &MockReadCloser{}
	body.On("Close").Return(nil)
	res.Body = body
	m.On("Do", mock.Anything, mock.Anything).Return(res, nil)
	checker := NewHealthChecker(&HealthCheckerConfig{
		LogHandler:            slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}),
		WebhookEndpoint:       "http://localhost:8000",
		HealthCheckEndpoint:   "http://localhost:8000",
		Interval:              1 * time.Second,
		Timeout:               1 * time.Second,
		OfflineThresholdCount: 3,
		TLSInsecureSkipVerify: true,
		Ctrl:                  ctrl,
		Ctx:                   context.Background(),
	})
	assert.NotNil(t, checker, "NewHealthChecker")
	checker.client = m
	assert.Equal(t, EndpointStateOffline, checker.currentState, "NewHealthChecker -> EndpointStateOffline")
}

func TestHealthChecker_Run(t *testing.T) {
	interval := 10 * time.Millisecond
	ctrl := make(chan EndpointState)
	ctx, cancel := context.WithCancel(context.Background())
	m := &MockHTTPRequestDoer{}
	m.On("Do", mock.Anything, mock.Anything).Return(getMockHTTPResponse(http.StatusOK), nil)
	checker := NewHealthChecker(&HealthCheckerConfig{
		LogHandler:            slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}),
		WebhookEndpoint:       "http://localhost:8000",
		HealthCheckEndpoint:   "http://localhost:8000",
		Interval:              interval,
		Timeout:               interval,
		OfflineThresholdCount: 3,
		TLSInsecureSkipVerify: true,
		Ctrl:                  ctrl,
		Ctx:                   ctx,
	})
	checker.client = m
	// wait for online check
	go checker.Run()
	state := <-ctrl
	assert.Equal(t, EndpointStateOnline, state, "HealthChecker.Run -> EndpointStateOnline")
	cancel()
	<-ctrl
	// reset state
	ctrl, cancel = resetHealthCheckerState(checker, getMockHTTPResponse(http.StatusInternalServerError),
		nil, interval)
	// wait for offline check
	go checker.Run()
	state = <-ctrl
	assert.Equal(t, EndpointStateOffline, state, "HealthChecker.Run -> EndpointStateOffline")
	cancel()
	<-ctrl
	// reset state
	ctrl, cancel = resetHealthCheckerState(checker, getMockHTTPResponse(http.StatusOK),
		nil, interval)
	// wait for online(2) check
	go checker.Run()
	state = <-ctrl
	assert.Equal(t, EndpointStateOnline, state, "HealthChecker.Run -> EndpointStateOnline(2)")
	cancel()
	<-ctrl
	// reset state
	ctrl, cancel = resetHealthCheckerState(checker, nil, errors.New("mock client failed"), interval)
	// wait for offline(2) check
	go checker.Run()
	state = <-ctrl
	assert.Equal(t, EndpointStateOffline, state, "HealthChecker.Run -> EndpointStateOffline(2)")
	cancel()
	<-ctrl
}

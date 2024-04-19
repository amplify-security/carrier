package webhook

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/amplify-security/carrier/transmitter"
)

const (
	// HEADER_PREFIX is the prefix used for all HTTP request headers sent by the Transmitter.
	HEADER_PREFIX = "X-Carrier-"
)

type (
	// HTTPClient interface defines the functions necessary for an HTTP client. This interface is used
	// to allow for mocking of the HTTP client in tests.
	HTTPClient interface {
		Do(*http.Request) (*http.Response, error)
	}

	// TransmitterConfig encapsulates all configuration settings for the Transmitter.
	TransmitterConfig struct {
		Endpoint              string
		TLSInsecureSkipVerify bool
	}

	// Transmitter sends messages to a webhook endpoint.
	Transmitter struct {
		endpoint string
		client   HTTPClient
	}
)

// NewTransmitter initializes and returns a new Transmitter.
func NewTransmitter(c *TransmitterConfig) *Transmitter {
	return &Transmitter{
		endpoint: c.Endpoint,
		client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: c.TLSInsecureSkipVerify,
				},
			},
		},
	}
}

// Transmit sends the message to the configured webhook endpoint with the provided attributes
// as HTTP request headers. All attributes will be prepended with the "X-CARRIER-" prefix before
// being sent as HTTP request headers.
func (t *Transmitter) Tx(message io.Reader, attributes transmitter.TransmitAttributes) error {
	req, err := http.NewRequest(http.MethodPost, t.endpoint, message)
	if err != nil {
		return fmt.Errorf("%w: failed to create request: %w", transmitter.ErrTransmitFailed, err)
	}
	for k, v := range attributes {
		req.Header.Add(fmt.Sprintf("%s%s", HEADER_PREFIX, k), v)
	}
	res, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: failed to send request: %w", transmitter.ErrTransmitFailed, err)
	}
	if res != nil && res.Body != nil {
		// ensure we close the response body
		defer res.Body.Close()
	}
	switch res.StatusCode {
	case http.StatusOK:
		// transmit successful
		return nil
	case http.StatusTooManyRequests:
		// return a retryable error with the retry-after header value
		retryErr := errors.New("status code 429 received")
		retryAfter := res.Header.Get("Retry-After")
		if retryAfter != "" {
			seconds, err := strconv.Atoi(retryAfter)
			if err != nil {
				// cannot retry if we cannot parse the Retry-After header
				return fmt.Errorf("%w: failed to parse Retry-After header: %w", transmitter.ErrTransmitFailed, err)
			}
			return transmitter.NewTransmitRetryableError(retryErr, time.Duration(seconds*int(time.Second)))
		}
		return fmt.Errorf("%w: status code 429 received: no Retry-After header", transmitter.ErrTransmitFailed)
	default:
		// return a non-retryable error
		return fmt.Errorf("%w: status code %d received", transmitter.ErrTransmitFailed, res.StatusCode)
	}
}

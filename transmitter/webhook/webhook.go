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
	// HeaderPrefix is the prefix used for all HTTP request headers sent by the Transmitter.
	HeaderPrefix = "X-Carrier-"
	HeaderRetryAfter = "Retry-After"
)

var (
	ErrNon200StatusCode              = errors.New("non-200 status code received")
	ErrStatusCode429                 = errors.New("status code 429 received")
	ErrNoRetryAfterHeader            = errors.New("no Retry-After header")
	ErrFailedToParseRetryAfterHeader = errors.New("failed to parse Retry-After header")
)

type (
	// HTTPRequestDoer interface defines the functions necessary for an HTTP client. This interface is used
	// to allow for mocking of the HTTP client in tests.
	HTTPRequestDoer interface {
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
		client   HTTPRequestDoer
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

// newRequest creates a new HTTP request with the provided message and attributes.
func (t *Transmitter) newRequest(message io.Reader, attributes transmitter.TransmitAttributes) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, t.endpoint, message)
	if err != nil {
		return req, err
	}
	for k, v := range attributes {
		req.Header.Add(fmt.Sprintf("%s%s", HeaderPrefix, k), v)
	}
	return req, err
}

// Transmit sends the message to the configured webhook endpoint with the provided attributes
// as HTTP request headers. All attributes will be prepended with the "X-CARRIER-" prefix before
// being sent as HTTP request headers.
func (t *Transmitter) Tx(message io.Reader, attributes transmitter.TransmitAttributes) error {
	req, err := t.newRequest(message, attributes)
	if err != nil {
		return fmt.Errorf("%w: failed to create request: %w", transmitter.ErrTransmitFailed, err)
	}
	res, err := t.client.Do(req)
	if res != nil && res.Body != nil {
		// ensure we close the response body
		defer res.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("%w: failed to send request: %w", transmitter.ErrTransmitFailed, err)
	}
	switch res.StatusCode {
	case http.StatusOK:
		// transmit successful
		return nil
	case http.StatusTooManyRequests:
		// return a retryable error with the retry-after header value
		retryAfter := res.Header.Get(HeaderRetryAfter)
		if retryAfter != "" {
			seconds, err := strconv.Atoi(retryAfter)
			if err != nil {
				// cannot retry if we cannot parse the Retry-After header
				return fmt.Errorf("%w: %w: %w", transmitter.ErrTransmitFailed, ErrStatusCode429, err)
			}
			return transmitter.NewTransmitRetryableError(ErrStatusCode429, time.Duration(seconds*int(time.Second)))
		}
		return fmt.Errorf("%w: %w: %w", transmitter.ErrTransmitFailed, ErrStatusCode429, ErrNoRetryAfterHeader)
	default:
		// return a non-retryable error
		return fmt.Errorf("%w: %w: %d", transmitter.ErrTransmitFailed, ErrNon200StatusCode, res.StatusCode)
	}
}

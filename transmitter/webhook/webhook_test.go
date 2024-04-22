package webhook

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/amplify-security/carrier/transmitter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type (
	// MockHTTPRequestDoer is a mock implementation of the HTTPClient interface.
	MockHTTPRequestDoer struct {
		mock.Mock
	}

	// MockReadCloser is a mock implementation of the io.ReadCloser interface.
	MockReadCloser struct {
		mock.Mock
	}
)

// Do implementation of the HTTPClient interface for the MockHTTPClient.
func (m *MockHTTPRequestDoer) Do(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	return args.Get(0).(*http.Response), args.Error(1)
}

// Read implementation of the io.ReadCloser interface for the MockReadCloser.
func (m *MockReadCloser) Read(p []byte) (n int, err error) {
	args := m.Called(p)
	return args.Int(0), args.Error(1)
}

// Close implementation of the io.ReadCloser interface for the MockReadCloser.
func (m *MockReadCloser) Close() error {
	args := m.Called()
	return args.Error(0)
}

func TestNewTransmitter(t *testing.T) {
	cases := []struct {
		endpoint   string
		skipVerify bool
	}{
		{
			endpoint:   "localhost",
			skipVerify: true,
		},
		{
			endpoint:   "http://localhost:8080",
			skipVerify: false,
		},
	}
	for _, c := range cases {
		tx := NewTransmitter(&TransmitterConfig{
			Endpoint:              c.endpoint,
			TLSInsecureSkipVerify: c.skipVerify,
		})
		assert.NotNil(t, tx, "NewTransmitter -> not nil")
		transport := tx.client.(*http.Client).Transport.(*http.Transport)
		assert.Equal(t, c.endpoint, tx.endpoint, fmt.Sprintf("NewTransmitter.endpoint -> %s", c.endpoint))
		assert.Equal(t, c.skipVerify, transport.TLSClientConfig.InsecureSkipVerify, fmt.Sprintf("NewTransmitter.skipVerify -> %t", c.skipVerify))
	}
}

func TestTransmitter_newRequest(t *testing.T) {
	cases := []struct {
		endpoint   string
		skipVerify bool
		r          io.Reader
		attributes transmitter.TransmitAttributes
		err        bool
	}{
		{
			endpoint: "fake:// \nlocalhost:8080\t",
			err:      true,
		},
		{
			endpoint: "http://localhost:8080",
			r:        &MockReadCloser{},
		},
		{
			endpoint: "http://localhost:8080",
			attributes: transmitter.TransmitAttributes{
				"Foo":    "bar",
				"Secure": "yes",
			},
		},
	}
	for _, c := range cases {
		tx := NewTransmitter(&TransmitterConfig{
			Endpoint:              c.endpoint,
			TLSInsecureSkipVerify: c.skipVerify,
		})
		req, err := tx.newRequest(c.r, c.attributes)
		if c.err {
			assert.NotNil(t, err, "Transmitter.newRequest -> error != nil")
		} else {
			assert.Nil(t, err, "Transmitter.newRequest -> error == nil")
		}
		if c.r != nil {
			assert.Equal(t, c.r, req.Body, "Transmitter.newRequest -> body")
		}
		if c.attributes != nil {
			for k, v := range c.attributes {
				assert.Contains(t, req.Header, fmt.Sprintf("%s%s", HeaderPrefix, k), fmt.Sprintf("Transmitter.newRequest -> header[%s]", k))
				assert.Equal(t, v, req.Header.Get(fmt.Sprintf("%s%s", HeaderPrefix, k)), fmt.Sprintf("Transmitter.newRequest -> header[%s] == %s", k, v))
			}
		}
	}
}

func TestTransmitter_Tx(t *testing.T) {
	cases := []struct {
		endpoint   string
		skipVerify bool
		r          io.Reader
		attributes transmitter.TransmitAttributes
		do         bool
		res        *http.Response
		retryAfter time.Duration
		err        error
	}{
		{
			endpoint: "fake:// \nlocalhost:8080\t",
			err:      transmitter.ErrTransmitFailed,
		},
		{
			endpoint: "http://localhost:8080",
			do:       true,
			res: &http.Response{
				StatusCode: http.StatusOK,
			},
		},
		{
			endpoint: "http://localhost:8080",
			do:       true,
			res: &http.Response{
				StatusCode: http.StatusInternalServerError,
			},
			err: ErrNon200StatusCode,
		},
		{
			endpoint: "http://localhost:8080",
			do:       true,
			res: &http.Response{
				StatusCode: http.StatusTooManyRequests,
			},
			err: ErrNoRetryAfterHeader,
		},
		{
			endpoint: "http://localhost:8080",
			do:       true,
			res: &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Retry-After": []string{"notanumber"}},
			},
			err: ErrFailedToParseRetryAfterHeader,
		},
		{
			endpoint: "http://localhost:8080",
			do:       true,
			res: &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Retry-After": []string{"10"}},
			},
			err:        transmitter.NewTransmitRetryableError(ErrStatusCode429, 10*time.Second),
			retryAfter: 10 * time.Second,
		},
	}
	for _, c := range cases {
		tx := NewTransmitter(&TransmitterConfig{
			Endpoint:              c.endpoint,
			TLSInsecureSkipVerify: c.skipVerify,
		})
		if c.res != nil {
			body := &MockReadCloser{}
			body.On("Close").Return(nil)
			c.res.Body = body
		}
		m := &MockHTTPRequestDoer{}
		m.On("Do", mock.Anything, mock.Anything).Return(c.res, c.err)
		tx.client = m
		err := tx.Tx(c.r, c.attributes)
		if c.err != nil {
			var retryable *transmitter.TransmitRetryableError
			assert.ErrorIs(t, err, c.err, fmt.Sprintf("Transmitter.Tx -> error == %v", c.err))
			if errors.As(c.err, &retryable) {
				// if error is retryable, check retry after time
				assert.ErrorAs(t, err, &retryable, fmt.Sprintf("Transmitter.Tx -> error == %T", &retryable))
				assert.Equal(t, c.retryAfter, retryable.RetryAfter, fmt.Sprintf("Transmitter.Tx -> retryAfter == %v", c.retryAfter))
			}
		} else {
			assert.Nil(t, err, "Transmitter.Tx -> error == nil")
		}
		if c.do {
			tx.client.(*MockHTTPRequestDoer).AssertCalled(t, "Do", mock.Anything)
		}
		if c.res != nil {
			c.res.Body.(*MockReadCloser).AssertCalled(t, "Close")
		}
	}
}

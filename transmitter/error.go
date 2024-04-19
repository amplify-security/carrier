package transmitter

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrTransmitFailed is an error that occurs when a message transmission fails.
	ErrTransmitFailed = errors.New("message transmit failed")
)

type (
	// TransmitRetryableError is an error that occurs when a transmission error is encountered
	// that may be retried at a later time.
	TransmitRetryableError struct {
		Err        error
		RetryAfter time.Duration
	}
)

// NewTransmitRetryableError initializes and returns a new TransmitRetryableError.
func NewTransmitRetryableError(err error, retryAfter time.Duration) error {
	return &TransmitRetryableError{
		Err: fmt.Errorf("%w: retryable error: %w", ErrTransmitFailed, err),
		RetryAfter: retryAfter,
	}
}

// Error implementation of the error interface for TransmitRetryableError.
func (e *TransmitRetryableError) Error() string {
	return e.Err.Error()
}

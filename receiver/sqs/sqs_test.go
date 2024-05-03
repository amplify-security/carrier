package sqs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/amplify-security/carrier/transmitter"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type (
	// mockMessageReadWriter is a mock implementation of the MessageReadWriter interface.
	mockMessageReadWriter struct {
		mock.Mock
	}

	// mockTransmitter is a mock implementation of the Transmitter interface.
	mockTransmitter struct {
		mock.Mock
	}
)

// GetQueueUrl implementation of the MessageReadWriter interface for the MockMessageReadWriter.
func (m *mockMessageReadWriter) GetQueueUrl(ctx context.Context, input *sqs.GetQueueUrlInput, opts ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error) {
	args := m.Called(ctx, input, opts)
	return args.Get(0).(*sqs.GetQueueUrlOutput), args.Error(1)
}

// ReceiveMessage implementation of the MessageReadWriter interface for the MockMessageReadWriter.
func (m *mockMessageReadWriter) ReceiveMessage(ctx context.Context, input *sqs.ReceiveMessageInput, opts ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	args := m.Called(ctx, input, opts)
	return args.Get(0).(*sqs.ReceiveMessageOutput), args.Error(1)
}

// DeleteMessageBatch implementation of the MessageReadWriter interface for the MockMessageReadWriter.
func (m *mockMessageReadWriter) DeleteMessageBatch(ctx context.Context, input *sqs.DeleteMessageBatchInput, opts ...func(*sqs.Options)) (*sqs.DeleteMessageBatchOutput, error) {
	args := m.Called(ctx, input, opts)
	return args.Get(0).(*sqs.DeleteMessageBatchOutput), args.Error(1)
}

// ChangeMessageVisibilityBatch implementation of the MessageReadWriter interface for the MockMessageReadWriter.
func (m *mockMessageReadWriter) ChangeMessageVisibilityBatch(ctx context.Context, input *sqs.ChangeMessageVisibilityBatchInput, opts ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityBatchOutput, error) {
	args := m.Called(ctx, input, opts)
	return args.Get(0).(*sqs.ChangeMessageVisibilityBatchOutput), args.Error(1)
}

// Tx implementation of the Transmitter interface for the MockTransmitter.
func (m *mockTransmitter) Tx(r io.Reader, a transmitter.TransmitAttributes) error {
	args := m.Called(r, a)
	return args.Error(0)
}

func TestNewHandler(t *testing.T) {
	h := newHandler(&handlerConfig{
		Transmitter: &mockTransmitter{},
		Ctx:         context.Background(),
		Work:        make(chan *message, 1),
		Results:     make(chan *transmitResult, 1),
	})
	assert.NotNil(t, h, "newHandler -> handler != nil")
}

func TestHandler_handleMessage(t *testing.T) {
	testErr := errors.New("mockTransmitter failed to transmit message")
	cases := []struct {
		m        *message
		txErr    error
		expected error
	}{
		{
			m: &message{
				Body: aws.String("foo"),
			},
		},
		{
			m:        &message{},
			expected: errNoMessageBody,
		},
		{
			m: &message{
				Body: aws.String("foo"),
			},
			expected: testErr,
			txErr:    testErr,
		},
	}
	for _, c := range cases {
		tx := &mockTransmitter{}
		if c.txErr == nil {
			tx.On("Tx", mock.Anything, mock.Anything).Return(nil)
		} else {
			tx.On("Tx", mock.Anything, mock.Anything).Return(c.txErr)
		}
		h := newHandler(&handlerConfig{
			Transmitter: tx,
			Ctx:         context.Background(),
		})
		err := h.handleMessage(c.m)
		if c.expected != nil {
			if !errors.Is(c.expected, errNoMessageBody) {
				tx.AssertCalled(t, "Tx", mock.Anything, mock.Anything)
			}
			assert.ErrorIs(t, err, c.expected, "handler.handleMessage -> error")
		} else {
			assert.Nil(t, err, "handler.handleMessage -> nil")
			tx.AssertCalled(t, "Tx", mock.Anything, mock.Anything)
		}
	}
}

func TestHandler_generateAttributes(t *testing.T) {
	cases := []struct {
		m        *message
		expected transmitter.TransmitAttributes
	}{
		{
			m: &message{
				Attributes: map[string]string{
					ApproxomiteReceiveCountSQSAttribute:          "1",
					ApproxomiteFirstReceiveTimestampSQSAttribute: "2021-09-01T00:00:00Z",
				},
			},
			expected: transmitter.TransmitAttributes{
				ReceiveCountTransmitAttribute:     "1",
				FirstReceiveTimeTransmitAttribute: "2021-09-01T00:00:00Z",
			},
		},
		{
			m: &message{
				Attributes: map[string]string{
					"UnsupportedKey": "1",
				},
			},
			expected: transmitter.TransmitAttributes{},
		},
		{
			m:        &message{},
			expected: transmitter.TransmitAttributes{},
		},
	}
	for _, c := range cases {
		h := newHandler(&handlerConfig{
			Ctx: context.Background(),
		})
		attrs := h.generateAttributes(c.m)
		assert.Equal(t, c.expected, attrs, "handler.generateAttributes")
	}
}

func TestHandler_handleMessages(t *testing.T) {
	testErr := errors.New("mockTransmitter failed to transmit message")
	cases := []struct {
		m        *message
		txErr    error
		expected error
	}{
		{
			m: &message{
				MessageID: aws.String("foo"),
				Body:      aws.String("bar"),
			},
		},
		{
			m: &message{
				MessageID: aws.String("foo"),
			},
			expected: errNoMessageBody,
		},
		{
			m: &message{
				MessageID: aws.String("foo"),
				Body:      aws.String("bar"),
			},
			expected: testErr,
			txErr:    testErr,
		},
	}
	for _, c := range cases {
		tx := &mockTransmitter{}
		if c.txErr == nil {
			tx.On("Tx", mock.Anything, mock.Anything).Return(nil)
		} else {
			tx.On("Tx", mock.Anything, mock.Anything).Return(c.txErr)
		}
		messages := make(chan *message, 1)
		results := make(chan *transmitResult, 1)
		ctx, cancel := context.WithCancel(context.Background())
		h := newHandler(&handlerConfig{
			Transmitter: tx,
			Ctx:         ctx,
			Work:        messages,
			Results:     results,
		})
		go h.handleMessages()
		messages <- c.m
		r := <-results
		cancel()
		assert.Equal(t, c.m.MessageID, r.MessageID, "handler.handleMessages -> message ID")
		if c.expected != nil {
			assert.ErrorIs(t, r.err, c.expected, "handler.handleMessages -> error")
		} else {
			assert.Nil(t, r.err, "handler.handleMessages -> nil")
		}
	}
}

func TestNewReceiver(t *testing.T) {
	cases := []struct {
		queueURL       string
		getQueueURLErr error
	}{
		{
			queueURL: "https://sqs.us-west-2.amazonaws.com/123456789012/foo",
		},
		{
			getQueueURLErr: errors.New("mock client failed to get queue URL"),
		},
	}
	for _, c := range cases {
		var rx *Receiver
		mockClient := &mockMessageReadWriter{}
		mockClient.On("GetQueueUrl", mock.Anything, mock.Anything, mock.Anything).Return(&sqs.GetQueueUrlOutput{
			QueueUrl: &c.queueURL,
		}, c.getQueueURLErr)
		ctx := context.Background()
		if c.getQueueURLErr == nil {
			rx = NewReceiver(&ReceiverConfig{
				LogHandler:        slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}),
				SQSClient:         mockClient,
				SQSQueueName:      "foo",
				VisibilityTimeout: 30,
				BatchSize:         10,
				Ctx:               ctx,
			})
			assert.Equal(t, c.queueURL, rx.queueURL, "NewReceiver.queueURL")
			assert.Equal(t, int32(30), rx.visibilityTimeout, "NewReceiver.visibilityTimeout")
			assert.Equal(t, int32(10), rx.batchSize, "NewReceiver.batchSize")
			assert.Equal(t, ctx, rx.ctx, "NewReceiver.ctx")
		} else {
			assert.Panics(t, func() {
				rx = NewReceiver(&ReceiverConfig{
					LogHandler:        slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}),
					SQSClient:         mockClient,
					SQSQueueName:      "foo",
					VisibilityTimeout: 30,
					BatchSize:         10,
					Ctx:               ctx,
				})

			}, "NewReceiver -> panics")
		}
	}
}

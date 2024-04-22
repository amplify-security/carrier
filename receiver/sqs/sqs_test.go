package sqs

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type (
	// MockMessageReadWriter is a mock implementation of the MessageReadWriter interface.
	MockMessageReadWriter struct {
		mock.Mock
	}
)

// GetQueueUrl implementation of the MessageReadWriter interface for the MockMessageReadWriter.
func (m *MockMessageReadWriter) GetQueueUrl(ctx context.Context, input *sqs.GetQueueUrlInput, opts ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error) {
	args := m.Called(ctx, input, opts)
	return args.Get(0).(*sqs.GetQueueUrlOutput), args.Error(1)
}

// ReceiveMessage implementation of the MessageReadWriter interface for the MockMessageReadWriter.
func (m *MockMessageReadWriter) ReceiveMessage(ctx context.Context, input *sqs.ReceiveMessageInput, opts ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	args := m.Called(ctx, input, opts)
	return args.Get(0).(*sqs.ReceiveMessageOutput), args.Error(1)
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
		mockClient := &MockMessageReadWriter{}
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

package sqs

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/amplify-security/carrier/transmitter"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type (
	// MessageReader interface defines the read messages API for SQS. This interface
	// allows for mocking the SQS client in tests.
	MessageReader interface {
		GetQueueUrl(context.Context, *sqs.GetQueueUrlInput, ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error)
		ReceiveMessage(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	}

	// MessageWriter interface defines the write messages API for SQS. This interface
	// allows for mocking the SQS client in tests.
	MessageWriter interface {
	}

	// MessageReadWriter interface defines the read and write messages API for SQS. This interface
	// allows for mocking the SQS client in tests.
	MessageReadWriter interface {
		MessageReader
		MessageWriter
	}

	// Transmitter interface defines the transmit API for carrier. This interface
	// allows for mocking the transmitter in tests.
	Transmitter interface {
		Tx(io.Reader, transmitter.TransmitAttributes) error
	}

	// ReceiverConfig encapsulates all configuration settings for the Receiver.
	ReceiverConfig struct {
		LogHandler        slog.Handler
		SQSClient         MessageReadWriter
		SQSQueueName      string
		VisibilityTimeout int
		BatchSize         int
		Transmitter       Transmitter
		Ctx               context.Context
	}

	// Receiver reads messages from an SQS queue using long polling.
	Receiver struct {
		log               *slog.Logger
		client            MessageReadWriter
		queueURL          string
		visibilityTimeout int32
		batchSize         int32
		transmitter       Transmitter
		ctx               context.Context
	}
)

// NewReceiver initializes and returns a new LongPoller.
func NewReceiver(c *ReceiverConfig) *Receiver {
	log := slog.New(c.LogHandler).With("source", "sqs.Receiver")
	res, err := c.SQSClient.GetQueueUrl(c.Ctx, &sqs.GetQueueUrlInput{
		QueueName: &c.SQSQueueName,
	})
	if err != nil {
		log.Error("failed to get queue URL", "queue_name", c.SQSQueueName, "error", err)
		panic(err)
	}
	return &Receiver{
		log:               log,
		client:            c.SQSClient,
		queueURL:          *res.QueueUrl,
		visibilityTimeout: int32(c.VisibilityTimeout),
		batchSize:         int32(c.BatchSize),
		transmitter:       c.Transmitter,
		ctx:               c.Ctx,
	}
}

// Rx starts the LongPoller event loop to receive messages.
func (p *Receiver) Rx() {
	p.log.Info("starting event loop")
	for {
		select {
		case <-p.ctx.Done():
			p.log.Info("stopping event loop")
			return
		default:
			// poll for new messages on the queue
			res, err := p.client.ReceiveMessage(p.ctx, &sqs.ReceiveMessageInput{
				QueueUrl:            &p.queueURL,
				MaxNumberOfMessages: p.batchSize,
				VisibilityTimeout:   p.visibilityTimeout,
				// this is a workaround until aws-sdk-go-v2 fixes issue #2124 https://github.com/aws/aws-sdk-go-v2/issues/2124
				AttributeNames: []types.QueueAttributeName{
					types.QueueAttributeName("ApproximateReceiveCount"),
					types.QueueAttributeName("ApproximateFirstReceiveTimestamp"),
				},
			})
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					p.log.Error("failed to receive messages", "error", err)
				}
				continue
			}
			for _, msg := range res.Messages {
				p.log.Info("received message", "message_id", *msg.MessageId, "body", *msg.Body, "attributes", msg.Attributes)
			}
		}
	}
}

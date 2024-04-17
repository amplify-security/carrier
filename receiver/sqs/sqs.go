package sqs

import (
	"context"
	"errors"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type (
	// MessageReader interface defines the read messages API for SQS. This interface
	// allows for mocking the SQS client in tests.
	MessageReader interface {
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

	// ReceiverConfig encapsulates all configuration settings for the LongPoller.
	ReceiverConfig struct {
		LogHandler        slog.Handler
		AWSConfig         *aws.Config
		SQSEndpoint       string
		SQSQueueName      string
		VisibilityTimeout int
		BatchSize         int
		Ctx               context.Context
	}

	// Receiver reads messages from an SQS queue using long polling.
	Receiver struct {
		log               *slog.Logger
		client            MessageReadWriter
		queueURL          string
		visibilityTimeout int32
		batchSize         int32
		ctx               context.Context
	}
)

// NewReceiver initializes and returns a new LongPoller.
func NewReceiver(c *ReceiverConfig) *Receiver {
	log := slog.New(c.LogHandler).With("source", "sqs.Receiver")
	client := sqs.NewFromConfig(*c.AWSConfig, func(o *sqs.Options) {
		o.BaseEndpoint = aws.String(c.SQSEndpoint)
	})
	res, err := client.GetQueueUrl(c.Ctx, &sqs.GetQueueUrlInput{
		QueueName: &c.SQSQueueName,
	})
	if err != nil {
		log.Error("failed to get queue URL", "queue_name", c.SQSQueueName, "error", err)
		panic(err)
	}
	return &Receiver{
		log:               log,
		client:            client,
		queueURL:          *res.QueueUrl,
		visibilityTimeout: int32(c.VisibilityTimeout),
		batchSize:         int32(c.BatchSize),
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
			})
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					p.log.Error("failed to receive messages", "error", err)
				}
				continue
			}
			for _, msg := range res.Messages {
				p.log.Info("received message", "message_id", *msg.MessageId, "body", *msg.Body)
			}
		}
	}
}

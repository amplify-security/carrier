package sqs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/amplify-security/carrier/transmitter"
	"github.com/amplify-security/probe/pool"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

const (
	// ApproxomiteReceiveCountSQSAttribute SQSAttributes key for the approximate number of times the message has been received.
	ApproxomiteReceiveCountSQSAttribute = "ApproximateReceiveCount"
	// ApproxomiteFirstReceiveTimestampSQSAttribute SQSAttributes key for the time the message was first received.
	ApproxomiteFirstReceiveTimestampSQSAttribute = "ApproximateFirstReceiveTimestamp"
	// RecieveCountTransmitAttribute TransmitAttributes key for number of times the message has been received.
	ReceiveCountTransmitAttribute = "Receive-Count"
	// FirstReceiveTimeTransmitAttribute TransmitAttributes key for the time the message was first received.
	FirstReceiveTimeTransmitAttribute = "First-Receive-Time"
)

var (
	errNoMessageBody = errors.New("message body is nil")
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
		DeleteMessageBatch(context.Context, *sqs.DeleteMessageBatchInput, ...func(*sqs.Options)) (*sqs.DeleteMessageBatchOutput, error)
		ChangeMessageVisibilityBatch(context.Context, *sqs.ChangeMessageVisibilityBatchInput, ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityBatchOutput, error)
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
		MaxWorkers        int
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
		cancel            context.CancelFunc
		pool              *pool.Pool
		messages          chan *message
		results           chan *transmitResult
	}

	// message encapsulates the SQS message and attributes.
	message struct {
		MessageID     *string
		ReceiptHandle *string
		Body          *string
		Attributes    map[string]string
	}

	// transmitResult encapsulates the result of a transmit operation.
	transmitResult struct {
		MessageID     *string
		ReceiptHandle *string
		err           error
	}

	// handlerConfig encapsulates all configuration settings for the Handler.
	handlerConfig struct {
		Transmitter Transmitter
		Ctx         context.Context
		Work        chan *message
		Results     chan *transmitResult
	}

	// handler handles the processing of messages received from the SQS queue.
	handler struct {
		transmitter Transmitter
		ctx         context.Context
		work        chan *message
		results     chan *transmitResult
	}
)

// newHandler initializes and returns a new MessageHandler.
func newHandler(c *handlerConfig) *handler {
	return &handler{
		transmitter: c.Transmitter,
		ctx:         c.Ctx,
		work:        c.Work,
		results:     c.Results,
	}
}

// generateAttributes generates the TransmitAttributes based on the SQS message attributes.
func (h *handler) generateAttributes(m *message) transmitter.TransmitAttributes {
	attributes := make(transmitter.TransmitAttributes)
	for k, v := range m.Attributes {
		switch k {
		case ApproxomiteReceiveCountSQSAttribute:
			attributes[ReceiveCountTransmitAttribute] = v
		case ApproxomiteFirstReceiveTimestampSQSAttribute:
			attributes[FirstReceiveTimeTransmitAttribute] = v
		}
	}
	return attributes

}

// handleMessage handles the message by transmitting it using the configured transmitter.
func (h *handler) handleMessage(m *message) error {
	if m.Body == nil {
		return fmt.Errorf("sqs.handler.handleMessage: %w", errNoMessageBody)
	}
	buf := bytes.NewBuffer([]byte(*m.Body))
	return h.transmitter.Tx(buf, h.generateAttributes(m))
}

// handleMessages transmits messages received on the work channel and submits results to the results channel.
func (h *handler) handleMessages() {
	for {
		select {
		case <-h.ctx.Done():
			return
		case m := <-h.work:
			h.results <- &transmitResult{
				MessageID: m.MessageID,
				err:       h.handleMessage(m),
			}
		}
	}
}

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
	workers := c.BatchSize
	if c.MaxWorkers != 0 {
		workers = c.MaxWorkers
	}
	messages := make(chan *message, c.BatchSize)
	results := make(chan *transmitResult, c.BatchSize)
	ctx, cancel := context.WithCancel(context.Background())
	p := pool.NewPool(&pool.PoolConfig{
		Size:       workers,
		BufferSize: workers,
		Ctx:        ctx,
	})
	for range workers {
		// start message handlers
		h := newHandler(&handlerConfig{
			Transmitter: c.Transmitter,
			Ctx:         ctx,
			Work:        messages,
			Results:     results,
		})
		p.Run(h.handleMessages)
	}
	return &Receiver{
		log:               log,
		client:            c.SQSClient,
		queueURL:          *res.QueueUrl,
		visibilityTimeout: int32(c.VisibilityTimeout),
		batchSize:         int32(c.BatchSize),
		transmitter:       c.Transmitter,
		ctx:               c.Ctx,
		cancel:            cancel,
		pool:              p,
	}
}

// processMessages processes any messages in the SQS receive message response.
func (p *Receiver) processMessages(res *sqs.ReceiveMessageOutput) {
	if len(res.Messages) == 0 {
		// empty receive, nothing to do here
		return
	}
	for _, msg := range res.Messages {
		p.messages <- &message{
			MessageID:     msg.MessageId,
			ReceiptHandle: msg.ReceiptHandle,
			Body:          msg.Body,
			Attributes:    msg.Attributes,
		}
	}
	deleteEntries := []types.DeleteMessageBatchRequestEntry{}
	retryEntries := []types.ChangeMessageVisibilityBatchRequestEntry{}
	for range len(res.Messages) {
		// this channel read will block until all messages from the previous batch are handled
		r := <-p.results
		if r.err != nil {
			var err *transmitter.TransmitRetryableError
			if errors.As(r.err, &err) {
				// update visibility timeout on retryable errors
				retryEntries = append(retryEntries, types.ChangeMessageVisibilityBatchRequestEntry{
					Id:                r.MessageID,
					ReceiptHandle:     r.ReceiptHandle,
					VisibilityTimeout: int32(err.RetryAfter.Seconds()),
				})
			} else {
				p.log.Error("failed to transmit message", "error", r.err)
			}
		} else {
			deleteEntries = append(deleteEntries, types.DeleteMessageBatchRequestEntry{
				Id:            r.MessageID,
				ReceiptHandle: r.ReceiptHandle,
			})
		}
	}
	if len(deleteEntries) > 0 {
		// delete messages that were successfully transmitted
		_, err := p.client.DeleteMessageBatch(p.ctx, &sqs.DeleteMessageBatchInput{
			QueueUrl: &p.queueURL,
			Entries:  deleteEntries,
		})
		if err != nil {
			p.log.Error("failed to delete messages", "error", err)
		}
	}
	if len(retryEntries) > 0 {
		// update visibility timeout on messages that received retryable errors
		_, err := p.client.ChangeMessageVisibilityBatch(p.ctx, &sqs.ChangeMessageVisibilityBatchInput{
			QueueUrl: &p.queueURL,
			Entries:  retryEntries,
		})
		if err != nil {
			p.log.Error("failed to update message visibility", "error", err)
		}
	}
}

// Rx starts the Receiver event loop to receive messages.
func (p *Receiver) Rx() {
	p.log.Info("starting event loop")
	for {
		select {
		case <-p.ctx.Done():
			p.log.Info("stopping event loop")
			p.cancel()
			p.pool.Stop(true)
			return
		default:
			// poll for new messages on the queue
			res, err := p.client.ReceiveMessage(p.ctx, &sqs.ReceiveMessageInput{
				QueueUrl:            &p.queueURL,
				MaxNumberOfMessages: p.batchSize,
				VisibilityTimeout:   p.visibilityTimeout,
				WaitTimeSeconds:     20,
				// this is a workaround until aws-sdk-go-v2 fixes issue #2124 https://github.com/aws/aws-sdk-go-v2/issues/2124
				AttributeNames: []types.QueueAttributeName{
					types.QueueAttributeName(ApproxomiteReceiveCountSQSAttribute),
					types.QueueAttributeName(ApproxomiteFirstReceiveTimestampSQSAttribute),
				},
			})
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					p.log.Error("failed to receive messages", "error", err)
				}
				continue
			}
			p.processMessages(res)
			// continue
		}
	}
}

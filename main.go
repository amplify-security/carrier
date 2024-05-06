package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"

	"github.com/amplify-security/carrier/receiver/sqs"
	"github.com/amplify-security/carrier/transmitter/webhook"
	"github.com/amplify-security/probe/pool"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awsSQS "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/kelseyhightower/envconfig"
	"github.com/lmittmann/tint"
)

type (
	Config struct {
		WebhookEndpoint              string `default:"http://localhost:9000" split_words:"true"`
		WebhookTLSInsecureSkipVerify bool   `envconfig:"WEBHOOK_TLS_INSECURE_SKIP_VERIFY" default:"false"`
		WebhookDefaultContentType    string `default:"application/json" split_words:"true"`
		SQSEndpoint                  string `envconfig:"SQS_ENDPOINT" required:"true"`
		SQSQueueName                 string `envconfig:"SQS_QUEUE_NAME" required:"true"`
		SQSBatchSize                 int    `envconfig:"SQS_BATCH_SIZE" default:"1"`
		SQSReceivers                 int    `envconfig:"SQS_RECEIVERS" default:"1"`
		SQSReceiverWorkers           int    `envconfig:"SQS_RECEIVER_WORKERS" default:"1"`
	}
)

// main entry point
func main() {
	var envCfg Config
	if err := envconfig.Process("carrier", &envCfg); err != nil {
		panic(err)
	}
	logHandler := tint.NewHandler(os.Stdout, &tint.Options{Level: slog.LevelInfo})
	log := slog.New(logHandler).With("source", "main")
	ctx, cancel := context.WithCancel(context.Background())
	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Error("failed to load AWS config", "error", err)
		panic(err)
	}
	sqsClient := awsSQS.NewFromConfig(awsCfg, func(o *awsSQS.Options) {
		o.BaseEndpoint = aws.String(envCfg.SQSEndpoint)
	})
	ctrl := make(chan os.Signal, 1)
	signal.Notify(ctrl, os.Interrupt)
	p := pool.NewPool(&pool.PoolConfig{
		LogHandler: logHandler,
		Ctx:        ctx,
		Size:       envCfg.SQSReceivers,
	})
	t := webhook.NewTransmitter(&webhook.TransmitterConfig{
		Endpoint:              envCfg.WebhookEndpoint,
		TLSInsecureSkipVerify: envCfg.WebhookTLSInsecureSkipVerify,
		DefaultContentType:    envCfg.WebhookDefaultContentType,
	})
	for range envCfg.SQSReceivers {
		receiver := sqs.NewReceiver(&sqs.ReceiverConfig{
			LogHandler:   logHandler,
			SQSClient:    sqsClient,
			SQSQueueName: envCfg.SQSQueueName,
			BatchSize:    envCfg.SQSBatchSize,
			MaxWorkers:   envCfg.SQSReceiverWorkers,
			Transmitter:  t,
			Ctx:          ctx,
		})
		p.Run(receiver.Rx)
	}
	// wait for shutdown
	<-ctrl
	cancel()
	p.Stop(true)
}

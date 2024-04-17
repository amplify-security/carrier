package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"

	"github.com/amplify-security/carrier/receiver/sqs"
	"github.com/amplify-security/probe/pool"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/kelseyhightower/envconfig"
	"github.com/lmittmann/tint"
)

type (
	Config struct {
		WebhookEndpoint string `default:"localhost" split_words:"true"`
		SQSEndpoint     string `envconfig:"SQS_ENDPOINT" required:"true"`
		SQSQueueName    string `envconfig:"SQS_QUEUE_NAME" required:"true"`
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
	ctrl := make(chan os.Signal, 1)
	signal.Notify(ctrl, os.Interrupt)
	receiver := sqs.NewReceiver(&sqs.ReceiverConfig{
		LogHandler:   logHandler,
		AWSConfig:    &awsCfg,
		SQSEndpoint:  envCfg.SQSEndpoint,
		SQSQueueName: envCfg.SQSQueueName,
		BatchSize:    10,
		Ctx:          ctx,
	})
	p := pool.NewPool(&pool.PoolConfig{
		LogHandler: logHandler,
		Ctx:        ctx,
		Size:       4,
	})
	p.Run(receiver.Rx)
	p.Run(receiver.Rx)
	p.Run(receiver.Rx)
	p.Run(receiver.Rx)
	// wait for shutdown
	<-ctrl
	cancel()
	p.Stop(true)
}

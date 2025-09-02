package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/amplify-security/carrier/receiver/sqs"
	"github.com/amplify-security/carrier/transmitter/webhook"
	"github.com/amplify-security/probe/pool"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awsSQS "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/dustin/go-humanize"
	"github.com/kelseyhightower/envconfig"
	"github.com/lmittmann/tint"
)

type (
	Config struct {
		EnableColorizedLogging       bool          `default:"false" split_words:"true"`
		EnableStatLog                bool          `default:"false" split_words:"true"`
		SQSBatchSize                 int           `envconfig:"SQS_BATCH_SIZE" default:"1"`
		SQSEndpoint                  string        `envconfig:"SQS_ENDPOINT" required:"true"`
		SQSQueueName                 string        `envconfig:"SQS_QUEUE_NAME" required:"true"`
		SQSReceivers                 int           `envconfig:"SQS_RECEIVERS" default:"1"`
		SQSReceiverWorkers           int           `envconfig:"SQS_RECEIVER_WORKERS" default:"1"`
		StatLogTimer                 time.Duration `default:"120s" split_words:"true"`
		WebhookEndpoint              string        `default:"http://localhost:9000" split_words:"true"`
		WebhookTLSInsecureSkipVerify bool          `envconfig:"WEBHOOK_TLS_INSECURE_SKIP_VERIFY" default:"false"`
		WebhookDefaultContentType    string        `default:"application/json" split_words:"true"`
		WebhookRequestTimeout        time.Duration `default:"60s" split_words:"true"`
		WebhookHealthCheckEndpoint   string        `split_words:"true"`
		WebhookOfflineThresholdCount int           `default:"5" split_words:"true"`
		WebhookHealthCheckInterval   time.Duration `default:"60s" split_words:"true"`
		WebhookHealthCheckTimeout    time.Duration `default:"10s" split_words:"true"`
	}

	// StatLogger is a utility for logging runtime statistics.
	StatLogger struct {
		ticker *time.Ticker
		log    *slog.Logger
		ctx    context.Context
	}

	// StatLoggerConfig encapsulates all configuration settings for the StatLogger.
	StatLoggerConfig struct {
		Ticker     *time.Ticker
		LogHandler slog.Handler
		Ctx        context.Context
	}
)

// NewStatLogger initializes and returns a new StatLogger.
func NewStatLogger(cfg *StatLoggerConfig) *StatLogger {
	return &StatLogger{
		ticker: cfg.Ticker,
		log:    slog.New(cfg.LogHandler).With("source", "main.StatLogger"),
		ctx:    cfg.Ctx,
	}
}

// Run executes the execution loop of the StatLogger.
func (l *StatLogger) Run() {
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-l.ticker.C:
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			l.log.Info("stats", "goroutines", runtime.NumGoroutine(), "memory", humanize.Bytes(m.Sys))
		}
	}
}

// main entry point
func main() {
	var envCfg Config
	var logHandler slog.Handler
	if err := envconfig.Process("carrier", &envCfg); err != nil {
		panic(err)
	}
	if envCfg.EnableColorizedLogging {
		logHandler = tint.NewHandler(os.Stdout, &tint.Options{Level: slog.LevelInfo})
	} else {
		logHandler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	}
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
	ticker := time.NewTicker(envCfg.StatLogTimer)
	ctrl := make(chan os.Signal, 1)
	state := make(chan webhook.EndpointState, 1)
	signal.Notify(ctrl, os.Interrupt)
	size := envCfg.SQSReceivers
	if envCfg.EnableStatLog {
		size++
	}
	if envCfg.WebhookHealthCheckEndpoint != "" {
		size++
	}
	p := pool.NewPool(&pool.PoolConfig{
		LogHandler: logHandler,
		Ctx:        ctx,
		Size:       size,
	})
	if envCfg.WebhookHealthCheckEndpoint != "" {
		// start health checker
		checker := webhook.NewHealthChecker(&webhook.HealthCheckerConfig{
			LogHandler:            logHandler,
			WebhookEndpoint:       envCfg.WebhookEndpoint,
			HealthCheckEndpoint:   envCfg.WebhookHealthCheckEndpoint,
			Interval:              envCfg.WebhookHealthCheckInterval,
			Timeout:               envCfg.WebhookHealthCheckTimeout,
			OfflineThresholdCount: envCfg.WebhookOfflineThresholdCount,
			TLSInsecureSkipVerify: envCfg.WebhookTLSInsecureSkipVerify,
			Ctrl:                  state,
			Ctx:                   ctx,
		})
		p.Run(checker.Run)
		<-state
		log.Info("carrier has arrived")
	}
	t := webhook.NewTransmitter(&webhook.TransmitterConfig{
		Endpoint:              envCfg.WebhookEndpoint,
		TLSInsecureSkipVerify: envCfg.WebhookTLSInsecureSkipVerify,
		DefaultContentType:    envCfg.WebhookDefaultContentType,
		RequestTimeout:        envCfg.WebhookRequestTimeout,
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
	if envCfg.EnableStatLog {
		// start stat log
		statLogger := NewStatLogger(&StatLoggerConfig{
			Ticker:     ticker,
			LogHandler: logHandler,
			Ctx:        ctx,
		})
		p.Run(statLogger.Run)
	}
	// wait for shutdown or webhook to go offline
	select {
	case <-ctrl:
	case <-state:
	}
	ticker.Stop()
	cancel()
	p.Stop(true)
}

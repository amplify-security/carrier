package webhook

import (
	"crypto/tls"
	"log/slog"
	"net/http"
)

type (
	HTTPClient interface {
		Do(*http.Request) (*http.Response, error)
	}

	TransmitterConfig struct {
		LogHandler slog.Handler
		Endpoint string
		TLSInsecureSkipVerify bool
	}

	Transmitter struct {
		log *slog.Logger
		endpoint string
		client   HTTPClient
	}
)

func NewTransmitter(c *TransmitterConfig) *Transmitter {
	log := slog.New(c.LogHandler).With("source", "webhook.Transmitter")
	return &Transmitter{
		log: log,
		endpoint: c.Endpoint,
		client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: c.TLSInsecureSkipVerify,
				},
			},
		},
	}
}
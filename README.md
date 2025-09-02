# Carrier

[![Amplify Security](https://github.com/amplify-security/carrier/actions/workflows/amplify.yml/badge.svg?branch=main)](https://github.com/amplify-security/carrier/actions/workflows/amplify.yml)
[![CI](https://github.com/amplify-security/carrier/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/amplify-security/carrier/actions/workflows/ci.yml)
[![Docker Pulls](https://img.shields.io/docker/pulls/amplifysecurity/carrier)](https://hub.docker.com/r/amplifysecurity/carrier)

A lightweight messaging adapter for webhooks written in Go. Currently, the
[`amplifysecurity/carrier`](https://hub.docker.com/r/amplifysecurity/carrier)
image size is under 8MB and at idle consumes under 5MB of RAM. Carrier can act as a messaging daemon in the
SQS event worker pattern and is designed to be deployed as a sidecar container in Kubernetes pods alongside
event workers.

## How to use Carrier

### Local development

Carrier can be used locally in a Docker Compose stack. For example, with a worker that expects webhooks
at `/webhook` and runs on port `9000`:

```yml
services:
  sqs:
    image: roribio/alpine-sqs
  carrier:
    image: amplifysecurity/carrier
    restart: unless-stopped
    volumes:
      - ${HOME}/.aws/credentials:/.aws/credentials
    links:
      - sqs:sqs
      - worker:worker
    environment:
      CARRIER_WEBHOOK_ENDPOINT: http://worker:9000/webhook
      CARRIER_SQS_ENDPOINT: http://sqs:9324
      CARRIER_SQS_QUEUE_NAME: default
  worker:
    build: .
```

> **Note**: This example still requires AWS credentials to be mounted even though they are not used or the AWS SDK will panic.

### Kubernetes manifest

Carrier can be deployed as a sidecar in a Kubernetes pod. For example, consuming messages from an SQS
queue `carrier-demo` in `us-west-2` for a worker that expects webhooks at `/webhook` and runs on port
`9000`:

```yml
---
# SQS event worker pattern deployment
apiVersion: apps/v1
kind: Deployment
metadata:
  name: carrier-demo
  namespace: demo
spec:
  replicas: 1
  selector:
    matchLabels:
      app: carrier-demo
  template:
    metadata:
      labels:
        app: carrier-demo
    spec:
      serviceAccountName: carrier-demo
      containers:
        - name: carrier
          image: amplifysecurity/carrier
          securityContext:
            runAsUser: 1000
            allowPrivilegeEscalation: false
            runAsNonRoot: true
          env:
            - name: CARRIER_WEBHOOK_ENDPOINT
              value: http://localhost:9000/webhook
            - name: CARRIER_SQS_ENDPOINT
              value: https://sqs.us-west-2.amazonaws.com
            - name: CARRIER_SQS_QUEUE_NAME
              value: carrier-demo
        - name: worker
          image: ${registry}/${container}:${tag}
```

> **Note**: This example assumes that the Kubernetes service account `carrier-demo` is mapped to an IAM role that has the appropriate permissions to access the `carrier-demo` SQS queue.

## Webhook Health Checks

Carrier supports health checks for the webhook. If a `CARRIER_WEBHOOK_HEALTH_CHECK_ENDPOINT` variable is provided,
Carrier will wait for this endpoint to come online before attempting to receive messages from SQS and transmit them
to the webhook endpoint. This can prevent scenarios where Carrier fails to submit many messages before the webhook
service successfully starts, which can send messages to dead letter queues unnecessarily. In addition, if the webhook
is determined to go offline, Carrier will exit. This is helpful when carrier is running in a pod, as K8s can restart
the failed pod. For information on configuring the webhook health check, see the Configuration section below.

## Dynamic visibility Timeouts

Carrier supports the concept of setting SQS visibility timouts dynamically from the worker application.
This can be useful for calculating distributed backoffs on specific messages. Carrier sends the following
headers with each webhook:

| Header                         | Description                                                                                                                                     |
| ------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `X-Carrier-Receive-Count`      | The SQS message receive count. This field indicates how many times the particular message has been received.                                    |
| `X-Carrier-First-Receive-Time` | The SQS message first receive time (in seconds since epoch). This field is the timestamp of the first time the particular message was received. |

Using these headers, any usual backoff scheme (like exponential) can be implemented on a distributed basis.
If the worker wants to set the visibility timeout of any message, return HTTP status code
`429 Too Many Requests` and utilize the `Retry-After` header. Carrier will calculate a new visibility
timeout to ensure that the message will not be received again until after the `Retry-After` header
timestamp.

## Webhook `Content-Type` Header

Carrier supports dynamic or pre-configured `Content-Type` header values and will always send the
`Content-Type` header in the HTTP POST to the webhook. To utilize a dynamic content type with SQS,
send an [SQS message attribute](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-message-metadata.html)
of Type `String` with Name `Body.ContentType` with the SQS message. The Value of the SQS message
attribute will be sent to the webhook in the `Content-Type` header. For a configurable but non-dynamic
`Content-Type` header, use the `CARRIER_WEBHOOK_DEFAULT_CONTENT_TYPE` environment variable.

## Configuration

Carrier currently has limited configuration options. More configuration options will be added as
the project continues to mature. Currently, all configuration is done via environment variables.

| Environment Variable                       | Required           | Default                 | Description                                                                                                                                                                                                                                                   |
| ------------------------------------------ | ------------------ | ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `CARRIER_ENABLE_COLORIZED_LOGGING`         |                    | `false`                 | When set to `true`, enables colorized log messages. This is useful when running carrier in a terminal or local Docker environment.                                                                                                                              |
| `CARRIER_ENABLE_STAT_LOG`                  |                    | `false`                 | When set to `true`, enables periodic statistics log messages.                                                                                                                                                                                                   |
| `CARRIER_SQS_ENDPOINT`                     | :white_check_mark: |                         | The endpoint for the SQS service. Official AWS service endpoints can be found [here](https://docs.aws.amazon.com/general/latest/gr/sqs-service.html).                                                                                                         |
| `CARRIER_SQS_BATCH_SIZE`                   |                    | `1`                     | The batch size each SQS receiver will request from SQS. All webhooks are transmitted one message per HTTP request.                                                                                                                                            |
| `CARRIER_SQS_QUEUE_NAME`                   | :white_check_mark: |                         | The SQS queue name.                                                                                                                                                                                                                                           |
| `CARRIER_SQS_RECEIVERS`                    |                    | `1`                     | The number of concurrent SQS receivers requesting messages from SQS.                                                                                                                                                                                          |
| `CARRIER_SQS_RECEIVER_WORKERS`             |                    | `1`                     | The number of concurrent workers transmitting messages as webhooks for each receiver. A common pattern is to set the batch size and receiver workers to the same value, which will cause all messages in a batch to be transmitted in parallel HTTP requests. |
| `CARRIER_STAT_LOG_TIMER`                   |                    | `120s`                  | The interval between statistics log messages.                                                                                                                                                                                                                 |
| `CARRIER_WEBHOOK_ENDPOINT`                 |                    | `http://localhost:9000` | The full path, including protocol, that webhooks will be sent to. For example, if your worker expects webhooks at `/v1/events`, `http://worker:8080/v1/events`.                                                                                               |
| `CARRIER_WEBHOOK_TLS_INSECURE_SKIP_VERIFY` |                    | `false`                 | When set to true, the webhook transmitter will not attempt to validate TLS for an `https` webhook endpoint.                                                                                                                                                   |
| `CARRIER_WEBHOOK_DEFAULT_CONTENT_TYPE`     |                    | `application/json`      | The default value that will be sent in the `Content-Type` header in all HTTP POSTS to the webhook endpoint.                                                                                                                                                   |
| `CARRIER_WEBHOOK_REQUEST_TIMEOUT`          |                    | `60s`                   | The webhook transmitter request timeout. See Go's [`time.ParseDuration()`](https://pkg.go.dev/time#ParseDuration) for acceptable formats.                                                                                                                     |
| `CARRIER_WEBHOOK_HEALTH_CHECK_ENDPOINT`    |                    |                         | When set, enables health check functionality using the provided endpoint.                                                                                                                                                                                     |
| `CARRIER_WEBHOOK_OFFLINE_THRESHOLD_COUNT`  |                    | `5`                     | The number of failed health checks before the webhook is determined to be offline.                                                                                                                                                                            |
| `CARRIER_WEBHOOK_HEALTH_CHECK_INTERVAL`    |                    | `60s`                   | The time interval between webhook health checks. See Go's [`time.ParseDuration()`](https://pkg.go.dev/time#ParseDuration) for acceptable formats.                                                                                                             |
| `CARRIER_WEBHOOK_HEALTH_CHECK_TIMEOUT`     |                    | `10s`                   | The webhook health check timeout. See Go's [`time.ParseDuration()`](https://pkg.go.dev/time#ParseDuration) for acceptable formats.                                                                                                                            |

## Architecture

Carrier was built with the idea of separating receivers and transmitters. Receivers receive (or read)
messages and transmitters transmit (or send) messages. Currently, the only implemented receiver is
for SQS and the only implemented transmitter is for an HTTP POST (or webhook). In the future this
architecture may be used to support multiple messaging queues and transmission methods.

## Prior Art

Carrier probably would not exist without the many great examples of SQS event worker daemons that
have been implemented before including (but not limited to):

- [`mozart-analytics/sqsd`](https://github.com/mozart-analytics/sqsd)
- [`mogadanez/sqsd`](https://github.com/mogadanez/sqsd)
- [`fterrag/simple-sqsd`](https://github.com/fterrag/simple-sqsd)

However, we still felt the need to develop and maintain carrier for a few reasons. First, the publicly
available `sqsd` Docker images include large and heavy frameworks like the JDK or Node.js which leads
to relatively large image sizes for what should be a simple sidecar. Second, the versions of `sqsd`
that implemented specific features like supporting dynamic visibility timeouts did not have publicly
available Docker images. Finally, carrier was built with the intent to extend it to be a more generic
message passing service with more than SQS support.

## Why Carrier?

Carrier is named after the Protoss Carrier unit from the popular video game series StarCraft. We didn't
set out to name our open source projects after a nostalgic video game series, but here we are! You can
imagine your carrier fleet ferrying messages for your application.

![Carrier](https://raw.githubusercontent.com/amplify-security/carrier/main/doc/carrier.jpg)

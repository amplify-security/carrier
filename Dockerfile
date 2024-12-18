FROM golang:1.23.4-alpine AS builder

WORKDIR /usr/src/app

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY receiver ./receiver
COPY transmitter ./transmitter
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /go/bin/carrier


FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /go/bin/carrier /go/bin/carrier

ENTRYPOINT ["/go/bin/carrier"]

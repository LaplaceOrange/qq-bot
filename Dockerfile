# syntax=docker/dockerfile:1
FROM golang:1.23-alpine AS builder
RUN apk add --no-cache ca-certificates git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/qq-bot ./cmd/bot

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /out/qq-bot /app/qq-bot
ENV DATA_PATH=/data/bot.db
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/qq-bot"]

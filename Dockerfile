# Build Stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
# CGO_ENABLED=0 for static binary
RUN CGO_ENABLED=0 GOOS=linux go build -o jetstream ./cmd/jetstream

# Final Stage
FROM alpine:latest

WORKDIR /app

# Install ca-certificates for HTTPS and ffmpeg for transcoding
RUN apk --no-cache add ca-certificates ffmpeg

# Copy binary from builder
COPY --from=builder /app/jetstream .

# Copy .env (optional, mostly for local dev, usually overridden by env vars in k8s/docker)
# COPY .env . 

EXPOSE 8080

CMD ["./jetstream"]

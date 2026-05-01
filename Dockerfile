FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o backend ./cmd/api

# Use a smaller image for the final stage
FROM alpine:latest

WORKDIR /app

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates

# Copy the binary from the builder stage
COPY --from=builder /app/backend .
COPY --from=builder /app/.env.example .env

# Expose the port the app runs on
EXPOSE 8080

# Command to run the executable
CMD ["./backend"]


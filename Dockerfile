# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy dependency files and download
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o draw-and-guess .

# Final stage
FROM alpine:latest

WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/draw-and-guess .

# Copy static assets
COPY --from=builder /app/static ./static
COPY --from=builder /app/assets ./assets

# Expose port (Render overrides this via the PORT environment variable)
EXPOSE 8080

# Command to run the executable
CMD ["./draw-and-guess"]

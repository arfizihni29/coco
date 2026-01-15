# Build Stage
FROM golang:1.20-alpine AS builder

WORKDIR /app

# Install git.
# Git is required for fetching the dependencies.
RUN apk update && apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build the binary.
RUN go build -o main .

# Run Stage
FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/main .
COPY --from=builder /app/*.html ./
COPY --from=builder /app/manifest.json ./
COPY --from=builder /app/sw.js ./
COPY --from=builder /app/icon.svg ./
COPY --from=builder /app/logo.png ./

# Create uploads directory
RUN mkdir uploads

EXPOSE 8080

CMD ["./main"]

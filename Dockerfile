# Build stage
FROM golang:1.22-alpine AS builder
WORKDIR /app
# cache-bust: 20260401140904
COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o humanmcp ./cmd/server/

# Runtime stage
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/humanmcp .
RUN mkdir -p /data/content
ENV PORT=8080
ENV CONTENT_DIR=/data/content
EXPOSE 8080
CMD ["./humanmcp"]

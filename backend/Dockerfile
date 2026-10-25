FROM golang:1.24-alpine3.20 AS builder
ENV GOTOOLCHAIN=auto
WORKDIR /src

# Önce bağımlılık katmanını önbelleğe al
COPY go.mod go.sum ./
RUN go mod download

# Kaynak kodları kopyala ve derle
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/simulator ./cmd/simulator && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/verify ./cmd/verify

FROM alpine:3.20.3
RUN adduser -D -u 10001 appuser
WORKDIR /app
COPY --from=builder /out/api /app/api
COPY --from=builder /out/simulator /app/simulator
COPY --from=builder /out/verify /app/verify
USER appuser
EXPOSE 8080
CMD ["/app/api"]
FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod ./
# go.sum henüz yoksa go mod tidy ile indirir
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/simulator ./cmd/simulator
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/verify ./cmd/verify

FROM alpine:3.20
RUN adduser -D -u 10001 appuser
WORKDIR /app
COPY --from=builder /out/api /app/api
COPY --from=builder /out/simulator /app/simulator
COPY --from=builder /out/verify /app/verify
USER appuser
EXPOSE 8080
CMD ["/app/api"]
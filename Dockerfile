ARG BUILDPLATFORM
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -o /proxysql ./cmd/proxysql

FROM alpine:3.20
RUN adduser -D -u 10001 proxysql
WORKDIR /app
COPY --from=builder /proxysql /app/proxysql
COPY config.yaml /app/config.yaml
USER proxysql
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --retries=3 CMD wget -qO- http://127.0.0.1:8080/healthz || wget -qO- http://127.0.0.1:8080/proxysql/healthz || exit 1
ENTRYPOINT ["/app/proxysql", "-config", "/app/config.yaml"]

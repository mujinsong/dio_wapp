FROM golang:1.26.6-alpine AS build

WORKDIR /src
RUN apk add --no-cache build-base

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/dio-api ./cmd/api

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S dio \
    && adduser -S -G dio dio

WORKDIR /app
COPY --from=build /out/dio-api /app/dio-api

USER dio
EXPOSE 8888
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q -O - http://127.0.0.1:8888/readyz || exit 1

ENTRYPOINT ["/app/dio-api"]

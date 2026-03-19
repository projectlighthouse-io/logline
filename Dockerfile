FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w \
      -X main.version=${VERSION} \
      -X main.commit=${COMMIT} \
      -X main.buildTime=${BUILD_TIME}" \
    -o /logline ./cmd/logline

FROM gcr.io/distroless/static-debian12

COPY --from=builder /logline /logline

EXPOSE 4000

ENTRYPOINT ["/logline"]

# syntax=docker/dockerfile:1.7

FROM node:18 AS builder

WORKDIR /web
COPY ./VERSION .
COPY ./web .

WORKDIR /web/default
RUN npm config set registry https://registry.npmmirror.com && npm install
RUN DISABLE_ESLINT_PLUGIN='true' REACT_APP_VERSION=$(cat ../VERSION) npm run build

# WORKDIR /web/berry
# RUN npm install
# RUN DISABLE_ESLINT_PLUGIN='true' REACT_APP_VERSION=$(cat VERSION) npm run build

# WORKDIR /web/air
# RUN npm install
# RUN DISABLE_ESLINT_PLUGIN='true' REACT_APP_VERSION=$(cat VERSION) npm run build

FROM golang:1.25 AS builder2

ENV GO111MODULE=on \
    CGO_ENABLED=1 \
    GOOS=linux

WORKDIR /build
ADD go.mod go.sum ./
COPY third_party/charge-shipper-v0.1.0 ./third_party/charge-shipper-v0.1.0
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
COPY --from=builder /web/build ./web/build
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -mod=readonly -ldflags "-s -w -X 'github.com/songquanpeng/one-api/common.Version=$(cat VERSION)' -extldflags '-static'" -o one-api

FROM alpine:3.22

RUN apk update \
    && apk upgrade \
    && apk add --no-cache ca-certificates tzdata \
    && update-ca-certificates 2>/dev/null || true

COPY --from=builder2 /build/one-api /
EXPOSE 3000
WORKDIR /data
ENTRYPOINT ["/one-api"]

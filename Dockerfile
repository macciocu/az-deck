# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/azd ./cmd/azd

# Runtime image ships the Azure CLI so the default credential chain can reuse
# a mounted ~/.azure login (or set AZURE_CLIENT_ID/AZURE_TENANT_ID/AZURE_CLIENT_SECRET).
FROM mcr.microsoft.com/azure-cli:latest
ENV TERM=xterm-256color EDITOR=vi
COPY --from=build /out/azd /usr/local/bin/azd
ENTRYPOINT ["azd"]

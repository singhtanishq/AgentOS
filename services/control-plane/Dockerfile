# AgentOS control plane — multi-stage build for linux/arm64 and amd64.
FROM golang:1.27-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agentos-control-plane ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/agentos-control-plane /agentos-control-plane

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/agentos-control-plane"]

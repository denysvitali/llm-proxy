# Stage 1: static Go binary. Package main lives at the repo root.
# chmod is explicit: some CI runners run with a umask that leaves the
# go build output non-executable, and distroless then fails at exec.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /llm-proxy . \
    && chmod 0755 /llm-proxy

# Stage 2: minimal runtime image.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /llm-proxy /llm-proxy
EXPOSE 8090
ENTRYPOINT ["/llm-proxy"]

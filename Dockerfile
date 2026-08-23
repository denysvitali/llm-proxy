# Stage 1: dashboard SPA. The built bundle is copied into the embed
# directory consumed by internal/server/web.go before `go build`.
FROM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

# Stage 2: static Go binary with the SPA embedded.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./internal/server/web/webdist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /llm-proxy . \
    && chmod 0755 /llm-proxy

# Stage 3: minimal runtime image.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /llm-proxy /llm-proxy
EXPOSE 8090
ENTRYPOINT ["/llm-proxy"]

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /llm-proxy ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /llm-proxy /llm-proxy
EXPOSE 8090
ENTRYPOINT ["/llm-proxy"]

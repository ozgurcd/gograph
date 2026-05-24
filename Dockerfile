FROM golang:1.26-bookworm AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /gograph ./cmd/gograph

FROM debian:bookworm-slim
COPY --from=builder /gograph /gograph

# Start the MCP server by default
ENTRYPOINT ["/gograph"]
CMD ["mcp"]

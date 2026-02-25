FROM golang:1.24-alpine AS builder

RUN apk add --no-cache protoc protobuf-dev

# Install code-generation tools (cached layer — tools change rarely).
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest \
    && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest \
    && go install github.com/swaggo/swag/cmd/swag@latest

WORKDIR /app

# Generate protobuf bindings before copying Go source.
COPY proto/ proto/
RUN protoc \
    --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    proto/bridge.proto

# Copy Go source and module definition.
COPY go.mod ./
COPY *.go ./

# Generate Swagger docs from annotations (reads comments; no compilation needed).
RUN swag init

# Resolve all dependencies (writes go.sum) then build.
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o /sidecar .

FROM alpine:3.19
RUN apk add --no-cache bash python3
COPY --from=builder /sidecar /sidecar
COPY bridge_ctl.py /usr/local/bin/bridge-ctl
RUN chmod +x /usr/local/bin/bridge-ctl
ENTRYPOINT ["/sidecar"]

#!/bin/bash
# 生成 gRPC Go 代码
# 前置条件：
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
#   protoc (https://github.com/protocolbuffers/protobuf/releases)

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

protoc \
  --go_out=. \
  --go_opt=paths=source_relative \
  --go-grpc_out=. \
  --go-grpc_opt=paths=source_relative \
  gateway.proto

echo "✓ gRPC 代码生成完成"

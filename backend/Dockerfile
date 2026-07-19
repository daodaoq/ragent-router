FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /ragent-router ./cmd/server

FROM alpine:3.20
RUN apk --no-cache add ca-certificates
COPY --from=builder /ragent-router /ragent-router
EXPOSE 15722
ENV MOCK_MODE=true
CMD ["/ragent-router"]

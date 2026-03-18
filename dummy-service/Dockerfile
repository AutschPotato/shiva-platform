FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /dummy-service .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /dummy-service /dummy-service
EXPOSE 8090
ENTRYPOINT ["/dummy-service"]

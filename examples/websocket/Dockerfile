### build stage ###
FROM golang:1.23-alpine AS builder

WORKDIR /websocket

COPY . .

RUN go build -o websocket-server /websocket/server/main.go

### run stage ###
FROM alpine:latest
COPY --from=builder /websocket/websocket-server ./websocket-server
CMD ["./websocket-server"]
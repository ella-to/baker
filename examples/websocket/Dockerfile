### build stage ###
FROM golang:1.23-alpine AS builder

WORKDIR /websocket

COPY . .

RUN go build -o server /websocket/main.go

### run stage ###
FROM alpine:latest
COPY --from=builder /websocket/server ./server
CMD ["./server"]
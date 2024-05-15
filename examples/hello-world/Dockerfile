### build stage ###
FROM golang:1.22-alpine AS builder

WORKDIR /helloworld

COPY . .

RUN go build -o server /helloworld/main.go

### run stage ###
FROM alpine:latest
COPY --from=builder /helloworld/server ./server
CMD ["./server"]
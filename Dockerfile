FROM golang:1.20.0-alpine3.17 as builder

WORKDIR /go/src/fc.local/terminator
COPY ./*.go .
COPY ./go.mod .

RUN go mod tidy
RUN go build -ldflags '-w -s' -o terminator .
RUN chmod 755 terminator

FROM scratch

COPY --from=builder /go/src/fc.local/terminator/terminator /usr/local/bin/terminator
ENTRYPOINT [ "terminator" ]
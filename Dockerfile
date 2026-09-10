FROM golang:1.26-alpine as builder
COPY . /workspace
WORKDIR /workspace
RUN mkdir -p /bin && go build -o /bin/intro.nyc

FROM alpine:latest

RUN apk add --no-cache tzdata

COPY --from=builder /bin/ /bin

CMD ["/bin/intro.nyc"]

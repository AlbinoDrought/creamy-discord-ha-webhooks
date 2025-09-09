FROM golang:1.24 as builder
WORKDIR /app
COPY . /app
RUN CGO_ENABLED=0 go get && CGO_ENABLED=0 go test && CGO_ENABLED=0 go build -o /creamy-discord-ha-webhooks

FROM alpine:3.14

RUN apk add --update --no-cache tini ca-certificates
USER 1000
COPY --from=builder /creamy-discord-ha-webhooks /creamy-discord-ha-webhooks
ENTRYPOINT ["tini", "--"]
CMD ["/creamy-discord-ha-webhooks"]

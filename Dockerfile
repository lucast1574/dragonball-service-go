FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN go test ./... && CGO_ENABLED=0 go build -o /api ./cmd/api

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && adduser -D app
COPY --from=build /api /api
USER app
EXPOSE 8080
ENTRYPOINT ["/api"]

FROM golang:1.26-alpine AS build
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o capabot ./cmd/capabot

FROM alpine:3.21
RUN apk add --no-cache git ca-certificates
COPY --from=build /app/capabot /usr/local/bin/capabot
COPY --from=build /app/.air.toml /app/.air.toml
WORKDIR /app
CMD ["capabot", "serve"]

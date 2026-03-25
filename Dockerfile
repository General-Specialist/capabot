FROM golang:1.26-alpine AS build
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o gostaff ./cmd/gostaff

FROM alpine:3.21
RUN apk add --no-cache git ca-certificates
COPY --from=build /app/gostaff /usr/local/bin/gostaff
WORKDIR /app
CMD ["gostaff", "serve"]

# syntax=docker/dockerfile:1
FROM golang:1.22-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG SERVICE=gateway
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/service ./services/${SERVICE}/cmd

FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl
COPY --from=build /out/service /usr/local/bin/lattice
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/lattice"]

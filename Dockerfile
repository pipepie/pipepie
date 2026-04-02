FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o /pie .

FROM alpine:3.21
RUN apk add --no-cache sqlite-libs
COPY --from=builder /pie /usr/local/bin/pie
ENTRYPOINT ["pie"]
CMD ["mcp"]

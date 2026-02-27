FROM golang:1.25-alpine AS builder

RUN apk add --no-cache nodejs npm

WORKDIR /src

COPY package.json package-lock.json ./
RUN npm ci

COPY go.mod go.sum ./
RUN go mod download

RUN go install github.com/a-h/templ/cmd/templ@latest
RUN go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

COPY . .

RUN templ generate
RUN sqlc generate
RUN npx tailwindcss -i input.css -o static/css/styles.css --minify
RUN go build -ldflags="-s -w" -o /bin/shelterkin ./cmd/shelterkin

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S shelterkin \
    && adduser -S shelterkin -G shelterkin

WORKDIR /app

COPY --from=builder /bin/shelterkin /app/shelterkin

RUN mkdir -p /app/data && chown -R shelterkin:shelterkin /app

USER shelterkin

VOLUME ["/app/data"]

EXPOSE 8080

ENTRYPOINT ["/app/shelterkin"]

# Build the PWA into the directory embedded by web/embed.go.
FROM node:22-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Build a static Linux binary so the runtime image needs no libc or shell.
FROM golang:1.26-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web-build /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/retain ./cmd/retain

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /out/retain /app/retain
# Retain supports user-selected IANA time zones for study days and reminders.
COPY --from=go-build /usr/local/go/lib/time/zoneinfo.zip /zoneinfo.zip
ENV DATABASE_PATH=/data/retain.db \
    HTTP_ADDR=:8080 \
    ZONEINFO=/zoneinfo.zip \
    TZ=UTC
WORKDIR /
EXPOSE 8080
ENTRYPOINT ["/app/retain"]

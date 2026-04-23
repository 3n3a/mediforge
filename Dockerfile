FROM golang:1.26.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/mediforge ./cmd/mediforge

FROM alpine:3.20
RUN apk add --no-cache ffmpeg ca-certificates tzdata
COPY --from=build /out/mediforge /usr/local/bin/mediforge
RUN mkdir -p /var/lib/mediforge

# The container is an idle long-running shell. It does NOT dispatch
# automatically on start — trigger work on demand with:
#   docker compose exec mediforge mediforge dispatch
# The mediforge binary is on PATH inside the container.
CMD ["tail", "-f", "/dev/null"]

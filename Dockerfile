FROM alpine:latest

RUN apk add --no-cache \
    openssh-client \
    ffmpeg \
    bash \
    inotify-tools \
    python3

COPY master/ /app/

RUN chmod +x /app/*.sh

ENTRYPOINT ["/app/watch.sh"]

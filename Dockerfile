FROM node:24-alpine AS web
WORKDIR /app/web/ui
COPY web/ui/package.json web/ui/package-lock.json ./
RUN npm ci
COPY web/ui/ ./
RUN npm run build

FROM golang:1.27-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY . ./
COPY --from=web /app/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X 'nuvio/internal/jellyfin.Version=${VERSION}'" \
    -o /out/nuvio ./cmd/nuvio

FROM alpine:3.24
RUN apk add --no-cache ffmpeg ca-certificates tzdata libva libva-utils mesa-va-gallium \
    && if [ "$(apk --print-arch)" = "x86_64" ]; then \
         apk add --no-cache libvpl onevpl-intel-gpu intel-media-driver libva-intel-driver; \
       fi
COPY --from=build /out/nuvio /usr/local/bin/nuvio
VOLUME ["/data"]
EXPOSE 8096
ENTRYPOINT ["nuvio", "-data", "/data", "-addr", ":8096"]

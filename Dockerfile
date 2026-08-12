FROM node:22-alpine AS web-build
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS server-build
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web-build /web/dist ./webui/dist
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o /dockport ./cmd/dockport

FROM docker:29-cli
RUN apk add --no-cache ca-certificates tzdata docker-cli-compose
COPY --from=server-build /dockport /usr/local/bin/dockport
RUN mkdir -p /opt/dockport/data/compose /opt/dockport/data/backups && addgroup -S dockport && adduser -S -G dockport dockport && chown -R dockport:dockport /opt/dockport
EXPOSE 8080
VOLUME ["/opt/dockport/data"]
ENTRYPOINT ["dockport"]

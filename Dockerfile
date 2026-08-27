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
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o /suma ./cmd/suma

FROM docker:29-cli
RUN apk add --no-cache ca-certificates tzdata docker-cli-compose git openssh-client
COPY --from=server-build /suma /usr/local/bin/suma
RUN mkdir -p /opt/suma/data/compose /opt/suma/data/gitops /opt/suma/data/backups && addgroup -S suma && adduser -S -G suma suma && chown -R suma:suma /opt/suma
EXPOSE 8080
VOLUME ["/opt/suma/data"]
ENTRYPOINT ["suma"]

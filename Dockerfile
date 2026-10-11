FROM golang:1.25-alpine AS build
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /git-mcp .

FROM alpine:3.20
RUN apk add --no-cache git \
 && adduser -D -H -u 10001 git-mcp \
 && mkdir -p /data/repos \
 && chown git-mcp:git-mcp /data/repos
COPY --from=build /git-mcp /usr/local/bin/git-mcp
USER git-mcp
VOLUME /data/repos
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --retries=5 \
  CMD wget -qO- http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["git-mcp"]
CMD ["serve", "--config", "/etc/git-mcp/config.yaml"]

FROM golang:1.25-alpine AS build
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /git-mcp .

FROM alpine:3.20
RUN apk add --no-cache git
COPY --from=build /git-mcp /usr/local/bin/git-mcp
VOLUME /data/repos
ENTRYPOINT ["git-mcp"]
CMD ["serve", "--config", "/etc/git-mcp/config.yaml"]

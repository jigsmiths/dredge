FROM node:24-alpine AS web-builder
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /app/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -o /dredge ./cmd/dredge

FROM alpine:latest
RUN apk add --no-cache ca-certificates && mkdir /data && chown 1000:1000 /data

COPY --from=go-builder /dredge /dredge

ENV MUSIC_DIR=/music DB_PATH=/data/dredge.db

USER 1000:1000

VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/dredge"]
CMD ["serve"]

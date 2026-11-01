FROM golang:1.27.3-alpine3.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/uip-api ./cmd/api

FROM alpine:3.23
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata && mkdir -p /data/uploads
COPY --from=build /out/uip-api ./uip-api
COPY --from=build /src/schema.sql ./schema.sql
ENV UPLOAD_DIR=/data/uploads
ENV NODE_ENV=production
EXPOSE 8080
ENTRYPOINT ["/app/uip-api"]

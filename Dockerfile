FROM node:24-alpine AS web

WORKDIR /app/web

COPY package.json package-lock.json ./

RUN npm ci

COPY . .

RUN npm run build

FROM golang:1.26-alpine AS api

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY server ./server

COPY migrations ./migrations

RUN CGO_ENABLED=0 GOOS=linux go build -o /app/chartdb-server ./server

FROM alpine:3.20 AS production

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=web /app/web/dist ./web

COPY --from=api /app/chartdb-server ./

ENV CHARTDB_STATIC_DIR=/app/web PORT=8080

EXPOSE 8080

CMD ["./chartdb-server"]

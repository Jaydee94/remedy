FROM node:lts-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -tags webui -o /out/remedy-server ./cmd/remedy-server
RUN mkdir /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/remedy-server /remedy-server
# The image runs as nonroot (65532). /data must exist and be writable for SQLite; named volumes inherit this.
COPY --from=build --chown=65532:65532 /out/data /data
ENV REMEDY_DB=/data/remedy.db
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/remedy-server"]

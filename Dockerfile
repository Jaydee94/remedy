FROM --platform=$BUILDPLATFORM node:lts-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
# The release passes the version and the commit (release.yml); a build without them says "dev". internal/version reads them.
ARG VERSION=dev
ARG COMMIT=
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags webui -ldflags "-X github.com/Jaydee94/remedy/internal/version.Version=$VERSION -X github.com/Jaydee94/remedy/internal/version.Commit=$COMMIT" -o /out/remedy-server ./cmd/remedy-server
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/remedy-tokenrefresh ./cmd/remedy-tokenrefresh

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/remedy-server /remedy-server
# The Helm chart runs this as the CronJob that keeps the write identity's token fresh.
COPY --from=build /out/remedy-tokenrefresh /remedy-tokenrefresh
# The image keeps no state: the database is PostgreSQL, reached by REMEDY_DATABASE_URL, and it runs as nonroot (65532).
EXPOSE 8080 8081
ENTRYPOINT ["/remedy-server"]

FROM golang:1.26-bookworm AS build

ARG SERVICE
WORKDIR /src/server

COPY server/go.mod server/go.sum ./
RUN go mod download

COPY server/ ./
RUN case "${SERVICE}" in login|gate|coordinator|zone|friend|info|mail) ;; *) exit 2 ;; esac \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
        -o /out/service "./cmd/${SERVICE}"

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/service /service
USER nonroot:nonroot
ENTRYPOINT ["/service"]

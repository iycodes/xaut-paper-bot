# syntax=docker/dockerfile:1
FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/xautbot ./cmd/xautbot

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/xautbot /app/xautbot
COPY configs/config.json /app/configs/config.json
VOLUME ["/app/data"]
EXPOSE 8082
USER nonroot:nonroot
ENTRYPOINT ["/app/xautbot"]
CMD ["-config", "/app/configs/config.json"]

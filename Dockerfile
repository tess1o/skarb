# syntax=docker/dockerfile:1

FROM golang:1.27.3-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out/data && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/skarb ./cmd/skarb

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/skarb /skarb
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV SKARB_DATA_DIR=/data
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/skarb", "healthcheck"]
ENTRYPOINT ["/skarb"]
CMD ["serve"]

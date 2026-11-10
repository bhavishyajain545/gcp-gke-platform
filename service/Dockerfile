# Build
FROM golang:1.27-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" -o /out/url-health-api .

# Run: distroless static image, non-root, read-only filesystem works (no writes at runtime).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/url-health-api /url-health-api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/url-health-api"]

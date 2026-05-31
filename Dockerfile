# syntax=docker/dockerfile:1

# --- build stage ---
# Floating 1.26 tag: always present and ≥1.26.3, carrying current stdlib fixes.
FROM golang:1.26 AS build
WORKDIR /src

# Cache modules first.
COPY go.mod go.sum ./
RUN go mod download

# Build a static binary so the final image can be distroless/scratch-small.
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pcloud-mcp ./cmd/pcloud-mcp

# --- runtime stage ---
# Distroless nonroot: no shell, no package manager, runs as uid 65532.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /home/nonroot

# Credentials live under the user config dir; point it at a writable, mountable path.
ENV XDG_CONFIG_HOME=/home/nonroot/.config

COPY --from=build /out/pcloud-mcp /usr/local/bin/pcloud-mcp

# Already nonroot via the base image; declare it explicitly for clarity.
USER nonroot:nonroot

ENTRYPOINT ["pcloud-mcp"]
CMD ["serve"]

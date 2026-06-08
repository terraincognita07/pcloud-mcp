# syntax=docker/dockerfile:1

# --- build stage ---
# Floating 1.26 tag: always present; if its Go is older than go.mod's toolchain
# pin (1.26.4) the build fetches the pinned release, so stdlib fixes are assured.
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

# HTTP transport port (only meaningful for `serve --http`). Documentation only.
EXPOSE 8080

# Containers are the remote scenario, so default to authenticated HTTP. This
# requires PCLOUD_MCP_TOKEN in the environment and credentials mounted at
# $XDG_CONFIG_HOME/pcloud-mcp/credentials.json (created once via `pcloud-mcp auth`
# on a machine with a browser, then copied in). For the local stdio scenario,
# override the command with: `serve`.
ENTRYPOINT ["pcloud-mcp"]
CMD ["serve", "--http", ":8080"]

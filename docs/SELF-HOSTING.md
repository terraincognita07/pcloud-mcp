# Self-hosting pcloud-mcp for Claude.ai (web / phone)

The default `pcloud-mcp serve` speaks MCP over **stdio** — Claude Desktop launches
it locally and files land on your own machine. That is the right mode for a
laptop. See the [README](../README.md) for it.

This guide is the other mode: **`serve --http`**, an authenticated HTTP server
you run 24/7 so **Claude.ai in a browser or on your phone** can reach your
pCloud from anywhere.

## What works remotely (and what doesn't)

A hosted server runs on *its* disk, not yours, so the local-filesystem tools are
**hidden in HTTP mode** (`download_file`, `download_folder`, `upload_file`). What
remains is everything that makes sense remotely:

- browse, search, organize (list / create folder / move / rename / delete)
- **share a file** → get a link you open on the phone to download
- **save text** → Claude writes a note/doc straight into pCloud
- **create an upload link** → open it on the phone to upload photos/PDFs into a folder

To move a binary file between clouds you still go through links or a machine with
the local tools; an MCP tool argument cannot carry file bytes.

## Security model (read this)

This server holds a **full-access pCloud OAuth token** and will be reachable from
the internet. Two controls stand between the world and your account:

1. **Bearer token** — every request must send `Authorization: Bearer <PCLOUD_MCP_TOKEN>`.
   Missing/wrong → `401`. The server refuses to start without a token. Use a long
   random secret: `openssl rand -hex 32`.
2. **HTTPS via a reverse proxy** — the container binds to `127.0.0.1` only; the
   proxy terminates TLS and forwards locally. Never expose the port to `0.0.0.0`.

Treat `PCLOUD_MCP_TOKEN` and `credentials.json` as secrets. Rotate the token by
changing the env var and restarting; revoke pCloud access entirely from your
pCloud account's app settings.

> Note on auth choice: this is a single-user, self-hosted server, so a bearer
> token is the best-practice fit. The MCP spec's OAuth 2.1 flow targets
> multi-user/resource-server deployments and needs a separate authorization
> server; it can be layered on later without changing the tools.

## Step 1 — One-time OAuth (on a machine with a browser)

The OAuth consent needs a browser, so do this on your laptop, then copy the
result to the server.

```sh
export PCLOUD_CLIENT_ID=xxxxxxxx
export PCLOUD_CLIENT_SECRET=yyyyyyyy
pcloud-mcp auth
```

This writes `credentials.json` under your user config dir
(`~/.config/pcloud-mcp/` on Linux). Copy that file next to `docker-compose.yml`
on the server.

## Step 2 — Set the token and start

```sh
echo "PCLOUD_MCP_TOKEN=$(openssl rand -hex 32)" > .env
docker compose up -d
docker compose logs -f       # expect: "serving over authenticated HTTP"
```

The service now listens on `127.0.0.1:8080` on the server. Keep the token from
`.env` — you'll give it to Claude.ai.

## Step 3 — Put HTTPS in front

Pick the proxy you already run. All three forward to `127.0.0.1:8080`.

### Caddy

```caddyfile
pcloud-mcp.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

### nginx

```nginx
server {
    listen 443 ssl;
    server_name pcloud-mcp.example.com;

    ssl_certificate     /etc/letsencrypt/live/pcloud-mcp.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/pcloud-mcp.example.com/privkey.pem;

    location / {
        proxy_pass         http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Forwarded-For   $remote_addr;
        proxy_set_header   X-Forwarded-Proto $scheme;
        # MCP streaming responses: don't buffer, allow long-lived connections.
        proxy_buffering    off;
        proxy_read_timeout 1h;
    }
}
```

### Traefik (labels on the compose service)

Add to the `pcloud-mcp` service in `docker-compose.yml` and attach it to your
Traefik network:

```yaml
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.pcloudmcp.rule=Host(`pcloud-mcp.example.com`)"
      - "traefik.http.routers.pcloudmcp.entrypoints=websecure"
      - "traefik.http.routers.pcloudmcp.tls.certresolver=letsencrypt"
      - "traefik.http.services.pcloudmcp.loadbalancer.server.port=8080"
```

> Do **not** put a human-login gateway (e.g. Authelia/OAuth2-Proxy) in front of
> this route. Claude is not a browser and cannot complete a login form; the
> bearer token is the access control here. Keep such gateways for your
> human-facing services.

## Step 4 — Connect Claude.ai

In Claude.ai → Settings → Connectors → add a custom MCP server:

- **URL:** `https://pcloud-mcp.example.com` (the path the SDK serves; try the
  root, and `/mcp` if your setup expects it)
- **Authorization header:** `Bearer <the PCLOUD_MCP_TOKEN from .env>`

Then ask Claude: *"list my pCloud root folder"* to confirm.

### Uploading a file from a chat (optional)

A file you attach in a Claude.ai chat lives in Claude's code-execution sandbox,
not in a tool argument. To push it to this server you must allowlist the server's
URL under Claude.ai → Settings → Capabilities → *Additional allowed domains*, so
the sandbox may `curl` it. Without that, use the **upload link** tool instead.

## Updating

```sh
docker compose pull   # if using a published PCLOUD_MCP_IMAGE
docker compose up -d --build
```

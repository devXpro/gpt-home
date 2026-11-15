# Existing Traefik

Traefik proxies WebSockets without extra options. Give the `gpt-home-frps`
container labels. Put them in a `docker-compose.override.yml` next to
`docker-compose.yml` in the install dir, so `server.sh` leaves them alone.

```yaml
services:
  frps:
    labels:
      - traefik.enable=true
      - traefik.docker.network=proxy
      - traefik.http.routers.gpt-home.rule=Host(`homie.example.com`)
      - traefik.http.routers.gpt-home.entrypoints=websecure
      - traefik.http.routers.gpt-home.tls.certresolver=letsencrypt
      - traefik.http.services.gpt-home.loadbalancer.server.port=7000
```

Adjust `proxy` (the docker network shared with Traefik), `websecure` (your TLS
entrypoint) and `letsencrypt` (your certificate resolver) to your setup.

Traefik reloads its dynamic config without closing established connections, so
the channel survives. Check that no middleware (rate limit, in-flight request
limit, buffering) sits on this router: the WebSocket is one long request.

# Huifu Relay Worker

This Worker is an optional network relay for the Huifu production API. It is
not an open proxy: it accepts only the three payment paths used by the
application, requires a shared token, and requires the server egress IP.

## Cloudflare configuration

1. Create a Worker and paste `huifu-relay.js` as the entrypoint.
2. Bind a custom domain, for example `huifu-relay.prompt.osh.lol`.
3. Add a Worker secret named `HUIFU_RELAY_TOKEN`.
4. Add a Worker variable named `ALLOWED_CLIENT_IP` with value
   `43.242.200.72`.
5. Do not enable caching for this Worker.

The backend must keep sending the same `X-OSH-Huifu-Proxy-Token` value as the
Worker secret. Set `PAY_HUIFU_BASE_URL` to the Worker custom domain. Keep
`PAY_HUIFU_NOTIFY_URL` pointed at the original backend callback URL.

The Worker egress IP is owned by Cloudflare and is not a substitute for a
fixed-IP proxy when Huifu requires a fixed source-IP allowlist. Confirm this
relay approach with Huifu before using it for production payments.

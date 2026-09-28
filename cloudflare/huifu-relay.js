const HUifu_ORIGIN = "https://api.huifu.com";
const ALLOWED_PATHS = new Set([
  "/v2/trade/payment/jspay",
  "/v2/trade/payment/scanpay/query",
  "/v2/trade/payment/scanpay/close",
]);
const MAX_BODY_BYTES = 256 * 1024;

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    if (request.method !== "POST" || !ALLOWED_PATHS.has(url.pathname)) {
      return new Response("Not found", { status: 404 });
    }

    const contentLength = Number(request.headers.get("content-length") || 0);
    if (contentLength > MAX_BODY_BYTES) {
      return new Response("Request too large", { status: 413 });
    }

    const expectedToken = env.HUIFU_RELAY_TOKEN || "";
    const receivedToken = request.headers.get("X-OSH-Huifu-Proxy-Token") || "";
    if (!expectedToken || receivedToken !== expectedToken) {
      return new Response("Unauthorized", { status: 401 });
    }

    const expectedClientIP = env.ALLOWED_CLIENT_IP || "";
    const clientIP = request.headers.get("CF-Connecting-IP") || "";
    if (!expectedClientIP || clientIP !== expectedClientIP) {
      return new Response("Forbidden", { status: 403 });
    }

    const target = new URL(HUifu_ORIGIN);
    target.pathname = url.pathname;
    target.search = url.search;

    const headers = new Headers(request.headers);
    headers.delete("host");
    headers.delete("content-length");
    headers.delete("X-OSH-Huifu-Proxy-Token");
    headers.set("Cache-Control", "no-store");

    try {
      const response = await fetch(new Request(target, {
        method: "POST",
        headers,
        body: request.body,
        redirect: "manual",
      }));

      const responseHeaders = new Headers(response.headers);
      responseHeaders.set("Cache-Control", "no-store");
      return new Response(response.body, {
        status: response.status,
        headers: responseHeaders,
      });
    } catch {
      return new Response("Upstream unavailable", { status: 502 });
    }
  },
};

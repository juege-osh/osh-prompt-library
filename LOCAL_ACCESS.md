# Local Startup and Resource Access

Run from this directory:

```powershell
go run . --port 18766
```

The application automatically reads `.env` using godotenv. Existing process
environment variables take priority. Set `R2_ACCESS_KEY_ID` and
`R2_SECRET_ACCESS_KEY` together. IDE run configurations must use this project as
the working directory. The startup log reports whether R2 is configured, without
printing credentials. `catalog.sqlite3` and `images_catalog.sqlite3` are read-only
indexes; neither contains the original video/image files. Without R2 credentials,
media requires the source files under the parent directory.

## Access and Pricing

- Public: catalog metadata, video posters and compressed image previews.
- Level 1 and 2: base prices are video 5 points and image 1 point.
- Actual cost = base price * administrator multiplier (0.10-1.00, default 1.00).
- Points have two decimal places. A 0.10 multiplier costs 0.50/video or 0.10/image.
- Level 3: all resources are accessible without spending points.
- An unlock includes the resource's prompt, original media, and downloads.
- Unlocks belong to the account and have no expiry. Repeat access is free,
  including after the remaining balance reaches zero or VIP expires.
- Blocked, deleted or logged-out users cannot retrieve protected resources.
- VIP supplies its monthly quota; it does not bypass resource charges.

`GET /api/resources/{video|image}/{id}/access` returns price and access state.
`POST /api/resources/{video|image}/{id}/unlock` requires a logged-in session and
`Content-Type: application/json` and `{"price_version":1}` (use the version
returned by the access endpoint). The price is selected on the server.
Stale or missing versions return 409 without charging; fetch access again and
ask the user to confirm the new price. Previously owned resources remain free.
The debit and `resource_unlocks` record are committed in one SQLite transaction.
Duplicate requests do not debit again. Missing resources and insufficient funds
do not create an unlock. Consumption uses expiring lots first.

Prompt detail APIs and original media URLs require an unlock or level 3.
They return 401 for unauthenticated requests and 402 for locked resources.
GET/HEAD/Range requests never initiate a debit. R2 media is streamed through the
backend with Range support; signed R2 URLs and credentials are not returned to
the browser. Protected responses are `private, no-store`.

Public image gallery previews use the existing compressed thumbnails. Reference
image previews are resized to at most 320 pixels and encoded as quality-35 JPEG;
the originals are not exposed as a fallback. Generated previews are cached in
the ignored `.media-cache` directory.

The local `app.sqlite3` holds accounts, points, unlocks, business orders, payment
orders and notification logs. Payment is disabled when `PAY_PROVIDER` is missing;
set `PAY_PROVIDER=mock` for local mock settlement without external credentials.
Set `PAY_PROVIDER=huifu`
only after mounting the Huifu PKCS#8 private key and platform X.509 public key,
and configuring a public notification URL. Never put those files or values in Git.

## Workspace and Pricing

- `/`: video catalog; `/images.html`: image catalog.
- `/assets`: purchased assets with video/image filters and pagination.
- `/account`: profile; `/account?view=orders`: orders.
- `/account?view=console`: admin console, hidden for levels 1 and 2.
- `/member`: standalone membership center. Creates membership/recharge payment orders;
  local mock mode can settle them from the payment dialog.
- `GET /api/assets?kind=all|video|image&page=1`: current user's purchases, 24/page.
- `GET /api/user/usage?days=10`: authenticated user's quota, lifetime video/image
  purchase counts and historical charges, plus daily charges for 1-10 days.
  The window includes today, grouped by Asia/Shanghai (UTC+8), with missing
  dates filled with zero. Counts are based on unique unlocks, not repeat access
  or per-lot ledger entries. Administrator free access is not a purchase.
  The quota denominator includes available and exhausted grant lots that have
  not expired; paid points and expired grants are excluded. The profile uses
  this quota progress bar, a purchase-count doughnut and two consumption lines.
- `GET /api/pricing`: public multiplier, version, effective video/image prices.
- `GET /api/admin/pricing`: same information, level 3 only.
- `POST /api/admin/pricing`: level 3 only; JSON `{"multiplier":0.1,"version":1}`.
  Origin checks, optimistic version checks and `pricing_changes` audit apply.
- `GET /api/payments/products`: payment provider, membership plans and fixed recharge packages.
- `GET /api/admin/payment-prices`: level 3 only; returns editable membership and recharge prices.
- `POST /api/admin/payment-prices`: level 3 only; JSON `{"kind":"plan|package","code":"...","price_cents":100}`.
  Prices are stored in cents and affect new orders only.
- `POST /api/payments/orders`: level 1; JSON contains only `order_type` plus a server-known
  `plan_code` or `package_code`, and optional `channel` (`alipay` or `wechat`). Amounts are
  loaded from SQLite and cannot be submitted by the browser.
- `GET /api/payments/{orderNo}`: level 1; returns the current order/payment status and
  reconciles a pending Huifu payment through the provider query API.
- `POST /api/payments/{orderNo}/cancel`: level 1; cancels a pending order after provider
  close succeeds. `POST /api/payments/{orderNo}/mock-success` exists only in mock mode.
- `POST /api/payments/notify/huifu`: public form callback; verifies the original
  `resp_data` string with RSA2 before processing success/failure notifications.

On first startup, `point_hundredths_v1` atomically converts all point amounts
in balances, lots, ledgers, plans, cycles, orders and unlocks to integer
hundredths. API values remain human-readable decimal points. Prices in cents
are unchanged. Back up `app.sqlite3` with SQLite's backup API or `VACUUM INTO`
before this upgrade. Do not run older binaries against the migrated database.
For direct SQL adjustments, 1 point is now stored as 100 units.
The migration marker prevents a second conversion on restart.

Use `go run . --port 18767 --app-db .local/qa.sqlite3` for isolated account QA.
Catalogs remain read-only; never use the real account database for test users.

## Verification

```powershell
go test ./... -count=1
go build ./...
node --check account.js
node --check app.js
node --check images.js
```

Tests use temporary SQLite databases, not the local account database.

## Account Changes

Registration grants 888 non-expiring **recharge points** (`point_type=paid`,
`source_type=register_gift`), not VIP quota. The gift keeps its own lot and ledger
entry and is not a paid order. VIP monthly quota remains 1288/1888/3888 and is
spent before non-expiring recharge points.

### Email Verification

Set these in the ignored local `.env` file; never commit SMTP credentials:

```dotenv
SMTP_HOST=smtp.163.com
SMTP_PORT=465
SMTP_SECURITY=tls
SMTP_USER=your-sender@163.com
SMTP_PASSWORD=your-smtp-authorization-code
SMTP_FROM=your-sender@163.com
```

Use the provider's SMTP authorization code, not its web login password. Port 465
uses implicit TLS; other ports default to required STARTTLS. Server certificates
are verified with the operating system trust store. Do not disable validation to
work around an untrusted certificate. SMTP operations time out after 30 seconds.
Restart the application after editing `.env`.

`POST /api/auth/email-code` accepts `email` and `purpose` (`register`,
`reset_password`, `change_email`). Changing email requires a valid session.
Codes are six digits, valid for 10 minutes, stored as hashes and consumed in the
same transaction as the account change. Five incorrect attempts invalidate a
code. A successful resend replaces previous codes for the same email/purpose.
Failed or pending sends cannot be used. Responses never include plaintext codes,
even when SMTP is unconfigured (503); SMTP failure returns 502. Success confirms
SMTP acceptance, not inbox placement. Check the recipient's spam folder too.

Limits persist in SQLite: 60 seconds between requests per email (all purposes),
5 requests per email/hour and 20 per client IP/hour. HTTP 429 includes
`Retry-After`; the frontend displays a resend countdown. The IP is taken from the
TCP peer, not untrusted forwarded headers; a reverse proxy shares this limit
until explicit trusted-proxy handling is configured. Failed deliveries count
toward these limits as well.

Registration and password reset require matching `password` and
`confirm_password`. Password changes require `old_password`, `new_password`
and a matching `confirm_password`. Confirmation is validated in both the browser
and backend. Logging out asks for confirmation before revoking the session.

Account cancellation sets `users.delete_flag=1` and records `deleted_at`.
It revokes all sessions and invalidates outstanding email codes in the same
transaction. The user row, memberships, orders, point history and resource
unlocks are retained. Cancelled accounts cannot log in, reset their password,
access resources or be selected for administrator account operations.
Active-account queries and unique indexes use `delete_flag=0`. Re-registering
with a cancelled account's email or username creates a separate account;
previous entitlements are not transferred. Existing databases are migrated at
startup, including older rows already marked with `deleted_at`.

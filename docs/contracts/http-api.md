---
status: accepted
version: 1
date: 2026-07-30
owners:
  - project-owner
related:
  - ../architecture/stateful-zone-v3-architecture.md
  - websocket-protocol.md
  - idempotency-and-errors.md
---

# HTTP Authentication and Bootstrap Contract V1

## 1. Scope and authority

This is the normative English contract for first-stage account registration, login, HTTP Session management, one-time WebSocket ticket issuance, Gateway discovery, and versioned client-configuration bootstrap.

It defines the public H5-to-LoginSvr HTTP boundary only. Game commands remain on the Client-to-GateSvr WebSocket; player state, Shard routing, Dirty checkpoints, and internal RPC are outside this contract.

Normative words `MUST`, `MUST NOT`, `SHOULD`, and `MAY` retain their usual requirements meaning. Published Protobuf field numbers and enum values MUST NOT be reused for another meaning. The Chinese file is a reading mirror; this English file wins if they differ.

The four security objects are distinct:

- an **HTTP Session** is an opaque, revocable server-side login record referenced only by an HttpOnly cookie;
- a **`ws_ticket`** is a short-lived bearer secret issued through an active HTTP Session and consumed once;
- **WebSocket connection authentication** occurs only when GateSvr atomically consumes that ticket during `AUTH`;
- **client configuration** is a public, immutable, hash-verified display package and is neither authentication nor transaction authority.

## 2. Transport and scalar conventions

### 2.1 HTTP and Protobuf

- Production endpoints use HTTPS only. TLS 1.2 or newer is required and TLS 1.3 is preferred.
- API request and response bodies use binary Protobuf with `Content-Type: application/x-protobuf`.
- Clients send `Accept: application/x-protobuf`. Unsupported request media types return `415`; an unacceptable response type returns `406`.
- A body-bearing request MUST contain exactly one complete message. Trailing bytes, malformed wire data, duplicate singular fields with conflicting values, and non-canonical semantic values are rejected.
- Requests to authentication endpoints MUST NOT use `Content-Encoding`. The immutable config object also uses identity encoding so its digest always covers the received body bytes.
- API request bodies are limited to 16 KiB. The config object is limited to 2 MiB. Limits are enforced before unbounded allocation.
- GET requests have no request body. `HEAD`, `204`, and `304` responses are the only successful bodyless exceptions.
- API responses, including errors, use `Cache-Control: no-store`, except the immutable client-config object.
- Unknown Protobuf fields are ignored and preserved where the selected runtime supports it. Known fields still undergo all semantic validation.

### 2.2 Scalars

| Meaning | Protobuf type | Rule |
|---|---|---|
| `player_id` | `uint64` | H5 uses generated `bigint` or decimal string, never JavaScript `number` |
| versions | `uint64` | Opaque equality tokens |
| time | `int64` | Unix milliseconds UTC; server time is authoritative |
| hash | `bytes` | Raw digest bytes, not hexadecimal text |
| request/ticket issue ID | `string` | Canonical lowercase UUID |
| account name | `string` | 3–32 lowercase ASCII characters; first is `a-z`, remaining are `a-z`, `0-9`, or `_` |

Passwords are 12–128 Unicode scalar values, MUST NOT be trimmed or Unicode-normalized, and MUST be compared only through the password verifier. NUL and invalid UTF-8 are rejected.

### 2.3 Correlation

The client MAY send `X-Request-ID` as a canonical lowercase UUID. The server MUST return an `X-Request-ID` on every API response, preserving a valid supplied value or generating one. It is logged as correlation metadata, is not an idempotency key, and MUST NOT contain or be treated as a secret.

## 3. Endpoints

| Method | Path | Authentication | CSRF | Success |
|---|---|---|---|---:|
| GET | `/v1/auth/csrf` | none | origin check | 200 |
| POST | `/v1/auth/register` | none | required | 201 |
| POST | `/v1/auth/login` | none | required | 200 |
| GET | `/v1/auth/session` | Session | not required | 200 |
| POST | `/v1/auth/logout` | optional Session | required | 200 |
| POST | `/v1/ws-tickets` | Session | required | 201 |
| GET | `/v1/gateways` | Session | not required | 200 |
| GET | `/v1/bootstrap` | Session | not required | 200 |

No endpoint accepts credentials, Session IDs, CSRF tokens, or tickets in a URL or query string.

## 4. Common messages and errors

`SessionView`:

| Tag | Field | Type | Rule |
|---:|---|---|---|
| 1 | `player_id` | `uint64` | authenticated player |
| 2 | `account_name` | `string` | canonical account name |
| 3 | `created_at_ms` | `int64` | Session creation |
| 4 | `idle_expires_at_ms` | `int64` | current idle deadline |
| 5 | `absolute_expires_at_ms` | `int64` | non-extendable deadline |

`HttpErrorCode`:

| Value | Name | Meaning |
|---:|---|---|
| 0 | `HTTP_ERROR_UNSPECIFIED` | invalid default |
| 100 | `INVALID_ARGUMENT` | malformed or semantically invalid request |
| 101 | `UNSUPPORTED_MEDIA_TYPE` | request body is not binary Protobuf |
| 102 | `NOT_ACCEPTABLE` | client does not accept binary Protobuf |
| 103 | `PAYLOAD_TOO_LARGE` | decoded/body limit exceeded |
| 200 | `UNAUTHENTICATED` | Session absent, expired, or revoked |
| 201 | `INVALID_CREDENTIALS` | generic account-name/password failure |
| 202 | `FORBIDDEN` | authenticated principal lacks permission |
| 203 | `CSRF_REJECTED` | origin or CSRF proof failed |
| 204 | `ACCOUNT_NAME_UNAVAILABLE` | registration name cannot be used |
| 300 | `GATEWAY_NOT_FOUND` | unknown or unavailable selected Gateway |
| 301 | `TICKET_REQUEST_CONFLICT` | issue ID reused with different semantics |
| 302 | `TICKET_REPLAY_EXPIRED` | retained ticket result can no longer be replayed |
| 303 | `CLIENT_CONFIG_UNAVAILABLE` | no compatible published config |
| 400 | `RATE_LIMITED` | admission rejected; retry later |
| 500 | `SERVICE_UNAVAILABLE` | temporary dependency or service failure |
| 501 | `INTERNAL_ERROR` | unexpected server failure |

`HttpError`:

| Tag | Field | Type | Rule |
|---:|---|---|---|
| 1 | `code` | `HttpErrorCode` | stable machine-readable value |
| 2 | `params` | repeated `ErrorParam` | localization parameters only |
| 3 | `retryable` | `bool` | automatic retry is permitted |
| 4 | `retry_after_ms` | optional `uint32` | minimum delay when applicable |
| 5 | `correlation_id` | `string` | equals response `X-Request-ID` |
| 6 | `debug_message` | optional `string` | development only; absent in production and never displayed |

`ErrorParam` has `key` tag 1 and `value` tag 2, both strings. Parameters MUST NOT contain passwords, cookies, CSRF values, tickets, account existence facts hidden by generic errors, internal addresses, or stack traces. Every non-2xx API response with a body contains exactly one `HttpError`.

HTTP status mapping:

| Status | Use |
|---:|---|
| 400 | malformed Protobuf semantics or `INVALID_ARGUMENT` |
| 401 | `UNAUTHENTICATED` or generic `INVALID_CREDENTIALS`; clear an invalid Session cookie |
| 403 | `FORBIDDEN` or `CSRF_REJECTED` |
| 406 | `NOT_ACCEPTABLE` |
| 409 | account-name, ticket issue-ID, or current-resource conflict |
| 413 | `PAYLOAD_TOO_LARGE` |
| 415 | `UNSUPPORTED_MEDIA_TYPE` |
| 429 | `RATE_LIMITED`, with `Retry-After` and `retry_after_ms` |
| 500 | `INTERNAL_ERROR` |
| 503 | `SERVICE_UNAVAILABLE` or `CLIENT_CONFIG_UNAVAILABLE`, optionally with `Retry-After` |

Unknown routes may return 404. Authentication failures MUST NOT distinguish unknown account, wrong password, disabled account, or mismatched password-hash version by status, code, body size class, or intentional timing.

## 5. CSRF, Origin, and CORS

### 5.1 CSRF bootstrap

`GET /v1/auth/csrf` returns `CsrfResponse`:

| Tag | Field | Type |
|---:|---|---|
| 1 | `csrf_token` | `string` |
| 2 | `expires_at_ms` | `int64` |

It also sets the non-HttpOnly `__Host-cf_csrf` cookie. The token contains at least 256 bits of entropy, is Base64url without padding, expires after 2 hours, and rotates after successful registration/login and periodically thereafter.

Every POST requires all of:

1. an `Origin` exactly matching a configured H5 origin;
2. `Sec-Fetch-Site` equal to `same-origin`, or `same-site` only for an explicitly configured same-site split-origin deployment, when the browser supplies it;
3. `X-CSRF-Token` exactly matching the CSRF cookie;
4. a server-valid signed token bound to the browser CSRF nonce and, when authenticated, the current Session generation.

Missing `Origin` is rejected for browser API requests. Non-browser clients require an explicitly configured trusted-client policy; production MUST NOT silently exempt them.

### 5.2 CORS

The default deployment is same-origin and emits no CORS headers. If H5 and API origins are split, they MUST remain same-site so `SameSite=Strict` cookies work; the server uses an explicit exact-origin allowlist, `Access-Control-Allow-Credentials: true`, and `Vary: Origin`. Wildcard origins are forbidden. Preflight permits only the documented methods and `Content-Type`, `Accept`, `X-CSRF-Token`, and `X-Request-ID`. A CORS decision never replaces CSRF validation.

## 6. Registration, login, and passwords

`RegisterRequest`:

| Tag | Field | Type |
|---:|---|---|
| 1 | `account_name` | `string` |
| 2 | `password` | `string` |

`RegisterResponse` contains `session` (`SessionView`) at tag 1.

Registration canonicalizes the account name to lowercase before uniqueness checking. The registration result is externally atomic: a 201 response is allowed only after the account is `ACTIVE` and the returned Session is valid. Initial Player checkpoint / farm resources are NOT created by LoginSvr; the current Owner Zone creates them on first Player Actor activation when Load returns NotFound. Any registration failure MUST expose no usable partially provisioned account or Session to H5. An internal partially provisioned account MUST NOT authenticate or appear through Session inspection, and its provisioning MUST be safely retryable and reconcilable.

The local prototype MAY satisfy this guarantee with one MySQL transaction when account and Player records are co-located. A production deployment that separates account and Player shards MUST use a separately defined idempotent provisioning state machine with Outbox and reconciliation semantics; this HTTP contract does not require or imply a local transaction shared by those databases.

Success rotates CSRF, sets the Session cookie, and returns 201. An unavailable or reserved name returns 409 `ACCOUNT_NAME_UNAVAILABLE`; it MUST NOT reveal an existing player's ID, profile, or internal provisioning state.

`LoginRequest`:

| Tag | Field | Type |
|---:|---|---|
| 1 | `account_name` | `string` |
| 2 | `password` | `string` |

`LoginResponse` contains `session` (`SessionView`) at tag 1.

On success, login creates a new Session generation, revokes every older Session for that account, invalidates all of their unconsumed tickets, and causes GateSvr to close their authenticated connections with WebSocket close code 4409. Revocation MUST become authoritative before the new login response is sent. This is duplicate-login revocation, not multi-device support.

Invalid login always returns 401 `INVALID_CREDENTIALS`. The server performs a bounded dummy password verification when the account is absent and uses constant-time verifier comparisons where applicable.

Passwords MUST be hashed with Argon2id, a unique cryptographically random salt of at least 16 bytes, and an output of at least 32 bytes. The production starting profile is memory 64 MiB, iterations 3, parallelism 1; parameters and hash version are stored per account and upgraded after a successful login when policy increases. A server-held pepper SHOULD be kept outside the database and versioned for rotation. Plaintext passwords, reversible encryption, logs, traces, analytics, and idempotency stores MUST NOT contain passwords. Resource parameters MUST be benchmarked against login capacity and denial-of-service limits but MUST NOT be reduced below Argon2id 19 MiB, 2 iterations, parallelism 1 without an accepted replacement decision.

## 7. HTTP Session

The production cookie is:

```text
__Host-cf_session=<opaque random value>; Secure; HttpOnly; SameSite=Strict; Path=/
```

It has no `Domain` attribute. The value has at least 256 bits of entropy and carries no player ID, role, expiry, or other client-readable authority. Only a keyed digest is stored server-side. Session IDs are rotated on login and privilege/authentication changes.

Session defaults:

- idle lifetime: 12 hours;
- absolute lifetime: 7 days from creation;
- authenticated activity may extend the idle deadline up to the absolute deadline;
- persistent refresh writes SHOULD be coalesced to at most once per 5 minutes;
- expiry or revocation invalidates ticket issuance immediately and requires authenticated WebSockets to close with 4401 or 4409 as appropriate.

`GET /v1/auth/session` returns `SessionResponse`, containing `session` at tag 1 and `server_time_ms` at tag 2. It returns 401 and clears the cookie when the Session is absent, expired, or revoked.

`POST /v1/auth/logout` has an empty `LogoutRequest`. `LogoutResponse` has `logged_out` (`bool`) at tag 1. Logout is idempotent: with valid CSRF proof it revokes the current Session if present, invalidates unconsumed tickets, closes its WebSockets with 4401, clears both cookies, and returns 200 with `logged_out = true`.

Session state and cookies MUST NOT be cached. Session lookup, revocation, and ticket consumption require a consistency level that does not accept a known older Session generation.

## 8. Gateway discovery

`GatewayEndpoint`:

| Tag | Field | Type | Rule |
|---:|---|---|---|
| 1 | `gateway_id` | `string` | stable opaque deployment ID |
| 2 | `websocket_url` | `string` | production `wss://` URL; no ticket in URL |
| 3 | `region` | `string` | routing hint, not authorization |
| 4 | `priority` | `uint32` | lower value attempted first |
| 5 | `expires_at_ms` | `int64` | discovery record freshness |

`GatewayDiscoveryResponse`:

| Tag | Field | Type |
|---:|---|---|
| 1 | `gateways` | repeated `GatewayEndpoint` |
| 2 | `server_time_ms` | `int64` |

`GET /v1/gateways` returns at least one currently eligible Gateway or 503. The client orders by priority and MAY fail over to another returned endpoint, but MUST request a ticket for the Gateway it will use. Gateway URLs MUST be from a server-controlled allowlist; clients MUST NOT follow a discovered URL to an untrusted scheme or inject their own Gateway ID.

## 9. One-time WebSocket tickets

`WsTicketRequest`:

| Tag | Field | Type | Rule |
|---:|---|---|---|
| 1 | `ticket_request_id` | `string` | canonical lowercase UUID |
| 2 | `gateway_id` | `string` | one currently discovered Gateway |

`WsTicketResponse`:

| Tag | Field | Type |
|---:|---|---|
| 1 | `ws_ticket` | `string` |
| 2 | `expires_at_ms` | `int64` |
| 3 | `gateway_id` | `string` |

Ticket rules:

- the value is an opaque Base64url bearer secret with at least 256 bits of entropy and at most 128 characters;
- plaintext is returned only in this response and MUST NOT be logged, placed in a URL, persisted by H5 beyond connection setup, or sent anywhere except the WebSocket `AuthRequest`;
- lifetime is 30 seconds and is not extended;
- it is bound to `player_id`, Session ID and generation, selected `gateway_id`, and issue record;
- GateSvr consumes it atomically with a compare-and-set from unused to consumed;
- consumption succeeds only when the ticket is unused, unexpired, for that Gateway, and its Session remains active;
- successful consumption fixes `caller_player_id` for the connection; client message fields can never replace it;
- every failed replay, expiry, wrong-Gateway use, or revoked-Session use fails authentication and exposes no reason beyond the generic WebSocket authentication behavior;
- logout, expiry, and duplicate login invalidate all unused tickets of that Session.

For one Session, issuing a new `ticket_request_id` invalidates any older unused ticket before returning the new one. Repeating the same issue ID with the same Gateway within 30 seconds returns the same ticket and expiry without creating another record; the service may retain it encrypted or derive it securely for this replay. Reusing that ID with another Gateway returns 409 `TICKET_REQUEST_CONFLICT`. Once consumed or expired, replay returns 409 `TICKET_REPLAY_EXPIRED`; the client refreshes Session/discovery as needed and uses a new UUID.

Ticket issuance success does not authenticate a WebSocket. `AUTH` MUST still be the first non-heartbeat WebSocket request within 10 seconds, as defined by `websocket-protocol.md`.

## 10. Client bootstrap and immutable config

`AuthBootstrap` has exactly the fields shared with successful WebSocket `AuthResponse`:

| Tag | Field | Type | Rule |
|---:|---|---|---|
| 1 | `player_id` | `uint64` | Session player |
| 2 | `heartbeat_interval_ms` | `uint32` | V1 default `30000` |
| 3 | `client_config_version` | `uint64` | required display-config version |
| 4 | `client_config_url` | `string` | immutable HTTP(S) object URL |
| 5 | `client_config_sha256` | `bytes` | SHA-256 of exact object body bytes |
| 6 | `protocol_min` | `uint32` | minimum accepted WebSocket protocol |
| 7 | `protocol_max` | `uint32` | maximum accepted WebSocket protocol |

`ClientBootstrapResponse`:

| Tag | Field | Type |
|---:|---|---|
| 1 | `auth_bootstrap` | `AuthBootstrap` |
| 2 | `gateways` | repeated `GatewayEndpoint` |
| 3 | `server_time_ms` | `int64` |

`GET /v1/bootstrap` is a convenience preflight combining current Session identity, Gateway discovery, protocol compatibility, and config publication. The nested `AuthBootstrap` MUST use the exact names, types, and meanings above. A successful WebSocket `AuthResponse` remains authoritative for that connection and MUST return those same seven logical fields. If publication changes between HTTP bootstrap and WebSocket AUTH, the newer AUTH values win; the client verifies and loads that version before game rendering. This contract does not add fields to or replace the accepted WebSocket AUTH contract.

The `client_config_url` points to a binary-Protobuf `ClientConfigPackage`:

| Tag | Field | Type | Meaning |
|---:|---|---|---|
| 1 | `schema_version` | `uint32` | package schema; V1 is 1 |
| 2 | `client_config_version` | `uint64` | equals bootstrap version |
| 3 | `published_at_ms` | `int64` | informational server time |
| 10 | `locale_bundles` | repeated `LocaleBundle` | localized display strings |
| 11 | `assets` | repeated `AssetEntry` | client asset references |
| 12 | `display_rules` | repeated `DisplayRule` | non-authoritative presentation values |

Nested messages:

- `LocaleBundle`: `locale` string tag 1; repeated `TextEntry` tag 2.
- `TextEntry`: stable `key` string tag 1; localized `value` string tag 2.
- `AssetEntry`: stable `asset_key` string tag 1; immutable `url` string tag 2; raw `sha256` bytes tag 3.
- `DisplayRule`: stable `key` string tag 1; opaque Protobuf `bytes value` tag 2; `value_schema` string tag 3.

The package may describe names, error text, images, and visual stage thresholds. It MUST NOT be trusted for prices, balances, growth authority, maturity, yields, inventory limits, task progress, rewards, authorization, or protocol acceptance.

Publication rules:

- each version is immutable; changing any byte requires a new `client_config_version` and URL;
- the URL SHOULD include the version and digest and MUST never be reassigned;
- response headers are `Content-Type: application/x-protobuf`, `Content-Encoding: identity`, and `Cache-Control: public, max-age=31536000, immutable`;
- an `ETag` MAY be the quoted lowercase hexadecimal digest, but the Protobuf field remains raw bytes;
- H5 downloads to a temporary buffer, enforces 2 MiB, verifies SHA-256 before parsing/activation, verifies package and requested versions match, then atomically activates and caches by version;
- digest mismatch, parse failure, unsupported schema, or version mismatch discards the object and prevents game rendering; the client refetches bootstrap with bounded backoff;
- publication MUST be atomic: bootstrap cannot advertise an object until the immutable object is retrievable from every intended serving path.

## 11. Rate limits and abuse controls

These are initial per-deployment defaults, not capacity claims. Limits use monotonic server-side windows/token buckets and MAY become stricter under attack:

| Operation | Default |
|---|---|
| CSRF bootstrap | 60/hour/IP |
| registration | 5/hour/IP |
| login | 20/15 minutes/IP and 10/15 minutes/account-name bucket |
| Session inspection | 120/minute/Session or IP |
| logout | 20/minute/Session or IP |
| Gateway/bootstrap reads | 60/minute/Session |
| ticket issuance | 10/minute/Session and one unused ticket |

Account-name buckets MUST use a keyed digest and MUST NOT expose whether an account exists. Authentication work is admitted before expensive password hashing so attackers cannot exhaust hash capacity. A rate rejection performs no account, Session, or ticket mutation and returns 429 with both retry hints. Repeated malformed, CSRF-invalid, or credential traffic may receive longer IP/device-edge penalties. Security logs redact credentials, cookies, CSRF tokens, and tickets.

## 12. Retry and caching semantics

- GET Session, Gateway, bootstrap, CSRF, and immutable config requests may retry with bounded exponential backoff and jitter when network failure, 429, or 503 permits it.
- Registration and login MUST NOT be automatically repeated after an ambiguous network outcome. The client first inspects `/v1/auth/session`; if unauthenticated, it asks the user to retry the intent.
- Logout is idempotent and may be retried with a fresh CSRF token if necessary.
- Ticket issuance follows `ticket_request_id` replay rules in section 9. Network retry preserves the same body and ID.
- A 400, 401 credential failure, 403, 406, 409, 413, or 415 is not automatically retried unchanged.
- 429 and 503 retries honor the greater of `Retry-After` and `retry_after_ms`, use jitter, and stop at a bounded client deadline.
- Redirects are forbidden for auth/session/ticket API calls. Config URLs may use at most one same-origin or explicitly trusted CDN redirect, but the final exact bytes still require digest verification.

## 13. Production and local-development security

Production MUST:

- redirect public HTTP to HTTPS before serving H5, then use HSTS (`max-age` at least 31536000; include subdomains only when operationally valid);
- set Secure cookies, reject plaintext API and WebSocket traffic, and use trusted proxy metadata only from configured proxies;
- keep Session/ticket signing or hashing keys and password pepper out of source control;
- avoid secrets and credential-derived values in logs, metrics labels, traces, URLs, and error bodies.

Local development MAY use `http://localhost`, loopback IPs, and `ws://` only when an explicit development profile verifies that every listener and advertised URL is loopback-only. Because `__Host-` cookies require `Secure`, local plaintext uses clearly separate `cf_session_dev` and `cf_csrf_dev` host-only cookies. It may omit HSTS and Secure, but MUST retain HttpOnly on Session, SameSite, Origin/CSRF checks, opaque Sessions, password hashing, ticket lifetime/single use, generic credential errors, and duplicate-login revocation. The development profile MUST fail startup if bound or advertised beyond loopback and MUST never be enabled by a client-controlled header.

### 13.1 Local prototype durability of Sessions versus tickets

Accepted by ADR-0010 for the local prototype:

- Without durable Login storage, restarting LoginSvr loses accounts, Sessions, unused tickets, and CSRF nonce records.
- With the optional MySQL Login path, accounts and HTTP Sessions MAY survive LoginSvr restart. Unused `ws_ticket` issue/consume records and CSRF nonce records remain process-local and MUST be treated as lost after LoginSvr restart.
- After such a restart, a client whose Session cookie is still valid MUST re-bootstrap CSRF and request a new ticket; it MUST NOT reuse a pre-restart ticket. If the Session is gone, the client logs in again.
- Completing the MySQL authentication path therefore means durable account, Session, and Player checkpoint provisioning—not durable unused tickets across LoginSvr restarts.

## 14. Required acceptance and security tests

Implementation tests MUST prove:

1. Go and TypeScript generated types round-trip every HTTP message and reject malformed, trailing, oversized, and wrong-content-type bodies.
2. Registration returns 201 only after the account is ACTIVE and the Session is valid; farm checkpoint initialization is deferred to Owner Zone first activation; concurrent same-name registration exposes only one usable account.
3. Injected failures at every provisioning step expose no authenticatable partial account or usable Session, and retries/reconciliation converge without duplicate Player initialization; the co-located transaction path and a simulated separated-shard state-machine/Outbox path both satisfy the same external guarantee.
4. Passwords are never stored/logged in plaintext; Argon2id parameters, unique salts, dummy verification, and upgrade-on-login work.
5. Login returns one generic failure for unknown account, wrong password, disabled account, and non-ACTIVE provisioning state, without a meaningful intentional timing distinction.
6. Successful duplicate login revokes old Sessions/tickets and closes old WebSockets with 4409 before the new Session is usable.
7. Idle and absolute Session expiry, rotation, cookie attributes, revocation consistency, and coalesced idle refresh behave as specified.
8. Every mutation rejects missing/invalid Origin, cross-site Fetch Metadata, missing/mismatched CSRF proof, and an old token after Session rotation.
9. CORS never uses wildcard with credentials and an unlisted origin cannot read or mutate the API.
10. Logout is idempotent, clears cookies, invalidates tickets, and closes connections.
11. Ticket issue-ID replay returns one live ticket; changed payload conflicts; new intent invalidates an older ticket.
12. Exactly one of two concurrent ticket consumptions succeeds; expiry, wrong Gateway, revoked Session, and replay all fail generically.
13. Tickets, cookies, CSRF values, and passwords are absent from URLs, normal logs, traces, metrics labels, and error parameters.
14. Gateway discovery returns only allowlisted `wss` endpoints in production and ticket binding prevents cross-Gateway use.
15. HTTP bootstrap fields exactly match WebSocket AUTH names/types/meanings; AUTH-time values are used when publication changes during connection.
16. Client config is not advertised before publication, is immutable, enforces size, and fails closed on digest, version, schema, or parse mismatch.
17. Display config cannot change server prices, state, rewards, authorization, or protocol acceptance.
18. Rate limits apply at every documented scope, reject before expensive hashing where appropriate, include retry hints, and cause no mutation.
19. Retry tests cover ambiguous registration/login, idempotent logout, same-ID ticket retry, 429/503 backoff, and forbidden redirects.
20. Production rejects plaintext transport and insecure cookies; development exceptions fail closed on any non-loopback bind or advertised URL.
21. Fuzzing all public Protobuf decoders and error paths causes no panic, unbounded allocation, secret disclosure, or authority from unknown fields.

## 15. Cross-contract consistency

This contract uses V3 only. It does not restore V1 stateless Zone or V2 Journal behavior.

The accepted `websocket-protocol.md` says HTTP login returns a short-lived ticket, while its reconnect sequence separately says the HTTP Session obtains a new ticket. This contract resolves the wording by making ticket issuance the authenticated `/v1/ws-tickets` call after registration/login establishes a Session; registration/login responses themselves do not carry a ticket.

No unavoidable conflict with the accepted WebSocket or idempotency/error contracts is known. WebSocket game-command errors and Actor idempotency remain governed by `idempotency-and-errors.md`; the HTTP error enum and ticket issue replay are separate scopes.

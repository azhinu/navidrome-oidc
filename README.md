# Navidrome OIDC Signup Portal

Single-binary web portal that authenticates users via OIDC (Authorization Code + PKCE) and lets them create or update their Navidrome password before jumping straight into the music server.

## Features

- **OIDC login** with state/nonce + PKCE, ID token validation, and secure cookie sessions.
- **Minimal UI**: one responsive card with dark/light theme, password form, status banner, and final "Log in Navidrome" shortcut.
- **Health endpoints**: `/healthz` for liveness, `/readyz` for Navidrome reachability (login + lightweight API probe).
- **Structured logs** with request IDs plus detailed Navidrome call metrics.

## Requirements

- Go 1.22+
- Accessible OIDC provider.
- Navidrome instance reachable from this service.

## Configuration

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `ND_CP_LISTEN`      | No  | `:8386` | Listen address. |
| `ND_CP_BASE_URL`    | Yes | —       | External HTTPS URL of the portal; you can include a path prefix like `https://navi.mydomain.tld/auth`. |
| `ND_CP_GOTO`        | Yes | —       | Destination URL for the final "Log in Navidrome" button. |
| `ND_CP_LOG_LEVEL`   | No  | `info`  | `debug`, `info`, `warn`, `error`. |
| `ND_CP_SESSION_KEY` | Yes | —       | Cookie encryption/signing key (≥32 bytes). |
| `ND_CP_OIDC_ISSUER` | Yes | —       | OIDC issuer URL. |
| `ND_CP_OIDC_CLIENT_ID` | Yes | —    | Client ID registered with the IdP. |
| `ND_CP_OIDC_CLIENT_SECRET` | Conditionally | — | Client secret (empty if public client). |
| `ND_CP_OIDC_REDIRECT_PATH` | No | `/oidc/callback` | Redirect path relative to the base path (e.g. `/oidc/callback` → `https://host/auth/oidc/callback`). |
| `ND_CP_ND_BASE_URL` | Yes | — | Navidrome base URL (service-to-service). |
| `ND_CP_ADMIN_USER`  | Yes | — | Navidrome admin username for API calls. |
| `ND_CP_ADMIN_PASS`  | Yes | — | Navidrome admin password. |
| `ND_CP_TLS_VERIFY`  | No  | `true` | `false` to skip TLS verification when talking to Navidrome. |
| `ND_CP_TIMEOUT`     | No  | `10s` | HTTP client timeout for Navidrome requests. |

## Running

```bash
# export all required ND_CP_* variables first
go mod tidy   # needs network access
go build ./cmd/app
./app
```

## Getting Started with Pocket ID

1. Configure Pocket ID with the following:
   - Redirect URI: `https://$ND_CP_BASE_URL/oidc/callback`
   - Client Type: Confidential
   - Grant Types: Authorization Code + PKCE
2. Fill environment variables as described above.
3. Start the app and navigate to `https://$ND_CP_BASE_URL` to test the login flow.

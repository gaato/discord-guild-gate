# discord-guild-gate

An authorization gateway that fronts a private web app trusting a
username header — such as [File Browser](https://filebrowser.org/) with
`auth.method=proxy`. Users sign in with Discord OAuth2
(`identify guilds`, no bot required), their guild memberships are mapped
to configured *groups*, and the selected group's shared account name is
forwarded to the upstream in the `X-Username` header.

- If exactly one configured guild matches, the user is let straight in;
  with multiple matches a picker page is shown.
- The account name is derived from `GATE_USERNAME_TEMPLATE`
  (default `{label}`; e.g. `{lang}{label}` yields `ja2026` / `en2026`).
  The UI language (`ja` / `en`) is auto-detected from `Accept-Language`
  and can be switched on the picker.
- Discord users listed in `GATE_ADMIN_DISCORD_IDS` can also select the
  `admin` account.
- File Browser public share links (`/share/*`, `/api/public/*`,
  `/static/*`) are proxied without authentication.
- The client-supplied `X-Username` header is always dropped. Deploy the
  upstream so it is **only reachable through this gate**
  (same pod / localhost only), otherwise the header can be spoofed.

## Environment variables

| Variable | Example | Description |
| --- | --- | --- |
| `GATE_LISTEN` | `:8081` | Listen address (default `:8081`) |
| `GATE_BASE_URL` | `https://files.example.com` | Public URL; redirect URI = `<BASE_URL>/auth/callback` |
| `DISCORD_CLIENT_ID` | | Discord application client ID |
| `DISCORD_CLIENT_SECRET` | | Discord application client secret |
| `GATE_COOKIE_SECRET` | `openssl rand -hex 32` | Session signing key (32+ chars) |
| `GATE_UPSTREAM` | `http://127.0.0.1:8080` | Upstream URL |
| `GATE_GUILDS` | `2026=1386…,2027=1525…` | Comma-separated `label=guildID`; picker order = config order. `admin` is a reserved label |
| `GATE_USERNAME_TEMPLATE` | `{lang}{label}` | Upstream account name template; placeholders `{label}` and `{lang}` (default `{label}`) |
| `GATE_ADMIN_DISCORD_IDS` | `123,456` | Discord user IDs allowed to pick `admin` (optional) |
| `GATE_SESSION_TTL` | `24h` | Session lifetime (default `24h`); guild membership is re-checked on expiry |
| `GATE_TITLE_JA` / `GATE_TITLE_EN` | `My Uploader` | Page title per language (default `Discord Guild Gate`) |
| `GATE_GROUP_NAME_JA` / `GATE_GROUP_NAME_EN` | `Project {label}` | Picker button text template, `{label}` placeholder (default `{label}`) |
| `GATE_ACCENT` | `#5865f2` | Accent color for buttons/links (hex) |

## Discord application setup

Add `<GATE_BASE_URL>/auth/callback` to OAuth2 → Redirects. No bot is
needed: membership is checked with `GET /users/@me/guilds` using the
`guilds` scope (up to 200 guilds per user).

## File Browser setup

```
filebrowser config set --auth.method=proxy --auth.header=X-Username \
  --auth.logoutPage=/auth/logout
```

Create the shared accounts (e.g. `ja2026`) beforehand. Switching to
proxy auth disables password login. `--auth.logoutPage=/auth/logout`
makes File Browser's own logout button clear the gate session instead
of instantly re-authenticating via the header.

## Container image

Pushes to `main` build `ghcr.io/gaato/discord-guild-gate:latest`
(linux/amd64, linux/arm64) via GitHub Actions.

## License

[MPL-2.0](LICENSE)

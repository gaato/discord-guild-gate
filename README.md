# discord-guild-gate

An authorization gateway that fronts a private web app trusting a
username header — such as [File Browser](https://filebrowser.org/) with
`auth.method=proxy`. Users sign in with Discord OAuth2
(`identify guilds`, no bot required), their guild memberships, or their
user ID on an allowlist, are mapped to configured *groups*, and the
selected group's account name is forwarded to the upstream in the
`X-Username` header.

- If exactly one configured group matches, the user is let straight in;
  with multiple matches a picker page is shown.
- `GATE_USERS` allowlists Discord users by ID, with or without
  `GATE_GUILDS`. IDs rather than usernames, because a username can be
  changed and a released one taken by someone else. A user who is not
  allowed sees their own ID on the error page, to send to the
  administrator. With `GATE_USERS` alone, only the `identify` scope is
  requested.
- The account name is derived from `GATE_USERNAME_TEMPLATE`
  (default `{label}`; e.g. `{lang}{label}` yields `ja2026` / `en2026`).
  Use `{user_id}` for one account per Discord user instead of a shared
  one per guild; the ID is used rather than the username because users
  can rename themselves.
  The UI language (`ja` / `en`) is auto-detected from `Accept-Language`
  and can be switched on the picker.
- Discord users listed in `GATE_ADMIN_DISCORD_IDS` can also select the
  `admin` account.
- File Browser public share links (`/share/*`, `/api/public/*`,
  `/static/*`) are proxied without authentication; `GATE_PUBLIC_PATHS`
  changes the list for other upstreams.
- The client-supplied `X-Username` header, and the `GATE_GROUPS_HEADER`
  one, are always dropped. Deploy the upstream so it is **only reachable
  through this gate** (same pod / localhost only), otherwise the headers
  can be spoofed.

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
| `GATE_USERS` | `alice=1234…,bob=5678…,team=1234…,team=5678…` | Comma-separated `label=discordUserID`, listed after the guilds in the picker. Repeat a label with other IDs to put those users in one shared group. Labels must not repeat a `GATE_GUILDS` label. At least one of `GATE_GUILDS` and `GATE_USERS` is required |
| `GATE_USERNAME_TEMPLATE` | `{lang}{label}` | Upstream account name template; placeholders `{label}`, `{lang}` and `{user_id}` (Discord user ID) (default `{label}`) |
| `GATE_ADMIN_DISCORD_IDS` | `123,456` | Discord user IDs allowed to pick `admin` (optional) |
| `GATE_SESSION_TTL` | `24h` | Session lifetime (default `24h`); guild membership is re-checked on expiry |
| `GATE_TITLE_JA` / `GATE_TITLE_EN` | `My Uploader` | Page title per language (default `Discord Guild Gate`) |
| `GATE_GROUP_NAME_JA` / `GATE_GROUP_NAME_EN` | `Project {label}` | Picker button text template, `{label}` placeholder (default `{label}`) |
| `GATE_GROUP_NAMES` | `team=Our team` | Comma-separated `label=display name` for the picker, in both languages, overriding the templates for those labels. Each label must be configured in `GATE_GUILDS` or `GATE_USERS` |
| `GATE_ACCENT` | `#5865f2` | Accent color for buttons/links (hex) |
| `GATE_GROUPS_HEADER` | `X-Groups` | If set, every request carries all of the user's group labels, comma-separated, in this header, and `X-Username` carries the user's own name; there is no picker. For upstreams that do group-based access themselves, such as copyparty (`--idp-h-usr x-username --idp-h-grp x-groups`). `GATE_USERNAME_TEMPLATE` must then contain `{user_id}` and not `{label}`. `GATE_ADMIN_DISCORD_IDS` only lets those users in; the upstream decides what they may do |
| `GATE_PUBLIC_PATHS` | `/share/,/.cpr/` | Path prefixes proxied without authentication, each starting and ending with `/` (default: File Browser's `/share/,/api/public/,/static/`). copyparty needs its `--shr` folder and `/.cpr/` |

## Discord application setup

Add `<GATE_BASE_URL>/auth/callback` to OAuth2 → Redirects. No bot is
needed: membership is checked with `GET /users/@me/guilds` using the
`guilds` scope (up to 200 guilds per user), which is requested only when
`GATE_GUILDS` is set.

## File Browser setup

```
filebrowser config set --auth.method=proxy --auth.header=X-Username \
  --auth.logoutPage=/auth/logout
```

Create the shared accounts (e.g. `ja2026`) beforehand. With one
account per user (`{user_id}`, or `GATE_USERS` under `{label}`), File
Browser's proxy auth creates unknown users on first access instead; enable `--createUserDir` so each gets their own home
directory as scope. Switching to proxy auth disables password login.
`--auth.logoutPage=/auth/logout` makes File Browser's own logout button
clear the gate session instead of instantly re-authenticating via the
header.

## Container image

Pushes to `main` build `ghcr.io/gaato/discord-guild-gate:latest`
(linux/amd64, linux/arm64) via GitHub Actions.

## License

[MPL-2.0](LICENSE)

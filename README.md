# HSE App Z

A keyboard-driven terminal client for **HSE App X** (HSE University's mobile app):
timetable, grades, rating, people/group/room search with profiles, cafés and
menus, campus buildings and libraries, SmartPoint services, and news. Built
with Go and [Bubble Tea](https://github.com/charmbracelet/bubbletea).

```
HSE 1 Schedule  2 Grades  3 Rating  4 Search  5 Food  6 Campus  7 Services  8 News     Максим Никитин
────────────────────────────────────────────────────────────────────────────────────────────────────
12–18 Oct 2026                          │ Probability Theory and Mathematical Statistics
 Mon 12 Oct                             │ Seminar
 10:00–11:20 SEM Probability Theory… 303│ Monday, 12 October · 10:00–11:20 (80 min) · pair 3
▌11:40–13:00 SEM Probability Theory… 412│ …
```

## Build & run

```sh
go build -o hse-app-z .
./hse-app-z            # first run opens the HSE sign-in page in your browser
./hse-app-z --demo     # explore with recorded data, no account needed
```

Requires Go 1.24+ (the toolchain fetches 1.24.2 automatically if needed).

## Signing in

The app uses the same OpenID Connect client as the Android app
(`app-x-android` at `saml.hse.ru`). On first start, or when the session
expires, it opens the sign-in page and listens on
`http://localhost:8001/callback`. Your password goes only to saml.hse.ru.

| Command | What it does |
|---|---|
| `hse-app-z login` | sign in without starting the UI |
| `hse-app-z login --refresh-token TOKEN` | import a refresh token (e.g. from `getRefreshToken.py`) |
| `hse-app-z whoami` | show the account and token expiry |
| `hse-app-z token` | print a fresh access token for scripts (`curl -H "Authorization: Bearer $(hse-app-z token)" …`) |
| `hse-app-z logout` | delete the session and cached data |
| `hse-app-z doctor` | show file locations, session state, language and image support |

Access tokens are refreshed automatically. Refresh tokens rotate, and
concurrent refreshes are serialised so the session isn't invalidated.

## Keys

Everything is reachable from the keyboard. Press `?` in the app for the full
list for the current screen.

| Key | Action |
|---|---|
| `1`–`8`, `tab` / `shift+tab` | switch tab |
| `j`/`k`, `↓`/`↑`, `g`/`G`, `ctrl+d`/`ctrl+u` | move in the list |
| `J`/`K` | scroll the detail pane |
| `enter` | actions for the selected item (each action also has its own key) |
| `esc` / `q` | back; `q` on a tab quits |
| `/` | search people, groups and rooms from anywhere |
| `r` | refresh |
| `L` | switch language — interface and API data (English / Русский) |
| `ctrl+c` | quit |

Schedule and profile pages also use:
- `h`/`l` for the previous/next week, and `t` for today
- `[`/`]` for the previous/next day
- `o` to open the online-class link, `m` for the map, `a` for the room's timetable
- `p` to open the lecturer's profile, `f` to star, `i` to toggle profile/lesson details

## Language

`L` switches everything at once: the interface (labels, hints, dates,
plurals) and the data the API returns (discipline names, lesson types,
error messages). The choice is remembered. Start in a specific language
with `--lang ru`.

## Avatars

In terminals that support the kitty graphics protocol's Unicode
placeholders (**kitty ≥ 0.28** and **Ghostty**), profile pages and the
search preview show people's photos. The images are drawn as ordinary text
cells, so they scroll, clip and redraw with the rest of the UI. Other
terminals show the same layout without pictures.

| Option | Effect |
|---|---|
| `--images auto` (default) | on in kitty/Ghostty — also inside tmux when tmux is attached from one of them and `allow-passthrough` is on |
| `--images on` | force on |
| `--images off` | never show images |

**tmux:** add `set -g allow-passthrough on` to `tmux.conf`. The outer
terminal must also be known to support truecolor; Ghostty's and kitty's
terminfo entries declare it. If avatars are off only because passthrough
is disabled, the app says so in the footer at startup.

**Why no avatars?** `hse-app-z doctor` prints what was detected and why.

The default can also be set with `HSE_APP_Z_IMAGES=on|off|auto`. Images are
downloaded on demand, cropped to a square, and freed from the terminal when
the app exits.

## Behaviour

- **Caching:** responses are cached with their ETags, like the official
  app, so repeat requests are cheap (`304 Not Modified`).
- **Offline and rate limits:** without a network, or when rate-limited (the
  API allows about 60 requests/min), the last cached data is shown and
  marked as cached.
- **Favourites:** stars are stored locally. Favourites from the official
  app are shown alongside them.
- **Safety:** all server text is sanitised before display. Links open only
  if they are http(s), mailto or tel.

## Files

| What | Where (macOS / Linux) |
|---|---|
| Session (0600) | `~/Library/Application Support/hse-app-z/tokens.json` / `~/.config/hse-app-z/tokens.json` |
| Settings, favourites | same directory |
| HTTP cache | `~/Library/Caches/hse-app-z/http` / `~/.cache/hse-app-z/http` |

Set `HSE_APP_Z_HOME=/some/dir` to keep everything in one place.

## Development

```sh
go test ./...                 # unit + headless UI tests (fixtures, no network)
go run . --demo               # UI on recorded data
```

- `docs/API.md` documents the reverse-engineered API.
- `internal/api/testdata/` holds recorded responses. They contain real
  personal data (names, emails, grades): **don't publish them**.

Layout:

```
main.go                 CLI: flags, login/logout/token/whoami, starts the TUI
internal/api            HTTP client (auth, ETag cache, offline fallback), types, fixtures
internal/auth           Keycloak sign-in (loopback + PKCE), token refresh, storage
internal/config         paths, settings, local favourites
internal/app            root model: tabs, page stacks, menus, help, sign-in screen
internal/ui             shared widgets (list, scroll, layout, styles, menu, messages)
internal/ui/<feature>   timetable, grades, rating, search, food, campus, services, news
internal/ui/avatar      avatar download/convert + upload scheduling
internal/kitty          kitty graphics protocol (Unicode placeholders)
```

## Troubleshooting

- **"Port 8001 is busy"** — another copy of hse-app-z (or `getRefreshToken.py`)
  is running. Close it and press `r`. The port is fixed because it is part of
  the redirect URI registered at saml.hse.ru.
- **Browser doesn't open** — copy the link shown on the sign-in screen (`c`).
- **"Session expired"** — the app shows the sign-in screen again; after you
  sign in, everything reloads.

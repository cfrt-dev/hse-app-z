# HSE App X API notes

Reverse-engineered from the Proxyman capture of HSE App X 1.60 (build 129554)
plus read-only replays. Base URL: `https://api.hseapp.ru`. Recorded responses
live in `internal/api/testdata/` (used by tests and `--demo`).

## Auth

Keycloak realm `hse` at `https://saml.hse.ru/realms/hse`, public client
`app-x-android`, redirect URI `http://localhost:8001/callback`
(authorization-code flow; we add `state` + PKCE S256).

- Token: `POST /protocol/openid-connect/token` (form-encoded) with
  `grant_type=authorization_code|refresh_token`, `client_id=app-x-android`.
  The WAF rejects default library User-Agents — send `okhttp/4.12.0`.
- Access tokens live 3 h (`exp - iat = 10800`). Refresh tokens rotate.
- Claims used: `email` (keys the timetable), `name`, `sub` (cache scope).
- API calls: `Authorization: Bearer <access>`. Bad/missing token → `401`
  `{"error":{"name":"TOKEN_INVALID","message":"…"}}`.

## Conventions

- Errors: `{"error":{"name":"NotFound","message":"…","originError":{…}},"trace_id":"…"}`.
- ETags on every response; the app sends `If-None-Match` and gets `304`.
- Rate limit headers: `X-Ratelimit-Limit` (60 or 120/min), `-Remaining`, `-Reset` (seconds).
- `Accept-Language: en-RU;q=1.0, ru-RU;q=0.9` → English discipline names, etc.
- Types are loose: `program_id` is a number in ratings and a zero-padded
  string in grades; most fields can be `null` or missing.

## Endpoints

| Method | Path | Notes |
|---|---|---|
| GET | `/v3/ruz/lessons?email=E&start=YYYY-MM-DD&end=YYYY-MM-DD` | Timetable. `end` is inclusive and optional (without it: everything published from `start`). Instead of `email`: `group=<ruz id>` or `auditorium=<id or ruzNNN>`. No selector → the caller's own lessons. (`group_id`/`auditorium_id` params are ignored.) Unknown email → `[]`. |
| GET | `/v3/dump/search?q=…` | People (`STUDENT`/`STAFF`), groups (`GROUP`: `label`, `course`, `program_name`), rooms (`AUDITORIUM`: `room`, `auditorium_type`, `auditorium_location`). Up to ~100 hits; 1-char queries work. |
| GET | `/v3/dump/email/{email}` | Profile: `names`, `education[]` (students), `staff_positions[]` (with `chief`), `staff_address[]`, `is_timetable_available`, `is_subordinates_available`, `birth_date` (`0000-MM-DD` hides the year). Unknown → 404. |
| GET | `/v3/dump/email/{email}/link` | `{"url": "https://www.hse.ru/staff/…"}` or `{"url": null}` |
| GET | `/v3/dump/email/{email}/subordinates` | `[]Person` |
| GET | `/v3/dump/favourites/me` | `[]Person` (favourites starred in the official app) |
| GET | `/education/grades[?academic_year=2025/2026&program_id=000275111]` | `items[]` (`grade` is `{ten_point_scale, five_point_scale, pass}`, `{pass:true}`, or missing when not graded yet) + `available_academic_years`, `available_programs` (`id`, `name`, `description`), `selected_*`, `current_academic_year`. |
| GET | `/education/ratings[?academic_year&program_id&type&module_number]` | `items[]` rating snapshots (`type`: current, cumul, retake, minor; places, percentile, GPA, `discipline_list`). Without `type` returns all types of the year. Selectors: `available_types`, `available_module_numbers`, `available_programs` (`id`, `title`, `description`), `available_academic_years`. |
| POST | `/v3/dump/buildings/groups` body `{"is_free":true}` | `[]{campus, near, location, buildings[]}`; buildings have `cafes[]`, `libraries_v3[]` (offices with weekly hours, phones, sanitary day). |
| GET | `/food/cafes` | `[]{campus_id (building id), campus_name, coordinates, cafes[]}`. Cafe: `opening_hours[]`, `closed_dates[]`, `has_menu`, `description` (directions), `navigation{room,floor}`, `current_load`, `banner`. |
| GET | `/food/cafes/{cafe_id}` | one cafe |
| GET | `/food/cafes/{cafe_id}/menu[?day=monday]` | `{current_day, available_days[], sections[{section_name, price (set menus), items[{item_name, item_name_opt, price, weight, calories, composition, chips[]}]}]}` |
| GET | `/tasks/services[?category=/ELK_…]` | SmartPoint services. Entries with `url` are links; entries with only `category` are folders. |
| GET | `/tasks` | `{cursor, next_cursor, items[]}` — the user's SmartPoint requests. |
| GET | `/v2/stories` | News stories: `title` (may be the invisible U+2800), `publisher{name}`, `pages[{date_published, image, action{title, link}}]`. |
| GET | `/banners[?section=ratings|grades|search]` | `[{id, title, description, is_dismissible, is_enabled, …}]`, usually `[]`. |

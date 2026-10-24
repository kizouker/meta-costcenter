# Dashboard Drill-Down — Plan

> **Status:** Implemented · **Created:** 2026-10-24 · **Implemented:** 2026-10-24
> **Parent plan:** [PLAN.md](PLAN.md) (MVP criterion: "Session drill-down exposes parent/subagent attribution and usage provenance")

## 1. Problem

The browser dashboard renders usage correctly but cannot be navigated. Every link-styled
element is a filter toggle that keeps the user on the same page:

- **Session activity → click a session** sets `filters.session` and reloads the same event
  table, now narrowed to that session family (`frontend/src/App.jsx`, `Activity`). Because
  the table usually already showed that session's latest events, it looks as if nothing
  happened.
- **Usage breakdown → click a project or model** adds a filter and stays on the same tab,
  which now shows a single row. The only visible sign is a count badge on the Filters
  button.
- **Usage overview** has no clickable elements.

The underlying causes are:

1. **"Session activity" is an event log, not a session list.** It shows the latest 100
   model completions, so one session takes up many rows. Each row has an opaque session
   ID, a model, token counts and a record ID, but no project, start time, duration, cost
   or subagent count.
2. **There is no detail view.** No page shows one session's parent/subagent structure,
   per-model split or provenance in one place, so the parent plan's drill-down criterion
   is unmet.
3. **Filters are hidden.** Clicks accumulate filters inside a modal, and all three pages
   then show less data without saying why.
4. **The navigation follows the data model rather than user questions.** Users ask "what
   cost the most today?" and "what did this session do?"; the current pages are organized
   around grouping keys.

## 2. Goals

1. Every session, project and model shown in the UI links to a page that explains it.
2. One row per session in the sessions list, with enough context (project, time, cost,
   subagents) to recognize it without knowing its ID.
3. A session detail page that satisfies the parent plan's drill-down criterion: summary,
   parent/subagent tree with per-node usage, per-model split and the provenance log.
4. Active filters are always visible and individually removable.
5. Detail pages have stable, shareable URLs (`/sessions/:id`, `/projects/:name`) that keep
   working after a browser refresh.

### Non-goals

- No change to collection, pricing or the SQLite schema.
- No change to the CLI or TUI reports.
- No new frontend dependencies beyond React and React Router.

## 3. Information architecture

| Route | Page | Purpose |
|---|---|---|
| `/` | Overview | Totals, usage chart, cost context, **top sessions** and **top projects** (linked). |
| `/sessions` | Sessions | One row per root session, sortable; replaces "Session activity". |
| `/sessions/:id` | Session detail | Summary, timeline, subagent tree, model split, event log. |
| `/projects` | Projects | One row per project (the current breakdown "Projects" tab, linked). |
| `/projects/:name` | Project detail | Project totals, timeline, model split and its sessions. |
| `/breakdown` | Breakdown | Kept for model and billing-mode grouping; project and session rows link to their detail pages. |
| `/activity` | — | Redirects to `/sessions` so existing bookmarks keep working. |

Sidebar order: Overview, Sessions, Projects, Breakdown.

Detail pages are scoped by their URL, not by the global filters. The global period and
date range still apply to detail pages, but project, model, session and billing-mode
filters do not, because a detail page that silently hides part of its own subject is the
same confusion this plan fixes. Each detail page shows the period it is using.

## 4. Pages

### 4.1 Sessions list (`/sessions`)

Rows are **root sessions** (sessions whose `parent` is empty or not present in the data).
Subagent usage rolls up into its root, using the same family expansion as
`reports.query`. Columns:

- Session: project name as the primary label, with a shortened ID (first 8 characters) as
  secondary text and the full ID in the `title` attribute.
- Started and last activity (local time), plus duration.
- Models used (comma-separated, most-used first).
- Input / output tokens.
- API-equivalent cost, using the existing `cost()` formatting and "+" for partial pricing.
- Subagents: count of descendant sessions.

Default sort is last activity, newest first. Column headers sort by cost, tokens, start
time and subagent count. The whole row links to `/sessions/:id`.

### 4.2 Session detail (`/sessions/:id`)

- **Header:** project, full session ID (copyable), started, last activity, duration,
  billing mode(s), and a "Parent session" link if the session is itself a subagent.
- **Summary cards:** input, output, cached tokens and API-equivalent cost for the whole
  family, with the root session's own share shown beside it.
- **Timeline:** tokens per hour (sessions are usually shorter than a day) using the
  existing chart component with a configurable bucket label.
- **Subagent tree:** an indented tree of the root and its descendants. Each node shows its
  own tokens, its cost and its percentage of the family's tokens, and links to that
  session's detail page.
- **Model split:** a small table of model, tokens and cost.
- **Event log:** every event in the family, oldest first, with the provenance currently
  shown in "Session activity" (sequence, record ID, usage status, pricing snapshot). This
  is paginated client-side in pages of 100.
- **Export:** downloads this session family's events using `/api/export?session=…`.

### 4.3 Projects list and project detail

The projects list is the current breakdown table filtered to the project grouping, with
added session count and last-activity columns, and each row linking to
`/projects/:name`. Project detail shows the same summary cards and daily timeline as the
overview, a model split, and the sessions table from 4.1 restricted to that project.

### 4.4 Overview additions

- "Top sessions" — five root sessions by API-equivalent cost in the current filters.
- "Top projects" — five projects by API-equivalent cost.
- The "Active sessions" card links to `/sessions`.

### 4.5 Visible filters

A row of chips under the page title lists each non-default filter (for example
`Project: my-project ×`, `Last 7 days ×`). Removing a chip clears that filter. "Clear all"
appears when two or more are active. The Filters modal remains for adding filters. The
breakdown's model and billing-mode rows continue to apply a filter when clicked, and the
chip makes that effect visible.

## 5. Backend changes (`src/meta_cost_center/`)

1. **SPA routing in `webapp.py`.** Serve `index.html` for `/`, `/sessions`, `/sessions/*`,
   `/projects`, `/projects/*`, `/breakdown` and `/activity` instead of the current
   exact-match table. Unknown non-API paths keep returning 404.
2. **`reports.sessions(db, filters, rates)`.** Returns one summary per root session:
   `id`, `project`, `started`, `last_activity`, `models`, `billing_modes`, `subagents`,
   and the existing `totals()` fields. Family roll-up reuses the descendant expansion
   from `query()`, factored into a shared helper so both use the same logic.
3. **`reports.session_detail(db, session_id, filters, rates)`.** Returns the header
   fields, family totals, an hourly timeline, a nested `tree` (each node with its own
   totals and `children`), a model split and the full event list with
   `api_equivalent_usd` and `pricing_snapshot`. Only the period and date-range filters
   apply.
4. **`reports.project_detail(db, project, filters, rates)`.** Totals, daily timeline,
   model split and `sessions(...)` restricted to the project.
5. **Endpoints:** `GET /api/sessions`, `GET /api/sessions/<id>`, `GET /api/projects/<name>`
   (path segments URL-decoded). Missing sessions or projects return 404 with
   `{"error": "…"}`. They share the existing filter parsing, validation, loopback check
   and SQLite error handling, which should move into a helper so the three endpoints and
   `/api/dashboard` do not duplicate it.
6. **`/api/dashboard`** gains `top_sessions` (five entries from `sessions()`) for the
   overview. The `events` field stays for compatibility.

All new money values stay `Decimal` and are serialized the same way as existing totals.

## 6. Frontend changes (`frontend/src/App.jsx`)

- Split `App.jsx` into modules once it passes roughly 500 lines: `api.js`, `format.js`,
  `components/` (chart, metric, filter chips, sessions table, tree) and `pages/`.
- Add routes from section 3, a `useResource(url)` polling hook generalized from
  `useUsage`, and a `<Navigate>` from `/activity` to `/sessions`.
- Replace `row-link` filter buttons for sessions and projects with `<Link>` elements.
- Add the filter chip bar to the page header.
- Add "not found" and empty states for unknown sessions and projects.
- Rebuild `src/meta_cost_center/web/app.js` with `npm run build` and commit the output,
  as today.

## 7. Testing

- **`tests/test_core.py`:** `sessions()` roll-up with nested subagents (grandchildren),
  orphan parents, partial pricing and date filtering; `session_detail()` tree shape and
  totals that equal the sum of the nodes; `project_detail()` totals that match the
  breakdown group for that project.
- **`tests/test_web.py`:** SPA routes return `index.html`, including deep links;
  `/api/sessions/<id>` returns 200 for known IDs and 404 for unknown ones; URL-encoded
  project names; invalid filters return 400 on the new endpoints.
- **`tests/browser_dashboard.py`:** clicking a session row navigates to its detail page;
  clicking a subagent node navigates to it; a refresh on `/sessions/:id` keeps the page;
  removing a filter chip clears that filter.

## 8. Delivery order

1. Backend: shared helpers, `sessions()`, `session_detail()`, endpoints and SPA routing,
   with tests.
2. Frontend: Sessions list and Session detail pages, `/activity` redirect, sidebar update.
3. Filter chips.
4. Projects list and project detail.
5. Overview "Top sessions" and "Top projects", breakdown links.
6. README update (pages and routes) and a parent-plan checkpoint note marking the
   drill-down criterion.

Steps 1–2 address the reported problem on their own and can ship before the rest.

## 9. Implementation notes

- **Subagent project attribution.** Real retained logs record subagent sessions with project
  `unassigned` (about 3,200 sessions and 8% of tokens at implementation time), because
  subagent logs carry no workspace metadata. `reports.query` now attributes such sessions to
  the nearest ancestor with a known project, so project filters, project pages, and the
  breakdown include subagent usage. This also changes the CLI project report.
- **Sessions table.** The separate "Started" column was merged into "Last activity" (with
  duration underneath) and models collapse to "primary +N more" so that cost and subagent
  columns fit at desktop widths with real data.
- **Short IDs** show the first eight and last four characters, because UUIDv7 session IDs
  share a time-based prefix.
- **Not addressed:** every rate in `pricing.json` has `effective_from` 2026-10-24, so all
  earlier usage shows "Unknown" cost. This is the open historical-pricing item in PLAN.md.

## 10. Open questions

- **Session labels.** The logs may contain a session title or first prompt that would
  identify a session better than project and time. If the collector can read one
  cheaply, it should be added as a nullable column in a follow-up; this plan does not
  depend on it.
- **Large families.** Sessions with hundreds of subagents may need the tree collapsed
  beyond the first level. Decide after measuring real retained logs.

# Muse Cost Center

![](./muse-cost-control-screenshot.jpg)

A local usage collector and live browser dashboard for Muse Code. It reads retained
usage records into SQLite, shows project/model/session totals, and keeps estimated
API-equivalent costs separate from subscription usage and manually entered bills.
No API key, hosted backend, or Python dependencies are required. Python 3.11+ on
Linux/WSL is supported. The browser dashboard uses a local Python HTTP server and
a React frontend with React Router. Built assets are included, so running `muse-cost show`
does not require Node or an additional frontend server.

From this checkout:

```bash
./muse-cost show
```

`show` starts the local server and opens the dashboard in your default browser at
`http://127.0.0.1:8765/` (or a free local port if 8765 is occupied). Keep the command running for live updates; Ctrl+C stops it.
The browser opens immediately while the first historical import runs in the background.
The collector rescans every 15 seconds, and the page checks for updates every two seconds.
The sidebar opens four pages: Overview (`/`), Sessions (`/sessions`), Projects
(`/projects`), and Breakdown (`/breakdown`). The Sessions page lists one row per session,
with subagent usage included in its parent. Selecting a session opens its page
(`/sessions/<id>`), which shows the session's totals, an hourly or daily timeline, the
tree of subagents with each one's share, a per-model split, and every recorded completion
with its provenance. Selecting a project opens `/projects/<name>` with that project's
sessions and models. These detail URLs can be bookmarked and survive a refresh; the old
`/activity` URL redirects to `/sessions`.

Use the Filters drawer to filter dates, projects, models, sessions, and billing modes.
Active filters appear as removable chips beside the page title, so it is always visible
why a page shows less data. Session and project pages apply only the date range, because
they are already scoped by their URL. Subagent logs carry no workspace metadata, so
subagent usage is attributed to the project of its top-level session. Filters remain
active on refresh and are saved in this browser tab for the current server session. Page
URLs stay clean, without filter query strings. Export downloads usage
matching the current filters. CLI filter options still set the initial dashboard view.

```bash
muse-cost show --port 8766       # choose another local port
muse-cost show --port 0         # select a free port
muse-cost show --no-browser     # print the URL without opening it
```

If your WSL browser integration is unavailable, open the printed localhost URL in your
Windows browser. There is no hosted service; the server listens only on loopback.
Updates appear after Muse writes complete usage records, rather than per-token streaming.

To install the `muse-cost` command into a virtual environment:

```bash
python3 -m venv .venv
.venv/bin/python -m pip install -e .
```sh

Run in the context of the python env
```sh
.venv/bin/muse-cost show
```

or activate the python env

```sh
source .venv/bin/activate
muse-cost show
```

Pip installs the declared build tools in an isolated build environment. This setup
requires access to your Python package index. The executable in the checkout works
without installation: `./muse-cost show`.

```bash
./muse-cost show --interval 2 --period month
./muse-cost show --project my-project --model muse-spark-1.3
./muse-cost show --session SESSION_ID --period all
./muse-cost show --from 2026-10-01 --to 2026-10-24 --billing-mode payg
./muse-cost tui                        # optional terminal dashboard
./muse-cost today                      # plain usage snapshot
./muse-cost scan
./muse-cost today
./muse-cost week
./muse-cost month
./muse-cost sessions --sort estimated-cost
./muse-cost session SESSION_ID
./muse-cost projects
./muse-cost models
./muse-cost watch --interval 15         # plain reports, no interactive UI
./muse-cost doctor
./muse-cost export --format csv --period month
./muse-cost export --format json --period all
```

Filters use exact labels and combine with AND. Date ranges are inclusive, using the
machine's local calendar and historical timezone offsets. The browser dashboard preserves
filters on refresh. `--source` accepts a session directory or an
explicit JSONL file and can be repeated. `--db`, `--config`, and `--prices` are available
on all commands. `doctor` includes source paths for local troubleshooting; portable
exports omit them.

Try live updates with synthetic usage in two terminals (no model calls):

```bash
python3 examples/live_demo.py
# In another terminal, run the command printed by the demo.
```

Configuration defaults to `${XDG_CONFIG_HOME:-~/.config}/meta-cost-center/config.toml`.
See [examples/config.toml](examples/config.toml) for explicit session labels and billing
modes. Native `runtime.session.metadata.workspace_root` supplies the project basename;
no project labels are inferred from prompts. Missing metadata becomes `unassigned`.
Billing mode stays `unknown` until explicitly mapped. After changing mappings, the
next scan updates existing observations for those sessions.

The default source is `${XDG_DATA_HOME:-~/.local/share}/muse/sessions`. Data is stored
at `${XDG_DATA_HOME:-~/.local/share}/meta-cost-center/costs.sqlite3`. New data directories
are private (`0700`) and the database is created with `0600` permissions. Collection
never modifies Muse logs or reads authentication files. Only allowlisted usage and
provenance metadata is persisted; prompts, model responses, and tool output are not.
The JSON parser necessarily reads log records into memory to extract those fields.

Cost estimates use the bundled [2026-10-24 pricing snapshot](src/meta_cost_center/pricing.json),
verified against [Meta's pricing page](https://dev.meta.ai/docs/pricing-rate-limits).
It applies only from its observation date because earlier effective dates were not
verified. Older usage, unknown model aliases, and missing cache counts have unknown
costs. Supply `--prices path.json` for additional audited historical rates; use the
same JSON schema, with a source URL, retrieval date, effective date, snapshot version,
model ID, USD currency, and three per-million rates. The newest applicable effective
date wins. All arithmetic uses `Decimal`. Non-token charges remain unknown, so even
priced token totals are partial estimates. Reports expose the pricing snapshot in
exports and keep unknown costs distinct from zero.

Manual account observations and local warnings:

```bash
./muse-cost reconcile add --date 2026-10-24 --from 2026-10-01 --to 2026-10-24 \
  --amount 12.34 --currency CAD --kind outstanding-balance --scope team
./muse-cost reconcile list
./muse-cost alert set --monthly-usd 30 --at 50,80,100
```

Observations remain separate from local estimates; different scope, currencies,
taxes, credits, and billing lag prevent a reliable automatic comparison. The local
warning uses the known month-to-date API-equivalent subtotal across all billing
modes, beginning at 50% of the threshold. It cannot stop charges and only runs while
the CLI is running.

Current accuracy limits:

- Native Muse 1.4.12 envelopes were inspected locally; parsing follows
  `payload.event.kind == model_completed`. Native IDs deduplicate copied logs.
  Records without IDs use source path, session, and durable sequence/offset.
- Session, subagent, observer, and approval-review streams are collected. Process-owner
  bookkeeping files are excluded. Unknown models are preserved without inventing rates.
- An interchange `run.usage` shape is supported for comparison only:
  `{ "type": "run.usage", "session_id": "...", "timestamp": "ISO8601", "usage":
  { "models": [{ "model": "...", "input_tokens": 100, "output_tokens": 10,
  "cached_tokens": 20 }] } }`. The upstream headless shape has not been captured;
  unsupported shapes are diagnosed. Retained native logs supply headless usage today.
  Summaries are never added to event totals. Multi-run/cumulative summary scope needs
  further validation; mismatches are not automatically resolved.
- Incomplete final lines wait until complete. Malformed usage records remain counted
  in diagnostics. Truncation/replacement restarts scanning and preserves historical
  observations; provenance may no longer exist. No usage is silently deleted.
- Only retained local sources are covered. Disabled logging, other machines, direct API
  activity, and missing sources cannot be counted. Empty results do not prove zero usage.
- The benchmark compatibility adapter and a live paid Muse/subagent fixture capture
  remain follow-up work. No new model calls are made by this tool or its tests.

Run the tests:

```bash
python3 -m unittest discover -s tests -v
```

Tests exercise incremental writes, partial lines, deduplication, replacement, subagents,
summary non-additivity, cache pricing, privacy, CLI exports, manual observations, and a
terminal session, browser-server routing, live updates, filter persistence, and exports.
See [the project plan](plans/PLAN.md) for the full intended scope.

## Frontend development

React components live in `frontend/src/App.jsx`; shared styling is in
`src/meta_cost_center/web/style.css`. After changing React code, rebuild the bundled
JavaScript before running `muse-cost show`:

```bash
npm ci --prefix frontend
npm run build --prefix frontend
```

The build uses esbuild and includes React and React Router in the local JavaScript
bundle. No CDN, Node server, or runtime package downloads are used by the dashboard.
Dependency versions are pinned in `frontend/package-lock.json`. Optional browser
regression checks use Playwright and an installed Chrome:

```bash
python3 tests/browser_dashboard.py
```

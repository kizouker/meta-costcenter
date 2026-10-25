# Cost Visibility, Pricing, and Alerts — Plan

> **Status:** Implemented · **Created:** 2026-10-24 · **Implemented:** 2026-10-24
> **Parent plan:** [PLAN.md](PLAN.md) · **Builds on:** [UI-DRILLDOWN.md](UI-DRILLDOWN.md)

## 1. Problem

The cost center exists to explain cost, but the dashboard currently leads with tokens:

- **Almost nothing is priced.** Every rate in `pricing.json` has `effective_from`
  2026-10-24, so all earlier usage is "Unknown". About 75% of observed completions also
  use models with no public rate (internal or preview models), and their rates cannot
  be added to the repository without publishing those model names.
- **There is no cost over time.** The only chart plots tokens per day. The "Cost
  context" panel splits cost by billing mode, which is mostly `$0.00` because billing
  mode is usually unknown.
- **Alerts are CLI-only and monthly-only.** `muse-cost alert set --monthly-usd` stores
  one threshold in the `settings` table, and the warning appears only in CLI text
  reports. There is no daily limit, no per-project limit, and nothing in the UI.

## 2. Decisions

| Question | Decision |
|---|---|
| Usage before a rate's verified date | Apply the model's earliest known rate and mark the estimate **assumed**. A verified older rate added later replaces the assumption for its period. |
| Rates for models without public pricing | Entered in the UI and stored only in the local SQLite database (`~/.local/share/meta-cost-center/costs.sqlite3`), never in the repository. |
| Alert delivery | A dashboard banner on every page, plus a desktop notification (browser Notification API) while the dashboard is open. |
| Alert scope | A limit applies to one period (day, week or month) and either all projects or one project, optionally pay-as-you-go usage only. |

## 3. Pricing

### 3.1 Rate sources

1. **Public rates** stay in `pricing.json`, embedded in the binary, (or the file named by
   `prices` in `config.toml`). Only publicly documented models belong here.
2. **Local rates** live in a new `rates` table in the local database:
   `model TEXT PRIMARY KEY, input_per_million, cached_input_per_million,
   output_per_million, updated_at`. A local rate applies to all dates and overrides any
   public rate for the same model. Local rates have `snapshot_version` `local`.

`reports.with_local_rates(db, rates)` merges both lists. The web server merges them on
every request, so edits apply immediately. The CLI merges them once at startup.

### 3.2 Estimates

`estimate(row, rates)` returns `(value, snapshot, assumed)`:

- A local rate for the model wins.
- Otherwise the latest public rate with `effective_from` on or before the usage date
  is used.
- Otherwise, if the model has any public rate, the earliest one is used and `assumed`
  is true.
- Otherwise, or if cached tokens are unknown, the cost is unknown, as today.

`totals()` gains `cost_assumed_events`. The UI marks totals containing assumed rates
with "≈" and explains it in a tooltip and footnote. Exports gain a `rate_assumed`
column.

### 3.3 Pricing page (`/pricing`)

This page has one row per model seen in usage: completions, tokens, estimated cost, rate
source (**Public**, **Public, assumed before <date>**, **Local**, or **Missing**) and
the three per-million rates. Models with no rate are listed first, with the share of
tokens they leave unpriced. Each row can add, edit or remove a local rate. The page
states that local rates are stored only on this machine.

API: `GET /api/pricing`, `PUT /api/pricing/<model>` (JSON body with the three rates),
`DELETE /api/pricing/<model>`.

## 4. Cost over time

- **Overview metrics** lead with cost: estimated cost for the period (with priced
  coverage, for example "92% of tokens priced"), average cost per active day, tokens,
  and active sessions.
- **Chart toggle.** Every usage chart (overview, session, project) gets a **Cost /
  Tokens** toggle, with cost as the default. Cost bars are per day (or per hour on short
  sessions), and days with unpriced usage are marked.
- **The "Cost context" panel becomes "Cost by model"**: the top models by estimated
  cost with their share, an "Unpriced" row showing the tokens with no rate and a link
  to the Pricing page, and the billing-mode split kept as a footnote.

## 5. Alerts

### 5.1 Model

A new `alerts` table holds `id, period ('day' | 'week' | 'month'), project (NULL = all
projects), billing_mode (NULL = all usage, or 'payg'), limit_usd, warn_levels (a JSON
list of one to four percentages below 100, default `[80]`), enabled, created_at`.

Windows use local calendar time: **day** is today, **week** starts on Monday, and
**month** is the calendar month. Spend is the API-equivalent estimate for usage in
the window that matches the alert's project and billing mode. Unpriced usage is
excluded and reported alongside, so the spend is a lower bound.

`reports.alert_status(db, rates)` loads rows once from the earliest window start and
returns, for each enabled alert: spend, limit, percent, level (`ok`, `warning` or
`exceeded`), window start and end, and unpriced completions.

### 5.2 Migration from the CLI monthly warning

`store.connect` moves an existing `settings.monthly_usd` into an all-projects monthly
alert, using the levels below 100 from `alert_levels` as `warn_levels`, and then
removes the old keys. `muse-cost alert set --monthly-usd` writes to the `alerts` table,
and CLI reports list exceeded and warning alerts from `alert_status`.

### 5.3 UI

- **Alerts page (`/alerts`)**: a form to add an alert (period, project or all projects,
  all usage or pay as you go only, limit in USD, warn percentage), and a list of alerts
  with progress bars, the current window, and controls to edit, pause or delete.
- **Banner**: every page shows a compact banner while any alert is at `exceeded` (red)
  or `warning` (amber), for example "Today: $6.10 of your $5.00 daily limit for
  lawnmower-sim". It links to the Alerts page.
- **Desktop notifications**: the Alerts page has an "Enable desktop notifications"
  button that requests permission. While any dashboard tab is open, the app notifies
  once per alert, window and level when a warning or exceeded level is first seen. It
  records what it has notified in `localStorage`, keyed by alert ID, window start and
  level.
- **Project page**: shows that project's alerts and an "Add alert for this project"
  link that opens the form pre-filled.

The dashboard payload includes `alerts` (the statuses), so the banner refreshes with
the existing two-second poll.

API: `GET /api/alerts`, `POST /api/alerts`, `PUT /api/alerts/<id>`,
`DELETE /api/alerts/<id>`.

## 6. Write safety

These are the first endpoints that change data. They keep the existing loopback
`Host`/`Origin` checks and also require `Content-Type: application/json`, which a
cross-site HTML form cannot send without a CORS preflight that the server never
approves. Bodies are limited to 16 KB, amounts must be finite and non-negative
decimals, and model and project names are limited to 200 characters.

## 7. Testing

- **Core:** assumed back-pricing; local rate overrides public; unknown when no rate;
  alert windows (day, Monday-start week, month), project and billing-mode scope,
  warning and exceeded levels; settings migration.
- **Web:** pricing and alert CRUD; validation errors return 400; foreign `Origin`
  returns 403; non-JSON bodies return 415; dashboard payload includes alert statuses.
- **Browser:** add a local rate and see cost change from Unknown; create a daily alert
  below current spend and see the banner; the cost/tokens toggle switches the chart.

## 8. Privacy

Local rates, alerts and project names stay in the local database, which is outside the
repository and covered by `.gitignore`. Nothing in this plan adds model or project names
to tracked files.

## 9. Implementation notes

- On the retained local logs at implementation time, back-pricing raised priced coverage
  from about 0% to 17% of tokens. The remaining 83% belongs to models without public
  rates, which need local rates on the Pricing page.
- Rows with model `unknown` (no model recorded in the log) are listed on the Pricing page
  like any other model and can be given a local rate.
- The dashboard payload now carries `alerts` instead of `monthly_warning_usd`.
- Alerts were extended to up to four warning levels (`warn_levels`). Existing
  databases migrate the single `warn_percent` column into a one-item list and drop it.

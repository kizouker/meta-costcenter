# Meta Cost Center — Project Plan

> **Status:** Initial implementation available; validation in progress · **Version:** 0.3 · **Updated:** 2026-10-24  
> **Repository:** [omenking/meta-ai-field-notes](https://github.com/omenking/meta-ai-field-notes)  
> **Suggested repository path:** `tools/meta-cost-center/PLAN.md`  
> **Primary platform:** Windows with WSL2 (Linux-first)  
> **Project type:** Local-first, open-source CLI for usage visibility and cost estimates

## Implementation checkpoint — 2026-10-24

The repository now contains the Python package, executable `./muse-cost`, live browser dashboard, incremental SQLite collector, shared filtered reports, versioned token pricing, exports, local warnings, and manual account observations. See [README.md](../README.md) for usage and limitations.

**Cost update — 2026-10-24:** Cost-first overview with a cost-over-time chart, assumed back-pricing, local rates for models without public pricing (stored only in the local database), and day/week/month alerts per project with a dashboard banner and desktop notifications. See [COST-AND-ALERTS.md](COST-AND-ALERTS.md).

**Drill-down update — 2026-10-24:** The dashboard now has Sessions, session detail, Projects, and project detail pages with a subagent tree, per-model split, and provenance log, which addresses the session drill-down criterion below. See [UI-DRILLDOWN.md](UI-DRILLDOWN.md).

Validation includes synthetic append/partial-line/replacement/subagent fixtures, terminal and browser interaction tests, and importing retained local Muse 1.4.12 logs. The test suite covers collection, reporting, browser-server routing, live refresh, and filter persistence. A live paid Muse run has not been generated for validation. Upstream `run.usage` schema capture, benchmark compatibility integration, and historical pricing effective dates remain open; native retained logs provide the headless fallback. Do not treat this checkpoint as completion of every acceptance criterion below.

## 1. Executive summary

**Meta Cost Center** answers a practical question: **“How much Meta AI usage have I generated today, which session or project caused it, and how much might it cost?”**

Build a **read-only, local-first collector** for Muse Code usage. It should ingest interactive-session logs and headless-run usage reports, normalize model/token usage, estimate the corresponding pay-as-you-go price, and present a clear local browser dashboard. Later, it should support manual reconciliation with the Meta developer billing dashboard.

**Decision:** Build this **before** Muse KeyGuard. Reuse the usage-parsing ideas in the existing [`run_benchmark.py`](https://github.com/omenking/meta-ai-field-notes/blob/main/docs/q001-programatic-user-wonder-full/run_benchmark.py), but do **not** rewrite the benchmark or require a wrapper around the Muse interactive terminal.

**Non-negotiable distinction:** An **estimated API-equivalent cost** is not necessarily the **amount Meta will bill**. A Muse Code subscription is flat-rate for its covered credential; additional Model API keys are pay-as-you-go. Meta does not currently provide a hard spend cap, and no supported, real-time account billing API has been verified for this project. Clearly label all three concepts.

## 2. Problem and motivation

Existing observability is fragmented:

- Muse's interactive `/usage` shows session usage, but not a durable, searchable view across every project and session.
- Retained Muse session JSONL files contain detailed model events and can include subagent activity.
- Recent Muse releases emit `run.usage` for headless `muse exec --json` runs.
- The developer Usage and Billing dashboards can show account activity, but a documented machine-readable billing ledger endpoint has not been verified.
- A spend notification is **not a hard stop**. Unexpected usage can continue after the email threshold is reached.

The first objective is **visibility**, not enforcement. This project should not claim that it prevents charges or revokes keys.

## 3. Goals and success criteria

### Goals

1. Show **today, last 7 days, month-to-date, and per-session** token usage and estimated costs.
2. Include **interactive and headless Muse sessions** without changing normal Muse workflows.
3. Break down estimates by **model, project, session, and known billing mode**.
4. Handle **cached input tokens** separately where evidence supports it.
5. Incorporate **subagent/background** work without losing it or counting it twice.
6. Preserve enough provenance to explain every total and highlight gaps.
7. Provide local warning thresholds and manual billing reconciliation.
8. Validate collection through a live browser dashboard with filters and session drill-down.
9. Work on WSL with **no cloud backend and no Meta API key needed for collection**.

### MVP acceptance criteria

- [ ] A clean WSL install can import existing Muse sessions from disk.
- [ ] `muse-cost show` opens an auto-refreshing local browser dashboard showing today and month-to-date usage grouped by model and project.
- [ ] Dashboard filters cover date range, project, model, session, and billing mode; active filters survive refreshes.
- [ ] Session drill-down exposes parent/subagent attribution and usage provenance.
- [ ] New complete usage records appear within one configured scan/refresh interval after Muse writes them; the dashboard shows last scan time, collection status, and coverage warnings.
- [ ] Running a Muse session alongside the dashboard demonstrates live updates without double counting across repeated refreshes.
- [ ] Interactive sessions are parsed directly from native Muse logs.
- [ ] Headless sessions use `run.usage` when present, with a documented fallback.
- [ ] Every imported usage event has stable provenance and survives repeated rescans **without double counting**.
- [ ] Parent-session/subagent accounting has regression tests using realistic fixtures.
- [ ] A price table with version/effective date supports normal input, cached input, and output.
- [ ] Missing model, pricing, billing mode, or cached-token data is labeled **unknown/incomplete**, never silently reported as $0.
- [ ] Local warnings can trigger at configurable estimated-cost thresholds, explicitly **not** hard spending caps.
- [ ] Output separates **measured tokens**, **estimated API-equivalent USD**, and **verified/manual billed amounts**.
- [ ] The collector never reads or persists prompt text, model output, bearer tokens, or authentication files.
- [ ] Documentation includes reproducible fixtures, limitations, and source references.

## 4. Users and primary workflow

**Initial user:** A solo developer using Muse Code interactively in WSL and occasionally running scripted `muse exec` benchmarks.

**Happy path:**

1. Use `muse` normally, in any project.
2. Run `muse-cost scan` to import retained session usage.
3. Run `muse-cost today`, `muse-cost month`, or `muse-cost sessions` for an explanation of where tokens and estimated dollars went.
4. Run `muse-cost show` alongside Muse to observe usage updates, filter the dashboard, and drill into sessions. Updates reflect usage written to local logs, not provider billing. Use `muse-cost watch` for collection without the interactive dashboard.
5. Periodically enter a dashboard-observed billed figure using `muse-cost reconcile` and inspect the difference.

> The initial implementation provides these commands. See [README.md](../README.md) for supported options and current limitations; this plan also includes work still awaiting validation.

## 5. Source-of-truth model

| Layer | Source | What it establishes | What it cannot establish |
|---|---|---|---|
| 1 — Observation | Retained Muse `session.jsonl` | Per-event usage and session metadata when logged | Meta's final invoice |
| 2 — Headless run summary | `muse exec --json` `run.usage` (Muse 1.4.11+) | Per-model run totals and subscription allowance details | Final pay-as-you-go bill |
| 3 — Pricing | Versioned Meta pricing snapshot | An **estimated API-equivalent cost** | Account-specific credits/tax or actual settlement |
| 4 — Account verification | User-entered values from Meta Usage/Billing UI | A dated **manually observed account figure** | An automated, guaranteed real-time balance |

**Rules:**

- A run-level summary is a reconciliation source, **not another billable event to add** to the model-completion events.
- Distinguish `subscription`, `payg`, and `unknown` billing modes. Never infer paid charges solely from token totals.
- Separate `estimated_usage_cost_usd`, `subscription_api_equivalent_usd`, and `manual_billed_amount`.
- Track currency explicitly. Pricing uses USD; the account dashboard may be displayed in another currency (e.g., CAD). Do not silently convert or compare mismatched currencies.
- If local session logging was disabled, label coverage **unavailable**; do not treat unobserved usage as zero.
- Do not present month-to-date local totals as *account-wide* usage when external apps or other computers can consume the same team credentials.

## 6. Proposed architecture

```text
                      ┌───────────────────────────┐
                      │ Muse Code (unchanged)     │
                      │ interactive / headless    │
                      └─────────────┬─────────────┘
                                    │
                 ┌──────────────────┴─────────────────────┐
                 ▼                                        ▼
      Native session JSONL                      run.usage from
      including subagents                       headless JSONL
                 │                                        │
                 └──────────────┬─────────────────────────┘
                                ▼
                      Source adapters / scanner
                    (read-only, incremental)
                                │
                                ▼
                  Normalizer + deduplication
                   + coverage/reconciliation
                                │
                                ▼
                        Local SQLite DB
                                │
               ┌────────────────┼─────────────────┐
               ▼                ▼                 ▼
        Pricing engine     CLI / dashboard   Warning rules
               │                │                 │
               └────────────────┴─────────────────┘
                                ▼
                      Optional manual billing
                         reconciliation
```

**Implementation preferences:** Python 3.11+ (matches the current benchmark's language), standard-library `sqlite3`, `argparse`/Typer (choose during spike), `Decimal` for monetary calculations, Linux-first XDG paths. Use a local browser UI for the MVP dashboard, opened by `muse-cost show`. Preserve the optional terminal interface as `muse-cost tui`. Keep the collector, report queries, and dashboard separate so all views share accounting and filter semantics. The dashboard runs incremental scans while open and refreshes from SQLite; `watch` uses the same collection path. Use React components and React Router for the three browser pages. Keep shared filters in React state and tab session storage; page URLs contain only their path. Serve the prebuilt React bundle over loopback from Python, without a hosted backend or Node runtime requirement.

**Proposed directories:**

```text
tools/meta-cost-center/
  PLAN.md
  README.md
  pyproject.toml
  src/meta_cost_center/
    cli.py
    config.py
    discovery.py
    adapters/
      muse_session.py
      muse_headless.py
    normalize.py
    store.py
    pricing.py
    reports.py
    dashboard.py              # live browser UI using shared report queries
    reconcile.py
  tests/
    fixtures/                 # synthetic/redacted; no secrets
    test_usage_parsing.py
    test_deduplication.py
    test_subagents.py
    test_pricing.py
    test_reconciliation.py
  data/
    pricing-example.json      # published rates + provenance
```

**Local runtime paths** (not committed):

```text
${XDG_DATA_HOME:-~/.local/share}/meta-cost-center/costs.sqlite3
${XDG_CONFIG_HOME:-~/.config}/meta-cost-center/config.toml
```

## 7. Data ingestion design

### 7.1 Native interactive Muse sessions

Default discovery root:

```text
${XDG_DATA_HOME:-~/.local/share}/muse/sessions/YYYY/MM/DD/<session-id>/session.jsonl
```

Meta also documents subagent logs beneath a parent session, in paths such as:

```text
.../<session-id>/subagent/<child-id>/...
```

Requirements:

- Discover top-level sessions and child/subagent logs, without assuming all versions use identical nested paths.
- Ingest recognized **usage metadata only** from `model_completed` records; never index full transcript/event bodies.
- Preserve `session_id`, parent/child relationship, model (if available), timestamp, counts, file provenance, and stable event identity.
- Incrementally scan append-only files. Save a file cursor and handle truncation, replacement, and partially written lines safely.
- Recognize when data is absent, malformed, or incomplete; report an explicit coverage warning.
- Record Muse version if available and version parsers as schemas change.

### 7.2 Headless `muse exec --json`

- Use a parser for `run.usage` as emitted by documented recent versions.
- **Capture real fixture events first**; the changelog confirms the event exists but this plan does not assume a particular nested JSON schema.
- Extract per-model totals if available, with source + run/session identity.
- If no `run.usage` exists, fall back to retained session-event aggregation.
- Where both sources exist, compare them and surface a reconciliation status; do **not** sum both.
- Preserve incomplete/aborted-run semantics; a failed run may still use billable tokens.

### 7.3 Existing benchmark integration

Current starting point: [`docs/q001-programatic-user-wonder-full/run_benchmark.py`](https://github.com/omenking/meta-ai-field-notes/blob/main/docs/q001-programatic-user-wonder-full/run_benchmark.py)

Existing reusable ideas:

- `find_session_log()` — locate the durable log for a benchmark session.
- `harvest_usage()` — collect `model_completed` token fields.
- `estimate_cost()` — current aggregate rate application.
- `events.jsonl` and `report.yml` — captured run artifacts.

**Refactor, do not rewrite:** Extract reusable parsing/costing into the Cost Center package. Keep the benchmark running with a compatibility adapter. Extend it to capture `run.usage`, reconcile subagents, and output **estimated** costs by model and cache category. Preserve old report fields temporarily and add a schema/version field.

## 8. Data model (MVP)

Minimal relational entities:

| Entity | Selected fields | Notes |
|---|---|---|
| `source_file` | `id`, `path`, `inode_or_fingerprint`, `offset`, `last_scanned_at`, `parser_version` | Incremental import provenance |
| `session` | `session_id`, `parent_session_id`, `project_label`, `started_at`, `finished_at`, `mode`, `billing_mode`, `muse_version` | `interactive`/`headless`; modes may be unknown |
| `usage_event` | `event_id`, `session_id`, `recorded_at`, `model`, `input_tokens`, `output_tokens`, `cached_input_tokens`, `source_file_id`, `record_sequence`, `usage_kind` | Immutable normalized observation; unique event identity |
| `run_summary` | `run_id`, `session_id`, `model`, `summary_tokens`, `source_file_id`, `accounting_status` | Reconcile with event totals; never additive |
| `price_rate` | `model`, `mode`, `currency`, `input_per_million`, `cached_input_per_million`, `output_per_million`, `effective_from`, `source_url`, `snapshot_version` | Pricing history must be auditable |
| `cost_estimate` | `usage_event_id`, `price_rate_id`, `estimated_usd`, `estimation_status`, `calculated_at` | Recalculable rather than assumed bill |
| `billing_observation` | `observed_at`, `period_start`, `period_end`, `amount`, `currency`, `scope`, `source_note` | Manual dashboard observation; distinguish billed, outstanding, usage |
| `warning_rule` | `period`, `threshold`, `currency`, `enabled` | Visibility/alerts only |

**Event ID strategy:** Prefer a native durable event ID if present; otherwise derive from normalized source identifier + session ID + durable sequence number/byte offset + event kind. Use database uniqueness constraints. Never hash or store secret-bearing payloads.

**Project label strategy:** Use an explicit mapping file or a project/workspace identifier already in event metadata. If no reliable label is available, show `unassigned` rather than guessing from user prompts.

## 9. Cost estimation and billing semantics

For token-priced models, where `cached_input_tokens` is a subset of `input_tokens`:

```text
uncached_input = max(0, input_tokens - cached_input_tokens)

token_estimate_usd =
    uncached_input     * input_usd_per_1m        / 1_000_000
  + cached_input      * cached_input_usd_per_1m / 1_000_000
  + output_tokens     * output_usd_per_1m       / 1_000_000
```

Additional charges must be a distinct component **only when observed/estimable**, such as hosted web searches or image generations. Do not silently label a token-only estimate as complete. Avoid separately charging reasoning tokens if they're already included in the provider's output-token total. Treat cache-write fields carefully until Meta's semantics are verified for the specific event.

**Unknown input is not free input:** If a model is missing from the price table, report `cost_unknown` rather than `$0.00`.

**Price provenance:** Each installed pricing snapshot contains a URL, retrieval date, model ID, effective date, and currency. Never backfill old usage with today's rates without explicitly indicating a recalculation.

**Modes:**

- **Pay-as-you-go:** Estimated API costs approximate usage charges, subject to extras, pricing, credits, billing delay and taxes.
- **Subscription:** Show tokens and API-equivalent value as a **comparison only**; do not represent it as the marginal billed cost of an included subscription session.
- **Unknown:** Show token usage with an explicit billing-mode warning.

## 10. CLI design (proposed)

```bash
# Import historical and new session usage
muse-cost scan

# Open the live, filterable browser dashboard (scans while open)
muse-cost show
muse-cost show --interval 15 --period today
muse-cost show --project my-project --model <model-id> --billing-mode payg
muse-cost show --session <session-id>
muse-cost show --from 2026-10-01 --to 2026-10-24

# View reports
muse-cost today
muse-cost week
muse-cost month
muse-cost sessions --sort estimated-cost
muse-cost session <session-id>
muse-cost models
muse-cost projects

# Re-scan periodically while this WSL instance is running
muse-cost watch --interval 15

# Show coverage, parser health, unknown prices, and source discrepancies
muse-cost doctor

# Set warnings that DO NOT halt Meta-side usage
muse-cost alert set --monthly-usd 30 --at 50,80,100

# Enter an observed dashboard value (not automatic API sync)
muse-cost reconcile add --date YYYY-MM-DD --amount 12.34 \
  --currency CAD --kind outstanding-balance --scope team

# Portable, secret-free reports
muse-cost export --format csv --period month
muse-cost export --format json --period month
```

### Live browser dashboard behavior and validation

- Default to today's observed usage, with month-to-date context, token totals, and clearly separated estimated pay-as-you-go cost and subscription API-equivalent value. Manual billed observations remain separately labeled.
- Support date range, project, model, session, and billing-mode filters both at launch and interactively. Combine filters consistently across summary totals, breakdowns, and drill-downs. Show active filters and allow clearing them.
- Provide project/model/session breakdowns and session drill-down with parent/subagent attribution, source record references, and accounting/reconciliation status. Never render transcript bodies or secrets.
- Run an initial incremental scan, then scan and refresh at a configurable interval (default 15 seconds). Preserve filters and selection across updates; provide manual refresh and a clean exit.
- Show the latest successful scan time, scanning/error/stale status, unknown prices, and incomplete coverage. An empty filtered result is distinct from unavailable collection. Retain the last successful view on scan failure and label it stale.
- “Near-real-time” means observed after Muse writes complete usage records; do not imply per-token streaming or live provider billing. Partial lines wait for the next scan. No continuous-monitoring claim applies after the dashboard exits or WSL stops.
- Validate with deterministic fixtures first, then a live Muse session: note initial totals, generate usage, observe the recorded delta within one configured scan/refresh interval, filter to that session, inspect its provenance, and refresh repeatedly to confirm unchanged totals without new events.

The CLI should use transparent units and labels, e.g.:

```text
Meta Cost Center — October 2026 (illustrative)

Observed local tokens        2,418,920 input / 286,500 output
API-equivalent estimate      USD 4.81 (partial: search charges unknown)
Verified account bill        Not entered
Billing mode coverage        78% identified; 22% unknown
Missing/ambiguous sessions   2
Latest log scan              2026-10-24 11:20 local
WARNING                     $30 local estimate alert is NOT a spend cap
```

**Never conflate** a local estimate with an account bill in the headline.

## 11. Local warnings and safety

- Support **local** warning thresholds such as 50%, 80%, and 100% of a configured estimate budget.
- Emit CLI warnings and optionally desktop notifications later; do not assume notifications will fire when WSL is stopped.
- Optionally flag unusually fast observed estimated spend, without promising detection of all activity.
- No automatic process termination, API key revocation, or blocking in the MVP.
- Never claim this tool enforces a real financial cap. Meta explicitly states that its spend alert is **not** a hard cap.

## 12. Security and privacy requirements

- Default to **read-only** access to Muse session files.
- Never ingest or persist prompts, model responses, tool outputs, environment-variable contents, API keys, or Muse auth files.
- Query only the usage-related JSON fields required for analytics.
- Keep SQLite/config local; use restrictive file permissions (`0700` directories, `0600` sensitive local files where possible).
- Scrub file paths in shared exports unless explicitly requested; project paths can contain personal information.
- Do not add telemetry, remote servers, OAuth browser automation, or dashboard scraping to the MVP.
- Use synthetic/redacted fixtures in the public Git repository.
- Advise users that local session coverage can be incomplete and that direct API calls from other machines may not appear.

## 13. Implementation milestones

### Phase 0 — Evidence and fixtures

**Deliverables:** Source inventory, sample redacted logs, versioned schema notes.

- [ ] Record installed Muse Code version and verify `/usage` output.
- [ ] Generate one interactive session, one headless run, and one subagent run.
- [ ] Confirm the observed `model_completed` fields and `run.usage` JSON shapes.
- [ ] Exercise partial/failed runs and disabled logging.
- [ ] Confirm available fields for model ID, project/workspace and billing mode.

**Exit gate:** Fixture-driven understanding of the schemas, with known gaps explicitly documented.

### Phase 1 — Reliable local collector

**Deliverables:** `scan`, SQLite schema, normalized event records.

- [ ] Discover files (including child agents); parse usage-only fields.
- [ ] Implement incremental ingestion and event deduplication.
- [ ] Build interactive + headless adapters.
- [ ] Reconcile event totals and run summaries; surface mismatches.
- [ ] Handle file growth, damaged lines, restarts, and duplicate scans.

**Exit gate:** Repeated scans are idempotent; fixture totals reconcile or explain why they don't.

### Phase 2 — Estimator and reports

**Deliverables:** Versioned pricing, `show` live browser dashboard, `today`/`month`/`sessions` reports, exports.

- [ ] Add model-specific cached/uncached/output pricing and price snapshots.
- [ ] Preserve unknown prices and costs; support partial-cost statuses.
- [ ] Separate subscription comparison value from estimated pay-as-you-go cost.
- [ ] Report project/model/session totals with provenance indicators.
- [ ] Implement `show` using the shared incremental scanner and report queries.
- [ ] Add interactive filters, session/subagent drill-down, and refreshes that preserve filters and selection.
- [ ] Display freshness, collection errors, coverage gaps, and unknown/partial estimates.
- [ ] Validate live updates alongside Muse and demonstrate idempotent repeated refreshes.

**Exit gate:** Test fixtures produce deterministic calculations; a Muse session running alongside `muse-cost show` produces visible usage updates within one configured interval after complete records are written. Filters and drill-down explain the delta, and repeated refreshes do not double count. The UI never describes estimates as billed charges.

### Phase 3 — Warnings and manual reconciliation

**Deliverables:** Warnings, `doctor`, manual dashboard-entry workflow.

- [ ] Configurable local warning levels; evaluate during `scan`/`watch`.
- [ ] Store dated billing observations by scope and currency.
- [ ] Show comparable periods and explain that differences may reflect coverage, billing lag, extras, taxes, credits, and FX.
- [ ] Create README quickstart, limitations, and threat-model notes.

**Exit gate:** A developer can inspect personal usage and enter one dashboard observation without exposing secrets.

### Phase 4 — Post-MVP candidates (not committed)

- Additional browser dashboard visualizations and cross-machine views.
- Cross-machine imports and direct Model API SDK adapters.
- Optional desktop/Windows Task Scheduler integrations.
- Verified account usage/billing API integration **if** Meta publishes a supported endpoint.
- Shared cost-policy engine for a future KeyGuard/managed proxy, with explicit opt-in enforcement.

## 14. Test and validation matrix

| Scenario | Expected result |
|---|---|
| Scan same log twice | Same totals, zero duplicate records |
| Dashboard observes new complete records | Totals update within one configured interval; partial records wait |
| Dashboard refreshes repeatedly without new records | Totals remain unchanged; filters and selection persist |
| Combined dashboard filters and session drill-down | Totals match shared report queries; parent/subagent provenance remains visible |
| Dashboard scan fails or sources are unavailable | Last successful data is labeled stale; failure/coverage status is visible |
| Dashboard filter matches no events | Explicit empty result, distinct from unavailable collection |
| Scan during log write | No crash; incomplete line processed later |
| Session with subagent | Correct parent/child attribution, no duplicate total |
| `run.usage` and model events both present | Reconciled, **not summed together** |
| Missing `run.usage` | Fallback to retained events with appropriate status |
| Session logging disabled | Incomplete-coverage warning |
| Unknown model/rate | `unknown` cost, not zero |
| Cached input reported | Uses cached-input rate correctly |
| Reasoning tokens included in output | No extra double charge |
| Headless run fails after token consumption | Counts observed usage, marks run failed |
| Subscription session | API-equivalent amount **not** presented as charge |
| Billing observation in CAD vs estimates in USD | Values not compared without explicit FX assumption |
| WSL shuts down | No claim of continuous monitoring; next scan catches retained events |
| Secret-shaped data in source log | Not stored or exported |

## 15. Key risks and decisions

| Risk / open question | Mitigation / decision |
|---|---|
| Session schemas evolve between Muse versions | Version-aware source adapters; fixture tests; parser warnings |
| `run.usage` field structure or breakdown varies | Capture real samples before assuming field names |
| Subagent and observer usage is difficult to reconcile | Explicit hierarchy + totals audit; choose one accounting authority per run |
| Some usage happens outside the local WSL instance | Clearly scope all totals to observed sources |
| Pricing changes or non-token charges appear | Versioned price table; partial-cost indicators |
| Muse subscription vs pay-as-you-go is ambiguous | User-specified mode or reliable metadata; otherwise `unknown` |
| No supported billing endpoint | Manual reconciliation only; no scraping credentials |
| WSL instance isn't always running | `scan` works on demand; `watch` is best-effort while active |
| Excessive scope from premature enforcement features | Ship an **observer**, not a proxy or KeyGuard clone |

## 16. Suggested first GitHub issues

1. **Capture and redact Muse usage fixtures** — interactive, headless, subagent, failure cases.
2. **Extract reusable usage parsing from Q001 benchmark** — compatibility tests included.
3. **Implement local SQLite ingestion with idempotent rescans**.
4. **Add `run.usage` adapter and double-count prevention**.
5. **Add model/cached-token pricing with source snapshots**.
6. **Implement live browser dashboard and shared day/month/session reporting** — filters, session/subagent drill-down, freshness indicators, and live validation.
7. **Implement `doctor`, local alerts and manual billing reconciliation**.
8. **Publish README, accuracy limits and security guidance**.

## 17. Out of scope for MVP

- Key creation, rotation, revocation or browser-extension automation (Muse KeyGuard).
- Guaranteed hard spending limits or stopping requests at a provider.
- Tracking all team activity across machines/accounts automatically.
- A verified, real-time Meta billing balance API (not established).
- Collection of secret values or full Muse transcripts.
- General-purpose multi-provider SaaS dashboards.
- Background service availability while WSL or Windows is off.

## 18. References and verified platform facts

These links were checked on **2026-10-24**; APIs, pricing and documented behavior may change.

1. [Muse Code changelog](https://dev.meta.ai/docs/muse-code/changelog) — `run.usage` in `muse exec --json` (1.4.11+); `/usage` background usage accounting.
2. [Audit and resume agent sessions](https://dev.meta.ai/docs/cookbook/audit-agent-sessions) — durable session JSONL paths and subagent logs.
3. [Muse Code interactive documentation](https://dev.meta.ai/docs/muse-code/interactive) — interactive session recording and logging-disable behavior.
4. [Meta Model API pricing and rate limits](https://dev.meta.ai/docs/pricing-rate-limits) — model-specific rates, cached input, and selected non-token charges.
5. [Muse Code subscriptions](https://dev.meta.ai/docs/muse-code/subscriptions) — subscription versus pay-as-you-go billing and additional API keys.
6. [Meta spend-limit alert help](https://dev.meta.ai/help/billing/spend-limit-alert) — emails do not enforce a spending cap.
7. [Existing Q001 `run_benchmark.py`](https://github.com/omenking/meta-ai-field-notes/blob/main/docs/q001-programatic-user-wonder-full/run_benchmark.py) — current headless logging and token/cost collection to evolve.

---

**MVP definition of done:** With only local WSL Muse logs and a versioned pricing file, `muse-cost show` can display and filter **observed** interactive and headless usage as local logs update, preserve filters across refreshes without double counting, explain missing coverage or disputed totals, and display defensible **estimated** costs—without collecting secrets or misrepresenting estimated spend as Meta's actual bill.

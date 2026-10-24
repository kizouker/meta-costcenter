import json
from collections import defaultdict
from datetime import date, datetime, timedelta
from decimal import Decimal
from pathlib import Path


def load_prices(path):
    if not path:
        return []
    data = json.loads(Path(path).expanduser().read_text())
    rates = data['rates']
    for r in rates:
        for key in ('model', 'effective_from', 'source_url', 'snapshot_version', 'retrieved_at'):
            if not r.get(key):
                raise ValueError(f'Pricing rate requires {key}')
        date.fromisoformat(r['effective_from'])
        if r.get('currency') != 'USD':
            raise ValueError('Pricing currency must be USD')
        for key in ('input_per_million', 'cached_input_per_million', 'output_per_million'):
            v = Decimal(str(r[key]))
            if not v.is_finite() or v < 0:
                raise ValueError('Rates must be finite and nonnegative')
    return rates


def estimate(row, rates):
    matches = [r for r in rates if r['model'] == row['model']
               and r['effective_from'] <= row['recorded_at'][:10]]
    if not matches or row['cached'] is None:
        return None, None
    rate = max(matches, key=lambda r: r['effective_from'])
    value = ((row['input'] - row['cached']) * Decimal(str(rate['input_per_million']))
             + row['cached'] * Decimal(str(rate['cached_input_per_million']))
             + row['output'] * Decimal(str(rate['output_per_million']))) / Decimal(1_000_000)
    return value, rate['snapshot_version']


def bounds(filters):
    today = date.today()
    period = filters.get('period', 'today')
    start = {'today': today, 'week': today-timedelta(days=6),
             'month': today.replace(day=1), 'all': None}[period]
    start = date.fromisoformat(filters['from']) if filters.get('from') else start
    end = date.fromisoformat(filters['to']) if filters.get('to') else today
    if start and start > end:
        raise ValueError('Start date must be on or before end date')
    return start, end


class NotFound(Exception):
    """A drill-down target (session or project) that has no recorded usage."""


def parents(db):
    """Map each session to its parent session (None for top-level sessions)."""
    return {s: p for s, p in db.execute('SELECT session, MAX(parent) FROM events GROUP BY session')}


def family(parent_of, session):
    """The session and all of its descendants."""
    members = {session}
    while True:
        children = {s for s, p in parent_of.items() if p in members}
        if children <= members:
            return members
        members |= children


def roots(parent_of):
    """Map each session to its top-level ancestor; unknown parents make a session a root."""
    result = {}
    for session in parent_of:
        node, seen = session, {session}
        while parent_of.get(node) in parent_of and parent_of[node] not in seen:
            node = parent_of[node]
            seen.add(node)
        result[session] = node
    return result


def inherited_projects(db, parent_of):
    """Subagent logs carry no workspace metadata; attribute them to the nearest known ancestor."""
    known = dict(db.execute("SELECT session, MAX(project) FROM events WHERE project != 'unassigned' GROUP BY session"))
    result = {}
    for session in parent_of:
        node, seen = session, set()
        while node is not None and node not in known and node not in seen:
            seen.add(node)
            node = parent_of.get(node)
        if node in known:
            result[session] = known[node]
    return result


def query(db, filters):
    start, end = bounds(filters)
    # Local calendar dates (including historical DST) rather than UTC-day boundaries.
    rows = [dict(r) for r in db.execute('SELECT * FROM events ORDER BY recorded_at, id')]
    parent_of = parents(db)
    projects = inherited_projects(db, parent_of)
    selected = filters.get('session')
    members = family(parent_of, selected) if selected else set()
    result = []
    for row in rows:
        if row['project'] == 'unassigned':
            row['project'] = projects.get(row['session'], 'unassigned')
        day = datetime.fromisoformat(row['recorded_at']).astimezone().date()
        if (start and day < start) or day > end:
            continue
        if selected and row['session'] not in members:
            continue
        if any(filters.get(k) and row[column] != filters[k] for k, column in
               [('project','project'), ('model','model'), ('billing_mode','billing_mode')]):
            continue
        result.append(row)
    return result


def totals(rows, rates):
    result = {'events': len(rows), 'input': 0, 'output': 0, 'cached': 0,
              'cache_unknown_events': 0, 'cost_unknown_events': 0,
              'payg_usd': Decimal(0), 'subscription_equivalent_usd': Decimal(0),
              'unknown_mode_equivalent_usd': Decimal(0)}
    for row in rows:
        for key in ('input', 'output'):
            result[key] += row[key]
        result['cached'] += row['cached'] or 0
        result['cache_unknown_events'] += row['cached'] is None
        value, _ = estimate(row, rates)
        if value is None:
            result['cost_unknown_events'] += 1
        else:
            key = {'payg': 'payg_usd', 'subscription': 'subscription_equivalent_usd'}.get(
                row['billing_mode'], 'unknown_mode_equivalent_usd')
            result[key] += value
    return result


def equivalent_usd(t):
    return t['payg_usd'] + t['subscription_equivalent_usd'] + t['unknown_mode_equivalent_usd']


def local(recorded_at):
    return datetime.fromisoformat(recorded_at).astimezone()


def ranked(rows, field):
    """Distinct values of field, most tokens first."""
    weight = defaultdict(int)
    for r in rows:
        weight[r[field]] += r['input'] + r['output']
    return sorted(weight, key=weight.get, reverse=True)


def grouped(rows, field, rates):
    buckets = defaultdict(list)
    for r in rows:
        buckets[r[field]].append(r)
    result = [{'label': label, **totals(items, rates)} for label, items in buckets.items()]
    return sorted(result, key=lambda r: r['input'] + r['output'], reverse=True)


def timeline(rows, rates, unit='day'):
    fmt = '%Y-%m-%dT%H:00' if unit == 'hour' else '%Y-%m-%d'
    buckets = defaultdict(list)
    for r in rows:
        buckets[local(r['recorded_at']).strftime(fmt)].append(r)
    return [{'bucket': key, **totals(items, rates)} for key, items in sorted(buckets.items())]


def sessions(db, filters, rates, rows=None):
    """One summary per top-level session; subagent usage rolls up into its root."""
    rows = query(db, filters) if rows is None else rows
    root_of = roots(parents(db))
    families = defaultdict(list)
    for r in rows:
        families[root_of.get(r['session'], r['session'])].append(r)
    result = []
    for root, items in families.items():
        own = [r for r in items if r['session'] == root] or items
        result.append({'id': root, 'project': ranked(own, 'project')[0],
                       'started': items[0]['recorded_at'], 'last_activity': items[-1]['recorded_at'],
                       'models': ranked(items, 'model'), 'billing_modes': ranked(items, 'billing_mode'),
                       'subagents': len({r['session'] for r in items} - {root}),
                       **totals(items, rates)})
    return sorted(result, key=lambda s: s['last_activity'], reverse=True)


def projects(db, filters, rates, rows=None):
    rows = query(db, filters) if rows is None else rows
    root_of = roots(parents(db))
    buckets = defaultdict(list)
    for r in rows:
        buckets[r['project']].append(r)
    result = [{'label': label, **totals(items, rates),
               'sessions': len({root_of.get(r['session'], r['session']) for r in items}),
               'last_activity': items[-1]['recorded_at']} for label, items in buckets.items()]
    return sorted(result, key=lambda r: r['input'] + r['output'], reverse=True)


def period_only(filters):
    """Detail pages are scoped by their URL; only the date range carries over."""
    return {k: filters.get(k) for k in ('period', 'from', 'to')}


def with_estimates(rows, rates):
    result = []
    for r in rows:
        value, snapshot = estimate(r, rates)
        result.append({**{k: v for k, v in r.items() if k != 'source'},
                       'api_equivalent_usd': value, 'pricing_snapshot': snapshot})
    return result


def session_detail(db, session, filters, rates):
    parent_of = parents(db)
    if session not in parent_of:
        raise NotFound(f'Unknown session: {session}')
    rows = query(db, dict(period_only(filters), session=session))
    members = family(parent_of, session)
    by_session = defaultdict(list)
    for r in rows:
        by_session[r['session']].append(r)

    def node(sid, seen=frozenset()):
        seen = seen | {sid}
        children = [c for c in (node(c, seen) for c in sorted(members) if parent_of[c] == sid and c not in seen) if c]
        own = by_session.get(sid, [])
        if not own and not children and sid != session:
            return None
        return {'id': sid, 'project': ranked(own, 'project')[0] if own else None,
                **totals(own, rates), 'children': children}

    span = (local(rows[-1]['recorded_at']) - local(rows[0]['recorded_at'])) if rows else None
    unit = 'hour' if span is not None and span.days < 2 else 'day'
    project = db.execute('SELECT project FROM events WHERE session=? GROUP BY project '
                         'ORDER BY COUNT(*) DESC LIMIT 1', (session,)).fetchone()
    return {'id': session, 'parent': parent_of[session], 'root': roots(parent_of)[session],
            'project': project[0] if project else None,
            'started': rows[0]['recorded_at'] if rows else None,
            'last_activity': rows[-1]['recorded_at'] if rows else None,
            'billing_modes': ranked(rows, 'billing_mode'),
            'totals': totals(rows, rates), 'own': totals(by_session.get(session, []), rates),
            'timeline': timeline(rows, rates, unit), 'timeline_unit': unit,
            'tree': node(session), 'models': grouped(rows, 'model', rates),
            'events': with_estimates(rows, rates)}


def project_detail(db, project, filters, rates):
    if not db.execute('SELECT 1 FROM events WHERE project=? LIMIT 1', (project,)).fetchone():
        raise NotFound(f'Unknown project: {project}')
    rows = query(db, dict(period_only(filters), project=project))
    return {'project': project, 'totals': totals(rows, rates), 'timeline': timeline(rows, rates),
            'models': grouped(rows, 'model', rates), 'sessions': sessions(db, {}, rates, rows)}


def reconciliation(db):
    results = []
    for s in db.execute('SELECT * FROM summaries ORDER BY recorded_at'):
        # Summary is a comparison only. Include the whole descendant family.
        rows = query(db, {'period':'all', 'session': s['session']})
        rows = [r for r in rows if r['model'] == s['model'] and r['recorded_at'] <= s['recorded_at']]
        inp = sum(r['input'] for r in rows)
        out = sum(r['output'] for r in rows)
        results.append({'session':s['session'], 'model':s['model'],
                        'status': 'match' if (inp, out) == (s['input'], s['output']) else 'mismatch / scope unverified',
                        'summary_input':s['input'], 'observed_input':inp,
                        'summary_output':s['output'], 'observed_output':out})
    return results


def money(value, unknown=0):
    return f'USD {value:,.6f}' + (' (known subtotal; incomplete)' if unknown else ' (token-only estimate)')


def lines(db, filters, rates, group='model', scan_result=None):
    rows = query(db, filters)
    t = totals(rows, rates)
    source = db.execute('SELECT MAX(scanned_at), SUM(errors), SUM(pending) FROM sources').fetchone()
    active = ', '.join(f'{k}={v}' for k,v in filters.items() if v) or 'today'
    month_filters = dict(filters, period='month', **{'from': None, 'to': None})
    month = totals(query(db, month_filters), rates)
    text = ['MUSE COST | observed local usage', f'Filters: {active}',
            f'Last scan: {source[0] or "never"} | malformed/unsupported: {source[1] or 0} | partial lines: {source[2] or 0}',
            f'Month-to-date (same non-date filters): input {month["input"]:,} | output {month["output"]:,}',
            f'Events {t["events"]:,} | Input {t["input"]:,} | Output {t["output"]:,} | Cached {t["cached"]:,}',
            f'Estimated PAYG: {money(t["payg_usd"], t["cost_unknown_events"]) if t["cost_unknown_events"] < t["events"] else "unknown / no priced observations"}',
            f'Subscription API-equivalent: {money(t["subscription_equivalent_usd"], t["cost_unknown_events"]) if t["cost_unknown_events"] < t["events"] else "unknown / no priced observations"}',
            f'Unknown billing mode API-equivalent: {money(t["unknown_mode_equivalent_usd"], t["cost_unknown_events"]) if t["cost_unknown_events"] < t["events"] else "unknown / no priced observations"}',
            f'Cost unknown: {t["cost_unknown_events"]} events | Cache unknown: {t["cache_unknown_events"]} events',
            'Coverage: local retained logs only; non-token charges unknown; estimates are not bills.']
    if not rows:
        text.append('No events match filters. Collection availability is shown separately above/below.')
    if scan_result:
        text += ['WARNING: '+w for w in scan_result['warnings']]
    groups = defaultdict(list)
    for row in rows:
        groups[row[group]].append(row)
    text += ['', f'{group.upper():36} {"INPUT":>12} {"OUTPUT":>12} {"API EQ USD*":>14} {"EVENTS":>8}']
    items_list = list(groups.items())
    if filters.get('sort') == 'estimated-cost':
        items_list.sort(key=lambda pair: sum((estimate(r, rates)[0] or Decimal(0) for r in pair[1]), Decimal(0)), reverse=True)
    else:
        items_list.sort()
    for label, items in items_list:
        v = totals(items, rates)
        cost = v['payg_usd'] + v['subscription_equivalent_usd'] + v['unknown_mode_equivalent_usd']
        display = 'unknown' if v['cost_unknown_events'] == len(items) else f'{cost:.6f}' + ('+' if v['cost_unknown_events'] else '')
        text.append(f'{label[:36]:36} {v["input"]:12,} {v["output"]:12,} {display:>14} {len(items):8,}')
    text.append('* API-equivalent token estimate, not a bill; + means incomplete subtotal.')
    if filters.get('session'):
        text += ['', 'PROVENANCE (last 30 events; session filter includes descendants)']
        for r in rows[-30:]:
            text.append(f'{r["recorded_at"]} {r["session"]} parent={r["parent"] or "none"} {r["model"]} '
                        f'in={r["input"]} out={r["output"]} record={r["sequence"]} id={r["id"]}')
    observations = list(db.execute('SELECT * FROM observations ORDER BY observed_at DESC LIMIT 3'))
    summary_count = db.execute('SELECT COUNT(*) FROM summaries').fetchone()[0]
    if summary_count:
        text += [f'Run summaries: {summary_count} comparison records excluded from event totals; run doctor for reconciliation.']
    text += ['', 'MANUAL ACCOUNT OBSERVATIONS (separate scope/currency; never subtracted from local estimates)']
    text += [f'{r["period_start"]}..{r["period_end"]} {r["currency"]} {r["amount"]} {r["kind"]} scope={r["scope"]}' for r in observations] or ['Not entered']
    threshold = db.execute("SELECT value FROM settings WHERE key='monthly_usd'").fetchone()
    if threshold:
        monthly = totals(query(db, {'period':'month'}), rates)
        observed = monthly['payg_usd'] + monthly['subscription_equivalent_usd'] + monthly['unknown_mode_equivalent_usd']
        text += [f'Local month API-equivalent warning threshold: USD {threshold[0]} (NOT a spend cap)']
        levels_row = db.execute("SELECT value FROM settings WHERE key='alert_levels'").fetchone()
        levels = json.loads(levels_row[0]) if levels_row else [50,80,100]
        crossed = [level for level in levels if observed >= Decimal(threshold[0]) * Decimal(level) / 100]
        if crossed:
            text.append(f'WARNING: crossed {max(crossed)}% level; month known API-equivalent subtotal USD {observed:.6f}; threshold USD {threshold[0]}')
    return text

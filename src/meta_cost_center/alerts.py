"""Local cost alerts: a USD limit per day, week, or month, for all projects or one."""
import json
from datetime import date, datetime, timedelta, timezone
from decimal import Decimal, InvalidOperation

from .reports import NotFound, equivalent_usd, local, query, totals

PERIODS = ('day', 'week', 'month')
PERIOD_NAMES = {'day': 'daily', 'week': 'weekly', 'month': 'monthly'}
MAX_WARNINGS = 4


def window(period, today=None):
    """Local calendar window containing today: the day, the Monday-start week, or the month."""
    today = today or date.today()
    start = {'day': today, 'week': today - timedelta(days=today.weekday()), 'month': today.replace(day=1)}[period]
    return start, today


def validate(body):
    if not isinstance(body, dict):
        raise ValueError('Alert must be a JSON object')
    period = body.get('period')
    if period not in PERIODS:
        raise ValueError('Period must be day, week, or month')
    project = body.get('project') or None
    if project is not None and (not isinstance(project, str) or len(project) > 200):
        raise ValueError('Project must be a name of at most 200 characters')
    billing_mode = body.get('billing_mode') or None
    if billing_mode not in (None, 'payg'):
        raise ValueError('Billing scope must be all usage or payg')
    try:
        limit = Decimal(str(body.get('limit_usd')))
    except InvalidOperation:
        raise ValueError('Limit must be a number') from None
    if not limit.is_finite() or limit <= 0:
        raise ValueError('Limit must be greater than zero')
    levels = body.get('warn_levels', [80])
    if not isinstance(levels, list) or not 1 <= len(levels) <= MAX_WARNINGS:
        raise ValueError(f'Set between 1 and {MAX_WARNINGS} warning levels')
    if any(not isinstance(v, int) or isinstance(v, bool) or not 1 <= v <= 99 for v in levels):
        raise ValueError('Warning levels must be whole percentages from 1 to 99')
    if len(set(levels)) != len(levels):
        raise ValueError('Warning levels must be different from each other')
    return {'period': period, 'project': project, 'billing_mode': billing_mode, 'limit_usd': str(limit),
            'warn_levels': json.dumps(sorted(levels)), 'enabled': 1 if body.get('enabled', True) else 0}


def create(db, body):
    alert = validate(body)
    with db:
        cursor = db.execute('INSERT INTO alerts (period, project, billing_mode, limit_usd, warn_levels, enabled, created_at) '
                            'VALUES (:period, :project, :billing_mode, :limit_usd, :warn_levels, :enabled, :created_at)',
                            {**alert, 'created_at': datetime.now(timezone.utc).isoformat()})
    return cursor.lastrowid


def update(db, alert_id, body):
    alert = validate(body)
    with db:
        if not db.execute('UPDATE alerts SET period=:period, project=:project, billing_mode=:billing_mode, limit_usd=:limit_usd, '
                          'warn_levels=:warn_levels, enabled=:enabled WHERE id=:id', {**alert, 'id': alert_id}).rowcount:
            raise NotFound(f'Unknown alert: {alert_id}')


def delete(db, alert_id):
    with db:
        if not db.execute('DELETE FROM alerts WHERE id=?', (alert_id,)).rowcount:
            raise NotFound(f'Unknown alert: {alert_id}')


def describe(alert):
    scope = alert['project'] or 'all projects'
    mode = ', pay as you go only' if alert['billing_mode'] == 'payg' else ''
    return f'{PERIOD_NAMES[alert["period"]].capitalize()} limit for {scope}{mode}'


def status(db, rates, today=None):
    """Current spend against every alert. Unpriced usage is excluded, so spend is a lower bound."""
    alerts = [dict(r) for r in db.execute('SELECT * FROM alerts ORDER BY id')]
    if not alerts:
        return []
    starts = {a['id']: window(a['period'], today) for a in alerts}
    earliest = min(start for start, _ in starts.values())
    rows = query(db, {'period': 'all', 'from': earliest.isoformat(), 'to': (today or date.today()).isoformat()})
    days = [(local(r['recorded_at']).date(), r) for r in rows]
    result = []
    for a in alerts:
        start, end = starts[a['id']]
        items = [r for day, r in days if day >= start
                 and (not a['project'] or r['project'] == a['project'])
                 and (not a['billing_mode'] or r['billing_mode'] == a['billing_mode'])]
        t = totals(items, rates)
        spent, limit = equivalent_usd(t), Decimal(a['limit_usd'])
        percent = spent / limit * 100
        levels = json.loads(a['warn_levels'])
        # The highest warning level reached; each one notifies separately.
        warning_at = max((v for v in levels if percent >= v), default=None)
        level = 'paused' if not a['enabled'] else 'exceeded' if percent >= 100 else 'warning' if warning_at else 'ok'
        result.append({**a, 'warn_levels': levels, 'warning_at': warning_at, 'enabled': bool(a['enabled']), 'label': describe(a),
                       'window_start': start.isoformat(), 'window_end': end.isoformat(),
                       'spent_usd': spent, 'percent': round(float(percent), 1), 'level': level,
                       'unpriced_events': t['cost_unknown_events'], 'assumed_events': t['cost_assumed_events']})
    return result

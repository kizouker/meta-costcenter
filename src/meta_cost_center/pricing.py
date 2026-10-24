"""Model rates: public rates ship with the package; local rates stay in the local database."""
from collections import defaultdict
from datetime import datetime, timezone
from decimal import Decimal, InvalidOperation

from .reports import RATE_FIELDS, NotFound, query, rate_for, totals


def validate(body):
    if not isinstance(body, dict):
        raise ValueError('Rate must be a JSON object')
    result = {}
    for key in RATE_FIELDS:
        try:
            value = Decimal(str(body.get(key)))
        except InvalidOperation:
            raise ValueError('Rates must be numbers') from None
        if not value.is_finite() or value < 0:
            raise ValueError('Rates must be finite and nonnegative')
        result[key] = str(value)
    return result


def set_local(db, model, body):
    if not model or len(model) > 200:
        raise ValueError('Model must be a name of at most 200 characters')
    rate = validate(body)
    with db:
        db.execute('INSERT OR REPLACE INTO rates VALUES (:model, :input_per_million, :cached_input_per_million, '
                   ':output_per_million, :updated_at)', {**rate, 'model': model, 'updated_at': datetime.now(timezone.utc).isoformat()})


def delete_local(db, model):
    with db:
        if not db.execute('DELETE FROM rates WHERE model=?', (model,)).rowcount:
            raise NotFound(f'No local rate for {model}')


def table(db, rates):
    """Every model seen in usage, with its rate source and all-time estimated cost."""
    rows = query(db, {'period': 'all'})
    by_model = defaultdict(list)
    for r in rows:
        by_model[r['model']].append(r)
    total_tokens = sum(r['input'] + r['output'] for r in rows)
    result = []
    for model, items in by_model.items():
        t = totals(items, rates)
        public = [r for r in rates if r['model'] == model and not r.get('local')]
        local = next((r for r in rates if r['model'] == model and r.get('local')), None)
        rate, _ = rate_for(model, items[-1]['recorded_at'][:10], rates)
        source = 'local' if local else 'public' if public else 'missing'
        result.append({'model': model, **t, 'source': source,
                       'rate': {k: rate[k] for k in RATE_FIELDS} if rate else None,
                       'public_rate': {k: public[-1][k] for k in RATE_FIELDS} if public else None,
                       'verified_from': min(r['effective_from'] for r in public) if public else None,
                       'source_url': public[-1].get('source_url') if public else None,
                       'token_share': (t['input'] + t['output']) / total_tokens * 100 if total_tokens else 0})
    order = {'missing': 0, 'local': 1, 'public': 2}
    return sorted(result, key=lambda m: (order[m['source']], -(m['input'] + m['output'])))

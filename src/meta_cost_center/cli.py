import argparse
import csv
import json
import os
import sqlite3
import sys
import time
import tomllib
from datetime import date
from decimal import Decimal, InvalidOperation
from pathlib import Path

from .collector import scan
from . import alerts
from .reports import bounds, estimate, lines, load_prices, query, reconciliation, with_local_rates
from .store import connect


def positive(value):
    number = float(value)
    if not 0.1 <= number <= 86400:
        raise argparse.ArgumentTypeError('Interval must be between 0.1 and 86400 seconds')
    return number


def parser():
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument('--db', default=argparse.SUPPRESS, help='SQLite database path')
    common.add_argument('--source', action='append', default=argparse.SUPPRESS, help='Session root or JSONL file; repeatable')
    common.add_argument('--config', default=argparse.SUPPRESS, help='TOML config path')
    common.add_argument('--prices', default=argparse.SUPPRESS, help='Versioned pricing JSON path')
    p = argparse.ArgumentParser(description='Observe local Muse usage; estimates are not bills.', parents=[common])
    sub = p.add_subparsers(dest='command', required=True)
    for name in ('scan','doctor','watch','show','tui','today','week','month','sessions','session','models','projects','export'):
        s = sub.add_parser(name, parents=[common])
        if name in ('show','tui','watch'):
            s.add_argument('--interval',type=positive,default=15)
        if name in ('tui','watch'):
            s.add_argument('--once',action='store_true',help='Scan/render once and exit')
        if name == 'show':
            s.add_argument('--port', type=int, default=8765, help='Local browser dashboard port (0 selects a free port)')
            s.add_argument('--no-browser', action='store_true', help='Print URL without opening a browser')
        if name not in ('scan','doctor','watch'):
            s.add_argument('--period',choices=['today','week','month','all'],default=None)
            s.add_argument('--from',dest='start')
            s.add_argument('--to',dest='end')
            s.add_argument('--project')
            s.add_argument('--model')
            s.add_argument('--billing-mode',choices=['payg','subscription','unknown'])
            s.add_argument('--session',dest='session_filter')
        if name == 'session':
            s.add_argument('session_id')
        if name == 'sessions':
            s.add_argument('--sort',choices=['session','estimated-cost'],default='session')
        if name == 'export':
            s.add_argument('--format',choices=['json','csv'],default='json')
    r = sub.add_parser('reconcile',parents=[common])
    r.add_argument('action',choices=['add','list'])
    r.add_argument('--date',default=date.today().isoformat())
    r.add_argument('--from',dest='start')
    r.add_argument('--to',dest='end')
    r.add_argument('--amount')
    r.add_argument('--currency',default='USD')
    r.add_argument('--kind',default='billed',choices=['billed','outstanding-balance','usage'])
    r.add_argument('--scope',default='account')
    a = sub.add_parser('alert',parents=[common])
    a.add_argument('action',choices=['set','status'])
    a.add_argument('--monthly-usd')
    a.add_argument('--project', help='Limit the alert to one project (the Muse workspace folder name)')
    a.add_argument('--at', default='50,80,100', help='Comma-separated warning percentages')
    a.add_argument('--revoke-keys', metavar='PATTERN',
                   help='Ask Meta Keyguard to revoke keys matching PATTERN (*, bootcamp-*, or key1,key2) once the limit is exceeded')
    a.add_argument('--json', action='store_true', help='Print alert status as JSON (status always prints JSON)')
    return p


def decimal_amount(value):
    v = Decimal(value)
    if not v.is_finite() or v < 0:
        raise ValueError('Amount must be finite and nonnegative')
    return str(v)


def main():
    args = parser().parse_args()
    data = Path(os.environ.get('XDG_DATA_HOME',Path.home()/'.local/share'))
    config_path = Path(getattr(args,'config',Path(os.environ.get('XDG_CONFIG_HOME',Path.home()/'.config'))/'meta-cost-center/config.toml')).expanduser()
    db = None
    try:
        config = tomllib.loads(config_path.read_text()) if config_path.exists() else {}
        for meta in config.get('sessions',{}).values():
            if meta.get('billing_mode','unknown') not in ('payg','subscription','unknown'):
                raise ValueError('Config billing_mode must be payg, subscription, or unknown')
        roots = getattr(args,'source',config.get('sources',[str(data/'muse/sessions')]))
        rates = load_prices(getattr(args,'prices',config.get('prices', Path(__file__).with_name('pricing.json'))))
        filters = {'period':getattr(args,'period',None) or (args.command if args.command in ('today','week','month') else 'all' if args.command in ('sessions','session','models','projects') else 'today'),
                   'from':getattr(args,'start',None),'to':getattr(args,'end',None),
                   'project':getattr(args,'project',None),'model':getattr(args,'model',None),
                   'session':getattr(args,'session_id',None) or getattr(args,'session_filter',None),
                   'billing_mode':getattr(args,'billing_mode',None), 'sort':getattr(args,'sort',None)}
        if args.command not in ('reconcile','alert'):
            bounds(filters)
        db = connect(getattr(args,'db',config.get('db',data/'meta-cost-center/costs.sqlite3')))
        rates = with_local_rates(db, rates)
        if args.command == 'reconcile':
            if args.action == 'add':
                if args.amount is None:
                    raise ValueError('--amount is required')
                amount = decimal_amount(args.amount)
                start, end = args.start or args.date, args.end or args.date
                if date.fromisoformat(start) > date.fromisoformat(end):
                    raise ValueError('Invalid observation period')
                date.fromisoformat(args.date)
                if len(args.currency)!=3 or not args.currency.isalpha():
                    raise ValueError('Currency must be a three-letter code')
                with db:
                    db.execute('INSERT INTO observations VALUES (NULL,?,?,?,?,?,?,?)',
                               (args.date,start,end,amount,args.currency.upper(),args.kind,args.scope))
            print(json.dumps([dict(r) for r in db.execute('SELECT * FROM observations')],indent=2))
            return
        if args.command == 'alert' and args.action == 'status':
            # Meta Keyguard polls this to decide which keys to revoke, so it reads fresh usage first.
            scan(db, roots, config)
            print(json.dumps(alerts.status(db, rates), indent=2, default=str))
            return
        if args.command == 'alert':
            if args.monthly_usd is None:
                raise ValueError('--monthly-usd is required')
            value = decimal_amount(args.monthly_usd)
            levels = sorted(set(int(x) for x in args.at.split(',')))
            if not levels or any(x <= 0 or x > 100 for x in levels):
                raise ValueError('Warning percentages must be between 1 and 100')
            below = [x for x in levels if x < 100][:alerts.MAX_WARNINGS]
            alerts.create(db, {'period': 'month', 'project': args.project, 'limit_usd': value, 'warn_levels': below or [80],
                               'revoke_keys': args.revoke_keys is not None, 'key_match': args.revoke_keys})
            scope = f'project {args.project}' if args.project else 'all projects'
            action = (f'When it is exceeded, Meta Keyguard revokes keys matching {args.revoke_keys}, '
                      'provided its Cost Center check is on (`keyguard cost-center on`).'
                      if args.revoke_keys else 'Cost Center only warns you; it does not stop usage.')
            print(f'Added a monthly alert for {scope}: USD {value} of estimated local usage, priced at API rates. '
                  f'{action}\nManage alerts on the dashboard Alerts page.')
            return
        if args.command == 'show':
            from .webapp import run
            if not 0 <= args.port <= 65535:
                raise ValueError('Port must be between 0 and 65535')
            run(str(Path(db.execute('PRAGMA database_list').fetchone()[2])), roots, config, filters, rates,
                args.interval, args.port, not args.no_browser)
            return
        if args.command == 'tui' and not args.once and sys.stdout.isatty() and sys.stdin.isatty():
            from .dashboard import run
            run(db, roots, config, filters, rates, args.interval)
            return
        while True:
            result = scan(db,roots,config)
            if args.command == 'scan':
                print(json.dumps(result,indent=2))
            elif args.command == 'doctor':
                print(json.dumps({'scan':result,'sources':[dict(r) for r in db.execute('SELECT * FROM sources')],
                                  'reconciliation':reconciliation(db),
                                  'pricing_rates':len(rates),
                                  'limitations':['No account-wide coverage','No supported live billing sync',
                                    'run.usage upstream shape unverified; unsupported records are diagnosed',
                                    'Source replacements retain historical observations; provenance may be unavailable']},indent=2))
            elif args.command == 'export':
                rows = query(db,filters)
                output = []
                for r in rows:
                    value, snapshot, assumed = estimate(r,rates)
                    # Exports deliberately omit source paths.
                    output.append({k:v for k,v in r.items() if k!='source'} | {
                        'estimated_api_equivalent_usd':str(value) if value is not None else None,
                        'pricing_snapshot':snapshot, 'rate_assumed':assumed})
                if args.format=='json':
                    print(json.dumps(output,indent=2))
                elif output:
                    writer=csv.DictWriter(sys.stdout,fieldnames=list(output[0]))
                    writer.writeheader()
                    writer.writerows(output)
            else:
                group = {'sessions':'session','session':'session','projects':'project'}.get(args.command,'model')
                print('\n'.join(lines(db,filters,rates,group,result)))
                if args.command == 'tui':
                    print('\nInteractive filters require a terminal. Use --project/--model/--session/--from/--to for snapshots.')
            if args.command!='watch' or args.once:
                break
            time.sleep(args.interval)
    except KeyboardInterrupt:
        pass
    except (OSError, ValueError, KeyError, TypeError, sqlite3.Error, InvalidOperation) as exc:
        print(f'muse-cost: {type(exc).__name__}: {exc}',file=sys.stderr)
        raise SystemExit(1)
    finally:
        if db is not None:
            db.close()

if __name__ == '__main__':
    main()

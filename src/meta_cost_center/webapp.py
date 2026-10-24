"""Loopback-only dashboard, sharing the CLI collector and accounting queries."""
import csv
import errno
import io
import json
import re
import shutil
import secrets
import sqlite3
import subprocess
import threading
import webbrowser
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, unquote, urlsplit

from .collector import scan
from .reports import (NotFound, bounds, equivalent_usd, estimate, grouped, project_detail, projects, query,
                      session_detail, sessions, timeline, totals, with_estimates)
from .store import connect

FILTERS = ('period', 'from', 'to', 'project', 'model', 'session', 'billing_mode')
ASSETS = Path(__file__).with_name('web')
PAGES = re.compile(r'/|/(?:breakdown|activity|sessions|projects)|/(?:sessions|projects)/[^/]+')
API = re.compile(r'/api/(dashboard|export|sessions|projects)(?:/([^/]+))?')


def report(db, filters, rates, status):
    bounds(filters)
    rows = query(db, filters)
    monthly = query(db, dict(filters, period='month', **{'from': None, 'to': None}))
    groups = {field: grouped(rows, field, rates) for field in ('model', 'project', 'session', 'billing_mode')}
    days = [{'day': point['bucket'], **point} for point in timeline(rows, rates)]
    session_rows = sessions(db, filters, rates, rows)
    source = db.execute('SELECT MAX(scanned_at), SUM(errors), SUM(pending) FROM sources').fetchone()
    provenance = with_estimates(rows[-100:], rates)[::-1]
    options = {field: [r[0] for r in db.execute(f'SELECT DISTINCT {field} FROM events ORDER BY {field}')]
               for field in ('model', 'project', 'session')}
    warnings = list(status.get('warnings', []))
    if source[1]:
        warnings.append(f'{source[1]} malformed or unsupported records. Run muse-cost doctor for diagnostics.')
    summary_count = db.execute('SELECT COUNT(*) FROM summaries').fetchone()[0]
    if summary_count:
        warnings.append(f'{summary_count} run summary records are comparison-only and excluded from event totals.')
    threshold = db.execute("SELECT value FROM settings WHERE key='monthly_usd'").fetchone()
    return {'filters': filters, 'totals': totals(rows, rates), 'month': totals(monthly, rates),
            'groups': groups, 'timeline': days, 'events': provenance,
            'session_count': len(session_rows),
            'top_sessions': sorted(session_rows, key=equivalent_usd, reverse=True)[:5],
            'top_projects': sorted(projects(db, filters, rates, rows), key=equivalent_usd, reverse=True)[:5],
            'options': options,
            'monthly_warning_usd': threshold[0] if threshold else None,
            'status': {**status, 'warnings': warnings, 'last_scan': source[0], 'pending_lines': source[2] or 0},
            'source_count': db.execute('SELECT COUNT(*) FROM sources').fetchone()[0]}


class DashboardServer(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, address, db_path, roots, config, rates, filters, interval):
        self.db_path, self.roots, self.config = db_path, roots, config
        self.rates, self.filters, self.interval = rates, filters, interval
        self.instance_id = secrets.token_hex(12)
        self.state_lock = threading.Lock()
        self.state = {'scanning': True, 'last_success': None, 'error': None, 'warnings': []}
        self.stop_event, self.refresh_event = threading.Event(), threading.Event()
        self.worker = None
        db = connect(db_path)
        db.close()
        super().__init__(address, Handler)
        self.worker = threading.Thread(target=self.collect, daemon=True)
        self.worker.start()

    def collect(self):
        db = connect(self.db_path)
        try:
            while not self.stop_event.is_set():
                self.refresh_event.clear()
                with self.state_lock:
                    self.state['scanning'] = True
                try:
                    result = scan(db, self.roots, self.config)
                    with self.state_lock:
                        self.state.update(scanning=False, last_success=result['scanned_at'],
                                          error=None, warnings=result['warnings'], added=result['added'])
                except (OSError, ValueError, sqlite3.Error):
                    with self.state_lock:
                        self.state.update(scanning=False, error='Collection failed; showing last imported usage.')
                self.refresh_event.wait(self.interval)
        finally:
            db.close()

    def server_close(self):
        self.stop_event.set()
        self.refresh_event.set()
        super().server_close()
        if self.worker is not None and self.worker.is_alive():
            self.worker.join(timeout=5)


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def allowed(self):
        host = self.headers.get('Host', '')
        port = self.server.server_port
        if host not in (f'127.0.0.1:{port}', f'localhost:{port}'):
            return False
        origin = self.headers.get('Origin')
        return not origin or origin in (f'http://127.0.0.1:{port}', f'http://localhost:{port}')

    def send(self, code, data, kind='application/json; charset=utf-8', attachment=None):
        if not isinstance(data, bytes):
            data = json.dumps(data, default=str).encode()
        self.send_response(code)
        self.send_header('Content-Type', kind)
        self.send_header('Content-Length', str(len(data)))
        self.send_header('Cache-Control', 'no-store')
        self.send_header('X-Content-Type-Options', 'nosniff')
        self.send_header('Referrer-Policy', 'no-referrer')
        self.send_header('Content-Security-Policy', "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
        if attachment:
            self.send_header('Content-Disposition', f'attachment; filename="{attachment}"')
        self.end_headers()
        try:
            self.wfile.write(data)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def do_GET(self):
        if not self.allowed():
            return self.send(403, {'error': 'Local dashboard requests only.'})
        parsed = urlsplit(self.path)
        assets = {'/app.js': ('app.js', 'text/javascript; charset=utf-8'),
                  '/style.css': ('style.css', 'text/css; charset=utf-8'),
                  '/logo.png': ('logo.png', 'image/png')}
        if parsed.path in assets:
            name, mime = assets[parsed.path]
            return self.send(200, (ASSETS / name).read_bytes(), mime)
        if PAGES.fullmatch(parsed.path):
            # Client-side routes; deep links such as /sessions/<id> must survive a refresh.
            return self.send(200, (ASSETS / 'index.html').read_bytes(), 'text/html; charset=utf-8')
        if parsed.path == '/api/config':
            return self.send(200, {'filters': self.server.filters, 'instance_id': self.server.instance_id})
        route = API.fullmatch(parsed.path)
        if not route:
            return self.send(404, {'error': 'Not found'})
        endpoint, key = route.group(1), unquote(route.group(2)) if route.group(2) else None
        raw = parse_qs(parsed.query, keep_blank_values=True)
        defaults = {} if any(k in raw for k in FILTERS) else self.server.filters
        filters = {k: raw[k][-1] if k in raw else defaults.get(k) for k in FILTERS}
        filters['period'] = filters.get('period') or 'today'
        rates = self.server.rates
        db = None
        try:
            bounds(filters)
            if filters.get('billing_mode') not in (None, '', 'payg', 'subscription', 'unknown'):
                raise ValueError('Invalid billing mode')
            db = sqlite3.connect(self.server.db_path, timeout=10)
            db.row_factory = sqlite3.Row
            if endpoint == 'sessions':
                return self.send(200, session_detail(db, key, filters, rates) if key else sessions(db, filters, rates))
            if endpoint == 'projects':
                return self.send(200, project_detail(db, key, filters, rates) if key else projects(db, filters, rates))
            if key:
                return self.send(404, {'error': 'Not found'})
            if endpoint == 'export':
                rows = [{k: v for k,v in r.items() if k != 'source'} for r in query(db, filters)]
                for r in rows:
                    value, snapshot = estimate(r, rates)
                    r.update(estimated_api_equivalent_usd=str(value) if value is not None else None,
                             pricing_snapshot=snapshot)
                if raw.get('format') == ['csv']:
                    output = io.StringIO()
                    fields = list(rows[0]) if rows else ['id','session','model','input','output','cached']
                    writer = csv.DictWriter(output, fieldnames=fields)
                    writer.writeheader()
                    writer.writerows(rows)
                    return self.send(200, output.getvalue().encode(), 'text/csv; charset=utf-8', 'muse-usage.csv')
                return self.send(200, rows, attachment='muse-usage.json')
            with self.server.state_lock:
                status = dict(self.server.state)
            status['interval'] = self.server.interval
            return self.send(200, report(db, filters, rates, status))
        except NotFound as error:
            return self.send(404, {'error': str(error)})
        except (ValueError, KeyError):
            return self.send(400, {'error': 'Invalid filter. Check period, date range, and billing mode.'})
        except sqlite3.Error:
            return self.send(503, {'error': 'Usage database temporarily unavailable; retrying shortly.'})
        finally:
            if db is not None:
                db.close()

    def do_POST(self):
        if not self.allowed():
            return self.send(403, {'error': 'Local dashboard requests only.'})
        if self.path != '/api/refresh':
            return self.send(404, {'error': 'Not found'})
        self.server.refresh_event.set()
        self.send(202, {'scanning': True})


def open_browser(url):
    try:
        if shutil.which('wslview'):
            return subprocess.run(['wslview', url], timeout=10, capture_output=True).returncode == 0
        # Windows browser integration for WSL installations without wslview.
        if shutil.which('powershell.exe'):
            return subprocess.run(['powershell.exe', '-NoProfile', '-Command', 'Start-Process', url],
                                  timeout=10, capture_output=True).returncode == 0
        return webbrowser.open(url, new=2)
    except (OSError, subprocess.TimeoutExpired, webbrowser.Error):
        return False


def run(db_path, roots, config, filters, rates, interval=15, port=8765, browser=True):
    try:
        server = DashboardServer(('127.0.0.1', port), db_path, roots, config, rates, filters, interval)
    except OSError as exc:
        if exc.errno != errno.EADDRINUSE or port == 0:
            raise
        print(f'Port {port} is in use; selecting a free local port.', flush=True)
        server = DashboardServer(('127.0.0.1', 0), db_path, roots, config, rates, filters, interval)
    url = f'http://127.0.0.1:{server.server_port}/'
    print(f'Muse Cost dashboard: {url}\nOpening your browser…' if browser else f'Muse Cost dashboard: {url}', flush=True)
    print('Keep this command running for live updates. Press Ctrl+C to stop.', flush=True)
    if browser:
        def launch():
            if not open_browser(url):
                print(f'Could not open a browser automatically. Open {url}', flush=True)
        threading.Thread(target=launch, daemon=True).start()
    try:
        server.serve_forever(poll_interval=0.2)
    finally:
        server.server_close()

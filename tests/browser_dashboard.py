"""Optional end-to-end checks: requires Playwright and installed Chrome."""
import json
import shutil
import sys
import tempfile
import threading
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path[:0] = [str(ROOT / 'src'), str(ROOT / 'tests')]
from playwright.sync_api import sync_playwright, expect
from meta_cost_center.webapp import DashboardServer
from meta_cost_center.reports import load_prices
from test_core import event


def main():
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        logs = root / 'logs'
        for i, project in enumerate(['Website', 'CLI tools', 'Research', 'Website']):
            path = logs / f'session-{i}' / 'session.jsonl'
            path.parent.mkdir(parents=True)
            records = [{'payload_type': 'runtime.session.metadata', 'payload': {'record': {'workspace_root': f'/demo/{project}'}}}]
            records += [event(f'event-{i}-{j}', f'session-{i}', inp=10000*(i+1), cached=5000, model='muse-spark-1.3') for j in range(3)]
            path.write_text(''.join(json.dumps(r)+'\n' for r in records))
        # A subagent of session-3 with output-only usage, so input totals stay round.
        sub = logs / 'session-3' / 'subagent' / 'sub-3a' / 'session.jsonl'
        sub.parent.mkdir(parents=True)
        sub.write_text(''.join(json.dumps(r)+'\n' for r in [{'payload_type': 'runtime.session.metadata', 'payload': {'record': {'workspace_root': '/demo/Website'}}}]
                               + [event(f'sub-event-{j}', 'sub-3a', inp=0, cached=0, model='muse-spark-1.3') for j in range(2)]))
        rates = load_prices(ROOT / 'src/meta_cost_center/pricing.json')
        server = DashboardServer(('127.0.0.1', 0), root/'usage.sqlite3', [logs], {}, rates, {'period': 'all'}, .1)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with sync_playwright() as p:
                executable = shutil.which('google-chrome') or shutil.which('chromium')
                browser = p.chromium.launch(**({'executable_path': executable} if executable else {}), headless=True, args=['--no-sandbox'])
                page = browser.new_page(viewport={'width': 1440, 'height': 1100})
                errors, documents = [], []
                page.on('pageerror', lambda error: errors.append(str(error)))
                page.on('request', lambda request: documents.append(request.url) if request.resource_type == 'document' else None)
                base = f'http://127.0.0.1:{server.server_port}'
                def open_filters():
                    page.get_by_role('button', name='Filters', exact=True).click()
                    expect(page.get_by_role('dialog', name='Filters')).to_be_visible()
                def close_filters():
                    page.get_by_role('button', name='Close filters').click()
                    expect(page.get_by_role('dialog')).to_have_count(0)
                def check_filter(name, value):
                    open_filters()
                    expect(page.locator(f'[name={name}]')).to_have_value(value)
                    close_filters()
                page.goto(base + '/')
                expect(page.locator('#input')).to_have_text('300,000')
                expect(page.get_by_role('dialog')).to_have_count(0)
                assert page.url == base + '/'
                open_filters()
                expect(page.locator('[name=period]')).to_have_value('all')
                page.keyboard.press('Escape')
                expect(page.get_by_role('dialog')).to_have_count(0)
                expect(page.get_by_role('button', name='Filters', exact=True)).to_be_focused()
                page.get_by_role('button', name='Export usage').hover()
                expect(page.get_by_role('tooltip')).to_contain_text('current filters')
                page.keyboard.press('Escape')
                page.set_viewport_size({'width': 1440, 'height': 420})
                for label, heading, rows in (('Overview', 'Usage overview', '#input'), ('Sessions', 'Sessions', '#session-rows'),
                                             ('Projects', 'Projects', '#project-rows'), ('Breakdown', 'Usage breakdown', '#breakdown-rows')):
                    page.locator('.nav', has_text=label).click()
                    expect(page.locator('h1')).to_have_text(heading)
                    page.wait_for_selector(rows)
                    assert page.locator('.page-header').bounding_box()['height'] <= 80
                    page.evaluate("window.scrollTo({top: 600, behavior: 'instant'})")
                    assert page.evaluate('window.scrollY') > 0
                    assert abs(page.locator('.page-header').bounding_box()['y']) < 2
                    expect(page.get_by_role('button', name='Filters', exact=True)).to_be_in_viewport()
                page.locator('.nav', has_text='Overview').click()
                page.set_viewport_size({'width': 1440, 'height': 1100})
                expect(page.locator('#input')).to_have_text('300,000')
                open_filters()
                page.locator('[name=project]').select_option('Website')
                expect(page.locator('#input')).to_have_text('150,000')
                # Switching projects must keep every project available without clearing first.
                expect(page.locator('[name=project] option')).to_have_count(4)
                page.locator('[name=project]').select_option('Research')
                expect(page.locator('#input')).to_have_text('90,000')
                page.locator('[name=project]').select_option('CLI tools')
                expect(page.locator('#input')).to_have_text('60,000')
                page.locator('[name=project]').select_option('')
                expect(page.locator('#input')).to_have_text('300,000')
                page.locator('[name=project]').select_option('Website')
                expect(page.locator('#input')).to_have_text('150,000')
                page.screenshot(path='/tmp/muse-cost-filters-desktop.png', full_page=True)
                close_filters()
                assert page.url == base + '/'
                page.locator('.nav', has_text='Breakdown').click()
                expect(page.locator('h1')).to_have_text('Usage breakdown')
                check_filter('project', 'Website')
                expect(page.locator('#breakdown-rows tr')).to_have_count(1)
                assert len(documents) == 1
                page.get_by_role('button', name='Token share').hover()
                expect(page.get_by_role('tooltip')).to_contain_text('not a share of cost')
                page.keyboard.press('Escape')
                page.locator('[data-group=session]').click()
                expect(page.locator('#breakdown-rows tr')).to_have_count(3)
                page.reload()
                expect(page.locator('[data-group=session]')).to_have_attribute('aria-pressed', 'true')
                expect(page.get_by_role('dialog')).to_have_count(0)
                check_filter('project', 'Website')
                # Breakdown session rows open the session page instead of silently filtering.
                page.locator('#breakdown-rows a', has_text='session-3').click()
                expect(page).to_have_url(base + '/sessions/session-3')
                expect(page.locator('h1')).to_have_text('Session')
                expect(page.locator('.chip-note')).to_be_visible()
                expect(page.locator('#event-rows tr')).to_have_count(5)
                expect(page.locator('#subagent-tree')).to_contain_text('sub-3a')
                with (logs/'session-3/session.jsonl').open('a') as f:
                    f.write(json.dumps(event('new-live', 'session-3', inp=1234, cached=100, model='muse-spark-1.3'))+'\n')
                expect(page.locator('#event-rows tr')).to_have_count(6, timeout=10000)
                expect(page.locator('#event-rows')).to_contain_text('new-live')
                page.reload()
                expect(page.locator('h1')).to_have_text('Session')
                expect(page.locator('#event-rows tr')).to_have_count(6)
                with page.expect_download() as download:
                    page.locator('#export').click()
                export = json.loads(Path(download.value.path()).read_text())
                assert len(export) == 6 and {row['session'] for row in export} == {'session-3', 'sub-3a'}
                page.locator('#subagent-tree a', has_text='sub-3a').click()
                expect(page).to_have_url(base + '/sessions/sub-3a')
                expect(page.locator('#event-rows tr')).to_have_count(2)
                page.locator('#parent-link').click()
                expect(page).to_have_url(base + '/sessions/session-3')
                page.go_back()
                expect(page).to_have_url(base + '/sessions/sub-3a')
                page.go_forward()
                expect(page).to_have_url(base + '/sessions/session-3')
                # Sessions list: one row per root session; filter chips explain and undo narrowing.
                page.locator('.nav', has_text='Sessions').click()
                expect(page.locator('#session-rows tr')).to_have_count(2)
                expect(page.locator('#session-rows')).not_to_contain_text('sub-3a')
                page.get_by_role('button', name='Remove project filter').click()
                expect(page.locator('#session-rows tr')).to_have_count(4)
                page.locator('#session-rows tr', has_text='Research').locator('td').nth(2).click()
                expect(page).to_have_url(base + '/sessions/session-2')
                page.locator('.breadcrumb a', has_text='Sessions').click()
                open_filters()
                page.locator('[name=session]').fill('No such session')
                expect(page.locator('#session-rows')).to_contain_text('No sessions match')
                page.locator('#clear').click()
                expect(page.locator('#session-rows tr')).to_have_count(4)
                close_filters()
                page.locator('.nav', has_text='Projects').click()
                page.locator('#project-rows a', has_text='Website').click()
                expect(page).to_have_url(base + '/projects/Website')
                expect(page.locator('#session-count')).to_have_text('2')
                expect(page.locator('#input')).to_have_text('151,234')
                page.locator('.nav', has_text='Overview').click()
                expect(page.locator('#input')).to_have_text('301,234')
                page.screenshot(path='/tmp/muse-cost-react-overview.png', full_page=True)
                page.set_viewport_size({'width': 390, 'height': 844})
                open_filters()
                expect(page.locator('[name=session]')).to_be_visible()
                page.locator('[name=project]').select_option('Research')
                expect(page.locator('#input')).to_have_text('90,000')
                page.locator('[name=project]').select_option('Website')
                expect(page.locator('#input')).to_have_text('151,234')
                page.screenshot(path='/tmp/muse-cost-filters-mobile.png', full_page=True)
                assert page.evaluate('document.documentElement.scrollWidth <= innerWidth')
                page.keyboard.press('Escape')
                page.locator('.nav', has_text='Breakdown').click()
                page.get_by_role('button', name='Token share').click()
                expect(page.get_by_role('tooltip')).to_be_visible()
                box = page.get_by_role('tooltip').bounding_box()
                assert box['x'] >= 0 and box['x'] + box['width'] <= 390
                page.goto(base + '/breakdown?period=all&project=Website&model=&from=&to=')
                expect(page).to_have_url(base + '/breakdown')
                check_filter('project', 'Website')
                expect(page.locator('#breakdown-rows tr')).to_have_count(3)
                page.goto(base + '/activity')
                expect(page).to_have_url(base + '/sessions')
                page.goto(base + '/sessions/no-such-session')
                expect(page.get_by_role('heading', name='Session not found')).to_be_visible()
                assert not errors, errors
                browser.close()
                print('React browser checks passed: clean URLs, client navigation, state persistence, filters, filter chips, session and project drill-down, subagent tree, live updates, export, tooltips, history, mobile, legacy URLs.')
        finally:
            server.shutdown()
            server.server_close()
            thread.join()


if __name__ == '__main__':
    main()

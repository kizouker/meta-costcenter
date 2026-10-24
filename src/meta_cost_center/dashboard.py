import curses
import time
import sqlite3

from .collector import scan
from .reports import lines


def safe(value):
    return ''.join(c if c.isprintable() else ' ' for c in str(value))


def run(db, roots, config, filters, rates, interval):
    def screen(win):
        curses.curs_set(0)
        win.timeout(200)
        state = dict(filters)
        group, scroll, deadline = 'model', 0, 0
        last, error = None, None
        content = []
        message = ''

        def prompt(label, current=''):
            win.timeout(-1)
            curses.curs_set(1)
            value = current
            try:
                while True:
                    height, width = win.getmaxyx()
                    win.move(height-1, 0)
                    win.clrtoeol()
                    win.addnstr(height-1, 0, safe(f'{label}: {value}'), max(0,width-1))
                    win.refresh()
                    key = win.get_wch()
                    if key in ('\n', '\r'):
                        return value.strip()
                    if key == '\x1b':
                        return current
                    if key in (curses.KEY_BACKSPACE, '\b', '\x7f'):
                        value = value[:-1]
                    elif isinstance(key, str) and key.isprintable():
                        value += key
            finally:
                curses.curs_set(0)
                win.timeout(200)

        while True:
            if time.monotonic() >= deadline:
                try:
                    win.erase()
                    win.addnstr(0, 0, 'Scanning local usage logs...', max(0, win.getmaxyx()[1]-1))
                    win.refresh()
                    result = scan(db, roots, config)
                    fresh = lines(db, state, rates, group, result)
                    content, last = fresh, result
                    error = 'collection unavailable or incomplete; see warnings' if result['warnings'] else None
                except (OSError, ValueError, sqlite3.Error):
                    error = type(exc).__name__ + ': scan failed; showing last successful view'
                deadline = time.monotonic() + interval
            height, width = win.getmaxyx()
            win.erase()
            header = 'q quit | r refresh | f filters | c clear | g grouping | arrows scroll'
            status = ('STALE: '+error) if error else f'Live local logs | refresh {interval:g}s | {message}'
            try:
                win.addnstr(0,0,header,max(0,width-1),curses.A_REVERSE)
                win.addnstr(1,0,status,max(0,width-1))
                scroll = max(0,min(scroll,max(0,len(content)-max(1,height-3))))
                for i,line in enumerate(content[scroll:scroll+max(0,height-3)],2):
                    win.addnstr(i,0,safe(line),max(0,width-1))
                win.refresh()
            except curses.error:
                pass
            key = win.getch()
            if key in (ord('q'), 27):
                return
            if key == ord('r'):
                deadline = 0
            elif key == ord('c'):
                state = {'period':'today'}
                scroll, deadline = 0, 0
            elif key == ord('g'):
                options = ['model','project','session','billing_mode']
                group = options[(options.index(group)+1)%len(options)]
                deadline = 0
            elif key == ord('f'):
                candidate = dict(state)
                for name, label in [('period','Period (today/week/month/all)'), ('from','From YYYY-MM-DD (blank clears)'),
                                    ('to','To YYYY-MM-DD (blank clears)'), ('project','Project (exact)'),
                                    ('model','Model (exact)'), ('session','Session (includes descendants)'),
                                    ('billing_mode','Billing mode (payg/subscription/unknown)')]:
                    candidate[name] = prompt(label, candidate.get(name) or '')
                candidate['period'] = candidate.get('period') or 'today'
                try:
                    from .reports import bounds
                    bounds(candidate)
                    if candidate.get('billing_mode') not in ('', 'payg','subscription','unknown'):
                        raise ValueError('Invalid billing mode')
                    state, message, scroll = candidate, '', 0
                except (ValueError,KeyError):
                    message = 'Invalid filters; previous filters retained'
                deadline = 0
            elif key in (curses.KEY_DOWN, ord('j')):
                scroll += 1
            elif key in (curses.KEY_UP, ord('k')):
                scroll = max(0,scroll-1)
            elif key == curses.KEY_NPAGE:
                scroll += max(1,height-3)
            elif key == curses.KEY_PPAGE:
                scroll = max(0,scroll-height+3)
    curses.wrapper(screen)

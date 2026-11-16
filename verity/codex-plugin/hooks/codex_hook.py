#!/usr/bin/env python3
"""Persist minimal Codex turn observations; never infer current state."""
import json
import os
from pathlib import Path
import sqlite3
import sys
import time

EVENTS = {'UserPromptSubmit', 'PreToolUse', 'PostToolUse', 'PermissionRequest', 'Stop', 'Interrupt'}


def database():
    return Path.home() / '.verity' / 'codex-observations.sqlite3'


def record(payload):
    kind, session, turn, cwd = (payload.get(k) for k in ('hook_event_name', 'session_id', 'turn_id', 'cwd'))
    if kind not in EVENTS or any(not isinstance(v, str) or not v or len(v) > 4096 for v in (session, turn, cwd)) or not os.path.isabs(cwd):
        return
    db = database()
    db.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    # A separate store avoids modifying any project database or its schema.
    if db.parent.is_symlink() or db.is_symlink():
        return
    try:
        fd = os.open(str(db), os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    except FileExistsError:
        pass
    else:
        os.close(fd)
    con = sqlite3.connect(str(db), timeout=2)
    try:
        con.execute('CREATE TABLE IF NOT EXISTS observations (session_id TEXT NOT NULL, turn_id TEXT NOT NULL, cwd TEXT NOT NULL, event TEXT NOT NULL, observed_at REAL NOT NULL, PRIMARY KEY(session_id, turn_id))')
        con.execute('INSERT INTO observations VALUES (?,?,?,?,?) ON CONFLICT(session_id,turn_id) DO UPDATE SET cwd=excluded.cwd,event=excluded.event,observed_at=excluded.observed_at WHERE excluded.observed_at >= observations.observed_at', (session, turn, cwd, kind, time.time()))
        con.commit()
    finally:
        con.close()


def snapshot():
    db = database()
    if not db.is_file():
        return []
    con = sqlite3.connect(db.as_uri() + '?mode=ro', uri=True, timeout=2)
    try:
        rows = con.execute('SELECT session_id,turn_id,cwd,event,observed_at FROM observations ORDER BY observed_at DESC LIMIT 10001').fetchall()
        if len(rows) > 10000:
            raise ValueError('observation limit exceeded')
        return [dict(zip(('session_id','turn_id','cwd','event','observed_at'), row)) for row in rows]
    finally:
        con.close()


def main():
    if len(sys.argv) == 2 and sys.argv[1] == 'snapshot':
        print(json.dumps(snapshot()))
        return
    try:
        data = sys.stdin.buffer.read(1024 * 1024 + 1)
        if len(data) <= 1024 * 1024:
            record(json.loads(data))
    except (ValueError, TypeError, OSError, sqlite3.Error):
        pass  # Monitoring must never block the turn.
    print('{}')

if __name__ == '__main__':
    main()

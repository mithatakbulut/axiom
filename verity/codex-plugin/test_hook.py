import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import unittest
from concurrent.futures import ThreadPoolExecutor
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('hook', Path(__file__).parent / 'hooks' / 'codex_hook.py')
hook = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hook)

class HookTests(unittest.TestCase):
    def test_separate_turns_concurrent_writes_and_private_content(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(hook, 'database', return_value=Path(tmp)/'data'/'observations.db'):
            def send(i):
                hook.record({'cwd':tmp, 'session_id':'session', 'turn_id':f'turn-{i}', 'hook_event_name':'UserPromptSubmit', 'prompt':'secret'})
            with ThreadPoolExecutor(max_workers=4) as pool:
                list(pool.map(send, range(20)))
            hook.record({'cwd':tmp, 'session_id':'session', 'turn_id':'turn-1', 'hook_event_name':'Stop', 'last_assistant_message':'secret'})
            rows = hook.snapshot()
            self.assertEqual(len(rows), 20)
            self.assertEqual(next(r for r in rows if r['turn_id']=='turn-1')['event'], 'Stop')
            self.assertNotIn('secret', json.dumps(rows))
            self.assertEqual(os.stat(hook.database()).st_mode & 0o777, 0o600)

    def test_invalid_input_and_read_do_not_create_store(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(hook, 'database', return_value=Path(tmp)/'data'/'observations.db'):
            self.assertEqual(hook.snapshot(), [])
            hook.record({'cwd':'relative', 'session_id':'s', 'turn_id':'t', 'hook_event_name':'Stop'})
            self.assertFalse(hook.database().exists())

if __name__ == '__main__': unittest.main()

import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("manage_opend", ROOT / "manage.py")
manager = importlib.util.module_from_spec(spec)
spec.loader.exec_module(manager)


class ManagedOpenDTest(unittest.TestCase):
    def test_login_change_restarts_and_disable_stops_process(self):
        with tempfile.TemporaryDirectory() as directory:
            folder = Path(directory)
            login = folder / "login.json"
            events = folder / "events.jsonl"
            binary = folder / "FutuOpenD"
            binary.write_text("""#!/usr/bin/env python3
import json, os, signal, sys, time
import xml.etree.ElementTree as ET
config = ET.parse(next(arg.split('=', 1)[1] for arg in sys.argv if arg.startswith('-cfg_file=')))
assert '-no_monitor=1' in sys.argv
def emit(kind):
    with open(os.environ['TEST_OPEND_EVENTS'], 'a') as out:
        out.write(json.dumps({'kind': kind, 'account': config.findtext('login_account'), 'digest': config.findtext('login_pwd_md5')}) + '\\n')
def stop(*args):
    emit('stop')
    sys.exit(0)
signal.signal(signal.SIGTERM, stop)
emit('start')
while True: time.sleep(0.1)
""")
            binary.chmod(0o700)
            env = dict(os.environ, FUTU_LOGIN_CONFIG=str(login), FUTU_OPEND_DIR=str(folder),
                       FUTU_CONFIG_TEMPLATE=str(ROOT / "FutuOpenD.xml.template"), TEST_OPEND_EVENTS=str(events))
            process = subprocess.Popen([sys.executable, str(ROOT / "manage.py")], env=env, stdout=subprocess.DEVNULL)

            def publish(enabled, account):
                temporary = folder / "next.json"
                temporary.write_text(json.dumps({"enabled": enabled, "account": account, "passwordMD5": "a" * 32}))
                temporary.replace(login)

            def wait_events(count):
                deadline = time.monotonic() + 8
                while time.monotonic() < deadline:
                    rows = [json.loads(line) for line in events.read_text().splitlines()] if events.exists() else []
                    if len(rows) >= count:
                        return rows
                    if process.poll() is not None:
                        self.fail("manager exited unexpectedly")
                    time.sleep(0.05)
                self.fail("timed out waiting for OpenD lifecycle")

            try:
                publish(True, "first&<fixture>@example.invalid")
                self.assertEqual(wait_events(1)[0]["account"], "first&<fixture>@example.invalid")
                publish(True, "second@example.invalid")
                rows = wait_events(3)
                self.assertEqual([row["kind"] for row in rows], ["start", "stop", "start"])
                self.assertEqual(rows[2]["account"], "second@example.invalid")
                publish(False, "second@example.invalid")
                self.assertEqual(wait_events(4)[3]["kind"], "stop")
            finally:
                process.terminate()
                process.wait(timeout=15)
            self.assertEqual(process.returncode, 0)

    def test_invalid_login_does_not_start_opend(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "login.json"
            for value in ["invalid", "[]", '{"enabled":true}', '{"enabled":true,"account":"test","passwordMD5":"invalid"}']:
                path.write_text(value)
                self.assertIsNone(manager.read_login(path))


if __name__ == "__main__":
    unittest.main()

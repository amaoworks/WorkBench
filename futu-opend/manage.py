#!/usr/bin/env python3
"""Run OpenD from the workbench's atomic, private login configuration."""
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import tempfile
import time
import xml.etree.ElementTree as ET


def render_config(login):
    tree = ET.parse(os.environ.get("FUTU_CONFIG_TEMPLATE", "/opt/template/FutuOpenD.xml.template"))
    values = {
        "ip": os.environ.get("FUTU_OPEND_IP", "0.0.0.0"),
        "api_port": os.environ.get("FUTU_OPEND_PORT", "11111"),
        "telnet_ip": os.environ.get("FUTU_OPEND_IP", "0.0.0.0"),
        "telnet_port": os.environ.get("FUTU_OPEND_TELNET_PORT", "22222"),
        "login_account": login["account"],
        "login_pwd_md5": login["passwordMD5"],
        "lang": os.environ.get("FUTU_LANG", "chs"),
        "log_level": os.environ.get("FUTU_LOG_LEVEL", "info"),
    }
    for key, value in values.items():
        tree.find(key).text = value
    return ET.tostring(tree.getroot(), encoding="utf-8", xml_declaration=True)


def read_login(path):
    try:
        login = json.loads(path.read_bytes())
        if not isinstance(login, dict) or login.get("enabled") is not True:
            return None
        if not isinstance(login.get("account"), str) or not login["account"].strip():
            return None
        if not isinstance(login.get("passwordMD5"), str) or not re.fullmatch(r"[0-9a-fA-F]{32}", login["passwordMD5"]):
            return None
        return render_config(login)
    except (OSError, ValueError, ET.ParseError):
        return None


def main():
    stopping = False

    def stop(_signum, _frame):
        nonlocal stopping
        stopping = True

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    login_path = Path(os.environ["FUTU_LOGIN_CONFIG"])
    binary = Path(os.environ.get("FUTU_OPEND_DIR", "/opt/FutuOpenD")) / "FutuOpenD"
    child = None

    def stop_child():
        nonlocal child
        if child is not None:
            child.terminate()
            try:
                child.wait(timeout=10)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait()
            child = None

    print("Waiting for Futu login settings from workbench", flush=True)
    with tempfile.TemporaryDirectory(prefix="workbench-opend-") as directory:
        config_path = Path(directory) / "OpenD.xml"
        active = None
        retry_at = 0
        try:
            while not stopping:
                desired = read_login(login_path)
                if desired != active:
                    stop_child()
                    active = desired
                    retry_at = 0
                    if desired is None:
                        config_path.unlink(missing_ok=True)
                        print("Futu disabled or login settings incomplete", flush=True)
                    else:
                        config_path.write_bytes(desired)
                        config_path.chmod(0o600)
                if child is not None and child.poll() is not None:
                    child = None
                    retry_at = time.monotonic() + 5
                if active is not None and child is None and time.monotonic() >= retry_at:
                    # Only the private config path is passed on the command line.
                    child = subprocess.Popen([str(binary), "-no_monitor=1", "-cfg_file=" + str(config_path)], cwd=binary.parent)
                    print("OpenD started with saved login settings", flush=True)
                time.sleep(1)
        finally:
            stop_child()


if __name__ == "__main__":
    main()

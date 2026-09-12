#!/usr/bin/env python3
"""Exercise a release binary or Compose image using disposable local data only."""
import argparse
import http.client
from http.cookies import SimpleCookie
import json
import os
from pathlib import Path
import secrets
import socket
import sqlite3
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--binary", type=Path)
    source.add_argument("--image", help="local Docker image:tag to test through compose.yaml")
    args = parser.parse_args()
    repo = Path(__file__).resolve().parent.parent
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    password = "Deployment-Test-" + secrets.token_hex(12)
    origin = "https://deployment.workbench.invalid"
    env = {k: v for k, v in os.environ.items() if not k.startswith(("WORKBENCH_", "OPENAI_"))}
    env.update(WORKBENCH_LISTEN=f"127.0.0.1:{port}", WORKBENCH_AUTH="password",
               WORKBENCH_PUBLIC_URL=origin, WORKBENCH_PASSWORD=password)
    cookies = {}
    token = ""
    process = None
    with tempfile.TemporaryDirectory(prefix="workbench-deployment-") as temp:
        work = Path(temp)
        env["WORKBENCH_DATA"] = str(work / "data.db")
        compose = ["docker", "compose", "--project-name", work.name,
                   "--file", str(repo / "compose.yaml")]
        if args.image:
            image, tag = args.image.rsplit(":", 1)
            env.update(WORKBENCH_IMAGE=image, WORKBENCH_VERSION=tag, WORKBENCH_PORT=str(port))
        log = (work / "server.log").open("w+")

        def command(argv, **kwargs):
            return subprocess.run(argv, env=env, check=True, text=True, **kwargs)

        def request(method, path, data=None, expected=200):
            connection = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
            headers = {"Host": "deployment.workbench.invalid", "Origin": origin,
                       "Content-Type": "application/json", "X-CSRF-Token": token,
                       "Cookie": "; ".join(f"{k}={v}" for k, v in cookies.items())}
            try:
                connection.request(method, path, None if data is None else json.dumps(data), headers)
                response = connection.getresponse()
                for key, value in response.getheaders():
                    if key.lower() == "set-cookie":
                        parsed = SimpleCookie()
                        parsed.load(value)
                        for name, cookie in parsed.items():
                            assert cookie["secure"] and cookie["httponly"], "missing secure cookie attributes"
                            cookies[name] = cookie.value
                payload = response.read()
                assert response.status == expected, f"{method} {path}: {response.status}, expected {expected}"
                return json.loads(payload) if payload else None
            finally:
                connection.close()

        def start():
            nonlocal process
            if args.image:
                command(compose + ["up", "-d", "--force-recreate", "--wait", "--wait-timeout", "90"])
            else:
                process = subprocess.Popen([str(args.binary.resolve())], env=env, stdout=log, stderr=log)
            deadline = time.monotonic() + 30
            while True:
                try:
                    request("GET", "/health/ready")
                    break
                except (OSError, AssertionError):
                    if (process is not None and process.poll() is not None) or time.monotonic() > deadline:
                        raise RuntimeError("service did not become ready")
                    time.sleep(0.2)
            if args.image:
                command(compose + ["exec", "-T", "workbench", "/workbench", "-healthcheck"])
            else:
                command([str(args.binary.resolve()), "-healthcheck"])

        def stop():
            nonlocal process
            if args.image:
                command(compose + ["stop", "--timeout", "20"])
            elif process is not None:
                process.terminate()
                assert process.wait(timeout=20) == 0, "SIGTERM must exit cleanly"
                process = None

        def login():
            nonlocal token
            cookies.clear()
            token = request("GET", "/api/auth/csrf")["token"]
            request("POST", "/api/auth/login", {"password": password})

        try:
            start()
            request("GET", "/api/modules/todo/tasks", expected=401)
            token = request("GET", "/api/auth/csrf")["token"]
            request("PUT", "/api/settings/logging", {"level": "error"}, expected=401)
            login()
            assert request("GET", "/api/settings")["logging"]["level"] == "info"
            request("PUT", "/api/settings/logging", {"level": "warn"})
            request("PUT", "/api/settings/logging", {"level": "invalid"}, expected=400)
            title = "Deployment persistence " + secrets.token_hex(4)
            request("POST", "/api/modules/todo/tasks", {"title": title}, expected=201)
            backup = request("POST", "/api/system/backup", {}, expected=201)["file"]
            if args.image:
                container = command(compose + ["ps", "-q", "workbench"], capture_output=True).stdout.strip()
                info = json.loads(command(["docker", "inspect", container], capture_output=True).stdout)[0]
                assert info["Config"]["User"] == "65532:65532", "container must run as non-root"
                assert info["HostConfig"]["ReadonlyRootfs"], "root filesystem must be read-only"
                command(["docker", "cp", f"{container}:/data/backups/{backup}", str(work / "backup.db")])
                backup_path = work / "backup.db"
            else:
                backup_path = work / "backups" / backup
            with sqlite3.connect(backup_path) as db:
                assert db.execute("PRAGMA integrity_check").fetchone()[0] == "ok"
                assert db.execute("SELECT count(*) FROM todo_tasks WHERE title = ?", (title,)).fetchone()[0] == 1
            stop()
            env["WORKBENCH_PASSWORD"] = ""  # Existing credentials must survive recreation.
            env["WORKBENCH_LOG_LEVEL"] = "debug"  # Saved settings must take precedence.
            start()
            login()
            assert request("GET", "/api/settings")["logging"]["level"] == "warn", "log level lost after restart"
            assert title in json.dumps(request("GET", "/api/modules/todo/tasks")), "data lost after restart"
            stop()
            print("PASS: initialization, login, healthcheck, backup, graceful stop and persisted data/credentials/log level")
        except BaseException:
            if args.image:
                subprocess.run(compose + ["logs", "--no-color", "--tail", "80"], env=env, check=False)
            else:
                log.flush()
                log.seek(0)
                print(log.read())
            raise
        finally:
            if process is not None and process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=20)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
            if args.image:
                subprocess.run(compose + ["down", "--volumes", "--remove-orphans", "--timeout", "20"], env=env, check=False)
            log.close()


if __name__ == "__main__":
    main()

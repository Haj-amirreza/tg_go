#!/usr/bin/env python3
"""Private, fail-closed operator commands for the separate Ubuntu web-only stack."""
import argparse
import getpass
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import stat
import subprocess
import sys
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
KEYS = {"APP_DOMAIN", "APP_ORIGIN", "APP_IMAGE", "POSTGRES_PASSWORD", "APP_DB_PASSWORD", "APP_KEY"}


def fail(message):
    raise SystemExit(message)


def domain(value):
    value = value.lower()
    labels = value.split(".")
    if (len(value) > 253 or len(labels) < 2 or value.endswith(".localhost")
            or not re.fullmatch(r"[a-z]{2,63}", labels[-1])
            or any(not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label) for label in labels)):
        fail("Use a public DNS hostname only, without scheme, port or path.")
    return value


def private_file(path):
    try:
        info = path.lstat()
    except FileNotFoundError:
        fail("Private config is missing; use init first.")
    if (not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077
            or info.st_uid != os.geteuid() or info.st_nlink != 1):
        fail("Config must be a private regular file owned by the current user (0600, no links).")


def load(path):
    private_file(path)
    values = {}
    for line in path.read_text().splitlines():
        if not line or line.startswith("#"):
            continue
        key, separator, value = line.partition("=")
        if not separator or key not in KEYS or key in values:
            fail("Config contains an unsupported or duplicate entry; no values were printed.")
        values[key] = value
    if set(values) != KEYS:
        fail("Private config is incomplete.")
    if values["APP_DOMAIN"] != domain(values["APP_DOMAIN"]):
        fail("APP_DOMAIN must be lowercase.")
    if values["APP_ORIGIN"] != "https://" + values["APP_DOMAIN"]:
        fail("APP_ORIGIN must be the HTTPS origin for APP_DOMAIN.")
    for key, size in (("POSTGRES_PASSWORD", 48), ("APP_DB_PASSWORD", 48), ("APP_KEY", 64)):
        if not re.fullmatch(r"[0-9a-f]{" + str(size) + r"}", values[key]):
            fail("Use the randomly generated hexadecimal credential format.")
    if len({values["POSTGRES_PASSWORD"], values["APP_DB_PASSWORD"], values["APP_KEY"]}) != 3:
        fail("Database and application credentials must be distinct.")
    if not re.fullmatch(r"unibot-next:web-[a-z0-9][a-z0-9_.-]{0,79}", values["APP_IMAGE"]):
        fail("APP_IMAGE must be a versioned local unibot-next:web-* image.")
    return values


def init(path, hostname, image):
    hostname = domain(hostname)
    if not re.fullmatch(r"unibot-next:web-[a-z0-9][a-z0-9_.-]{0,79}", image):
        fail("Use a unique image name such as unibot-next:web-<release>.")
    if not path.parent.is_dir():
        fail("Create the private config directory first (install -d -m 0700).")
    values = dict(APP_DOMAIN=hostname, APP_ORIGIN="https://" + hostname, APP_IMAGE=image,
                  POSTGRES_PASSWORD=secrets.token_hex(24), APP_DB_PASSWORD=secrets.token_hex(24), APP_KEY=secrets.token_hex(32))
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    except FileExistsError:
        fail("Existing config was preserved. Never regenerate credentials for an existing database volume.")
    with os.fdopen(fd, "w") as stream:
        stream.write("".join(key + "=" + value + "\n" for key, value in values.items()))
    print("Created private web-only config; credentials were not printed.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", type=Path, default=Path("/etc/unibot-web/web.env"))
    parser.add_argument("--prebuilt", action="store_true", help="Use existing native Linux artifacts for build (Go 1.27.4, Node 24.22.1)")
    actions = parser.add_subparsers(dest="action", required=True)
    create = actions.add_parser("init", help="Create NEW 0600 config; never replace an existing file")
    create.add_argument("--domain", required=True)
    create.add_argument("--image", required=True, help="Unique local tag, e.g. unibot-next:web-<release>")
    validate = actions.add_parser("validate", help="Validate private settings and Compose without printing secrets")
    validate.add_argument("--offline", action="store_true", help="Validate only config, without Docker")
    backup = actions.add_parser("backup", help="Briefly stop web writers and create a private, consistent backup")
    backup.add_argument("--directory", type=Path, required=True, help="NEW absolute directory outside the checkout")
    for action, description in (("build", "Build the application image; does not start services"),
                                ("up", "Initialize volumes, migrate, then start the web-only stack"),
                                ("bootstrap", "Interactively create the FIRST administrator in an empty registry"),
                                ("check", "Check local container readiness and public HTTPS"),
                                ("stop", "Stop services while preserving all volumes")):
        actions.add_parser(action, help=description)
    args = parser.parse_args()
    path = args.env_file
    if not path.is_absolute():
        fail("Use an absolute --env-file path outside the source checkout.")
    if path.resolve().is_relative_to(ROOT):
        fail("Keep production config outside the source checkout.")
    if args.action == "init":
        init(path, args.domain, args.image)
        return
    values = load(path)
    if args.action == "validate" and args.offline:
        print("Private web-only config is valid; Docker was not invoked.")
        return
    if (path.parent / "bot.env").exists() and args.action in {"up", "backup", "stop"}:
        fail("Telegram credentials exist. Use enable-telegram.py disable and the staged production operations before web-only maintenance.")
    # Parent shell values must never override the validated private env or select
    # another Compose file/project. Do not source the credentials into a shell.
    # Keep registry credentials/configuration while discarding daemon selectors.
    env = {key: value for key, value in os.environ.items()
           if key not in KEYS and (key == "DOCKER_CONFIG" or not key.startswith(
               ("COMPOSE_", "DOCKER_", "BUILDX_", "BUILDKIT_", "BAKE_")))}
    compose = ["docker", "--host", "unix:///var/run/docker.sock", "compose", "--project-name", "unibot-web", "--env-file", str(path),
               "-f", str(ROOT / "compose.production.yaml")]
    if args.prebuilt:
        compose += ["-f", str(ROOT / "compose.production.prebuilt.yaml")]

    def run(*arguments, **options):
        return subprocess.run(compose + list(arguments), cwd=ROOT, env=env, check=True, **options)

    run("config", "--quiet")
    if args.action == "validate":
        print("Private config and production Compose are valid; credentials were not printed.")
    elif args.action == "build":
        if args.prebuilt:
            for artifact in (ROOT / "backend/bin/unibot", ROOT / "frontend/admin/dist/index.html"):
                if not artifact.is_file():
                    fail("Build native Linux artifacts with tools/dev/build-next.sh first.")
        run("build", "--pull", "api")
    elif args.action == "up":
        # Keep older application processes away from a potentially newer schema.
        # A migration failure intentionally leaves the web writers stopped.
        run("stop", "caddy", "api", "scheduler")
        run("run", "--rm", "--no-deps", "volume-init")
        run("up", "-d", "--no-build", "--wait", "--wait-timeout", "120", "db")
        run("run", "--rm", "--no-deps", "migrate")
        run("up", "-d", "--no-build", "--wait", "--wait-timeout", "180", "api", "scheduler", "caddy")
        print("Web-only stack started. Run bootstrap once, then check public HTTPS.")
    elif args.action == "bootstrap":
        if not sys.stdin.isatty():
            fail("Bootstrap requires an interactive terminal; no passwords in arguments or files.")
        username = input("Admin username: ").strip()
        name = input("Display name: ").strip()
        password = getpass.getpass("New password (12-72 UTF-8 bytes): ")
        if password != getpass.getpass("Repeat password: "):
            fail("Passwords differ.")
        if not 12 <= len(password.encode()) <= 72:
            fail("Password must contain 12-72 UTF-8 bytes.")
        # Web release has no Telegram identity activation or automatic grants.
        body = json.dumps(dict(username=username, name=name, password=password, telegram_id=""))
        run("run", "--rm", "-T", "--no-deps", "migrate", "-mode", "admin-bootstrap", input=body, text=True)
        print("First web administrator created; Telegram remains disabled.")
    elif args.action == "backup":
        destination = args.directory
        if not destination.is_absolute() or destination.resolve().is_relative_to(ROOT):
            fail("Use a NEW absolute backup directory outside the checkout.")
        if not destination.parent.is_dir():
            fail("Create a private backup parent directory first (install -d -m 0700).")
        services = run("ps", "--services", "--status", "running", capture_output=True, text=True).stdout.splitlines()
        if "db" not in services:
            fail("Database must be running before backup.")
        try:
            destination.mkdir(mode=0o700)
        except FileExistsError:
            fail("Backup directory already exists; it was preserved.")
        marker = destination / "INCOMPLETE"
        marker.touch(mode=0o600)
        writers = [name for name in ("caddy", "api", "scheduler") if name in services]
        try:
            if writers:
                run("stop", *writers)
            with (destination / "database.dump").open("xb") as stream:
                os.chmod(stream.fileno(), 0o600)
                run("exec", "-T", "db", "pg_dump", "-U", "postgres", "-d", "unibot_web", "--format=custom", stdout=stream)
            with (destination / "private-files.tar.gz").open("xb") as stream:
                os.chmod(stream.fileno(), 0o600)
                run("run", "--rm", "-T", "--no-deps", "backup-files", stdout=stream)
            with (destination / "web.env").open("x") as stream:
                os.chmod(stream.fileno(), 0o600)
                stream.write(path.read_text())
            revision = subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, check=True, capture_output=True, text=True).stdout.strip()
            with (destination / "release.txt").open("x") as stream:
                stream.write("revision=" + revision + "\nimage=" + values["APP_IMAGE"] + "\n")
            checksums = []
            for item in sorted(destination.iterdir()):
                if item != marker:
                    checksum = hashlib.sha256()
                    with item.open("rb") as stream:
                        for block in iter(lambda: stream.read(1024 * 1024), b""):
                            checksum.update(block)
                    checksums.append(checksum.hexdigest() + "  " + item.name + "\n")
            (destination / "SHA256SUMS").write_text("".join(checksums))
            marker.unlink()
        finally:
            if writers:
                run("start", *writers)
        print("Private backup completed and previous web services restarted. Copy it off this server.")
    elif args.action == "check":
        run("exec", "-T", "caddy", "wget", "-q", "-O", "/dev/null", "http://api:8080/readyz")
        # Check certificate and actual TLS endpoint; never disable verification.
        for endpoint in ("/healthz", "/readyz", "/api/v1/system/status"):
            target = values["APP_ORIGIN"] + endpoint
            with urllib.request.urlopen(target, timeout=15) as response:
                if response.geturl() != target:
                    fail("Public health endpoint unexpectedly redirected.")
                result = json.load(response)
            if endpoint in ("/healthz", "/readyz") and result.get("status") != ("alive" if endpoint == "/healthz" else "ready"):
                fail("Public health endpoint returned an unexpected status.")
            if endpoint.endswith("/status") and (result.get("environment") != "production"
                    or result.get("botConnected") is not False or result.get("databaseReady") is not True):
                fail("Public endpoint does not report a ready production web-only service.")
        print("Local readiness and public HTTPS passed; Telegram is disabled.")
    elif args.action == "stop":
        run("stop")
        print("Services stopped; data volumes and private config were preserved.")


if __name__ == "__main__":
    try:
        main()
    except subprocess.CalledProcessError:
        fail("Docker operation failed; existing credentials and volumes were preserved. Check service logs.")
    except (OSError, ValueError) as error:
        # Avoid printing exception contents that could include a resolved URL.
        fail("Operation failed (" + type(error).__name__ + "); review permissions, services and DNS/TLS.")

"""Shared private, local-only production operations; never source an env file."""
import contextlib
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import tempfile
import subprocess
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
BOT_KEYS = {"TELEGRAM_BOT_TOKEN", "TELEGRAM_WEBHOOK_SECRET", "TELEGRAM_BOT_USERNAME"}
WEB_KEYS = {"APP_DOMAIN", "APP_ORIGIN", "APP_IMAGE", "POSTGRES_PASSWORD", "APP_DB_PASSWORD", "APP_KEY"}
WRITERS = ("worker", "sender", "caddy", "api", "scheduler")


class OpsError(Exception):
    """Only fixed, sanitized operator messages belong in this exception."""


def validate_path(path, new=False):
    path = Path(path)
    if not path.is_absolute() or ".." in path.parts or path.resolve().is_relative_to(ROOT):
        raise OpsError("Use an absolute private path outside the source checkout, without '..'.")
    for directory in reversed(path.parents):
        info = directory.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid not in (0, os.geteuid()):
            raise OpsError("Private paths must use real directories owned by root or the operator.")
        # Root-owned sticky /tmp is acceptable above a private operator directory.
        if info.st_mode & 0o022 and not (info.st_uid == 0 and info.st_mode & stat.S_ISVTX):
            raise OpsError("Private path ancestors must not be writable by other accounts.")
    parent = path.parent.stat()
    if parent.st_uid != os.geteuid() or parent.st_mode & 0o077:
        raise OpsError("The private parent directory must be owned by the operator and have mode 0700.")
    try:
        info = path.lstat()
    except FileNotFoundError:
        if new:
            return path
        raise OpsError("Private configuration is missing; prepare it first.") from None
    if new:
        raise OpsError("The destination already exists; it was preserved.")
    if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_uid != os.geteuid() or info.st_nlink != 1:
        raise OpsError("Configuration must be an operator-owned 0600 regular file, without links.")
    return path


def load_web(path):
    path = validate_path(path)
    spec = importlib.util.spec_from_file_location("unibot_web_settings", ROOT / "tools/deploy/ubuntu-web.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    try:
        return module.load(path)
    except SystemExit as error:
        raise OpsError(str(error)) from None


def validate_bot(values):
    if set(values) != BOT_KEYS:
        raise OpsError("Bot config must contain exactly token, webhook secret and verified bot username.")
    token = values["TELEGRAM_BOT_TOKEN"]
    match = re.fullmatch(r"([1-9][0-9]{0,18}):[A-Za-z0-9_-]{30,100}", token)
    if not match or int(match[1]) > 9223372036854775807:
        raise OpsError("The bot token has an invalid format; no credential was printed.")
    secret = values["TELEGRAM_WEBHOOK_SECRET"]
    if not re.fullmatch(r"[A-Za-z0-9_-]{32,256}", secret) or len(set(secret)) < 8:
        raise OpsError("The webhook secret must be a strong randomly generated URL-safe value.")
    if not re.fullmatch(r"[A-Za-z][A-Za-z0-9_]{1,28}[Bb][Oo][Tt]", values["TELEGRAM_BOT_USERNAME"]):
        raise OpsError("The verified bot username is invalid.")
    return values


def load_bot(path):
    path = validate_path(path)
    values = {}
    for line in path.read_text().splitlines():
        if not line or line.startswith("#"):
            continue
        key, separator, value = line.partition("=")
        if not separator or key not in BOT_KEYS or key in values:
            raise OpsError("Bot config contains unsupported or duplicate entries; no values were printed.")
        values[key] = value
    return validate_bot(values)


def write_bot(path, values):
    validate_bot(values)
    path = validate_path(path, new=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "w") as stream:
        stream.write("".join(key + "=" + values[key] + "\n" for key in sorted(BOT_KEYS)))
        stream.flush()
        os.fsync(stream.fileno())


def clean_environment():
    return {key: value for key, value in os.environ.items()
            if key not in WEB_KEYS | BOT_KEYS and not key.startswith("TELEGRAM_")
            and (key == "DOCKER_CONFIG" or not key.startswith(("COMPOSE_", "DOCKER_", "BUILDX_", "BUILDKIT_", "BAKE_")))}


@contextlib.contextmanager
def operation_lock(web_env):
    path = Path(web_env).parent / ".production-ops.lock"
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        info = os.fstat(fd)
        if info.st_uid != os.geteuid() or info.st_mode & 0o077 or info.st_nlink != 1:
            raise OpsError("The operation lock must be a private operator-owned regular file.")
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise OpsError("Another production operation is running; retry after it finishes.") from None
        yield
    finally:
        os.close(fd)


class Stack:
    def __init__(self, web_env, bot_env=None, prebuilt=False):
        self.web_env = Path(web_env)
        self.values = load_web(self.web_env)
        self.bot_env = Path(bot_env) if bot_env is not None else None
        if self.bot_env is not None:
            load_bot(self.bot_env)
        self.environment = clean_environment()
        self.command = ["docker", "--host", "unix:///var/run/docker.sock", "compose", "--project-name", "unibot-web",
                        "--env-file", str(self.web_env)]
        if self.bot_env is not None:
            self.command += ["--env-file", str(self.bot_env)]
        self.command += ["-f", str(ROOT / "compose.production.yaml")]
        if prebuilt:
            self.command += ["-f", str(ROOT / "compose.production.prebuilt.yaml")]
        if self.bot_env is not None:
            self.command += ["-f", str(ROOT / "compose.production.bot.yaml")]

    def run(self, *arguments, capture=False, input=None, stdout=None, timeout=300):
        try:
            result = subprocess.run(self.command + list(arguments), cwd=ROOT, env=self.environment, check=True,
                                    input=input, text=input is not None or capture,
                                    stdout=subprocess.PIPE if capture else (stdout if stdout is not None else subprocess.DEVNULL),
                                    stderr=subprocess.PIPE, timeout=timeout)
        except (subprocess.SubprocessError, OSError) as error:
            detail=getattr(error,'stderr',None) or type(error).__name__
            if isinstance(detail,bytes): detail=detail.decode(errors='replace')
            secrets=[self.values[key] for key in ('POSTGRES_PASSWORD','APP_DB_PASSWORD','APP_KEY')]
            if self.bot_env is not None:
                values=load_bot(self.bot_env)
                secrets += [values['TELEGRAM_BOT_TOKEN'],values['TELEGRAM_WEBHOOK_SECRET']]
            for value in secrets:
                detail=detail.replace(value,'[REDACTED]')
            stage=arguments[0] if arguments else 'unknown'
            log=self.web_env.parent/'last-operation-error.log'
            try:
                fd,name=tempfile.mkstemp(prefix='.ops-error-',dir=log.parent)
                with os.fdopen(fd,'w') as stream:
                    stream.write('Docker stage: '+stage+'\n'+detail[-32768:]+'\n')
                os.replace(name,log)
            except OSError:
                raise OpsError('Docker '+stage+' failed; private diagnostics could not be saved. Check config directory permissions.') from None
            raise OpsError('Docker '+stage+' failed. Redacted details: '+str(log)+'. Existing credentials and volumes were preserved.') from None
        return result.stdout if capture else None

    def running(self):
        return self.run("ps", "--services", "--status", "running", capture=True).splitlines()

    def mode(self, name, body=None):
        result = self.run("run", "--rm", "-T", "--no-deps", "migrate", "-mode", name,
                          input=json.dumps(body) if body is not None else None, capture=True)
        if not result.strip():
            return {}
        try:
            value = json.loads(result)
            if not isinstance(value, dict):
                raise ValueError()
            return value
        except (ValueError, TypeError):
            raise OpsError("The image returned an unsupported operator response; use a matching release.") from None

    def backup(self, destination, services):
        """Call only after all previous writers have been stopped; never restart here."""
        destination = validate_path(destination, new=True)
        destination.mkdir(mode=0o700)
        marker = destination / "INCOMPLETE"
        marker.touch(mode=0o600)
        for name, command in (("database.dump", ("exec", "-T", "db", "pg_dump", "-U", "postgres", "-d", "unibot_web", "--format=custom")),
                              ("private-files.tar.gz", ("run", "--rm", "-T", "--no-deps", "backup-files"))):
            fd = os.open(destination / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
            with os.fdopen(fd, "wb") as stream:
                self.run(*command, stdout=stream)
        for source, name in ((self.web_env, "web.env"), (self.bot_env, "bot.env")):
            if source is not None:
                fd = os.open(destination / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
                with os.fdopen(fd, "wb") as stream:
                    stream.write(source.read_bytes())
        fd = os.open(destination / "release.json", os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as stream:
            json.dump({"image": self.values["APP_IMAGE"], "previous_services": sorted(services)}, stream)
        checksums = []
        for item in sorted(destination.iterdir()):
            if item != marker:
                checksum = hashlib.sha256()
                with item.open("rb") as stream:
                    for block in iter(lambda: stream.read(1024 * 1024), b""):
                        checksum.update(block)
                checksums.append(checksum.hexdigest() + "  " + item.name + "\n")
        fd = os.open(destination / "SHA256SUMS", os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as stream:
            stream.writelines(checksums)
        marker.unlink()


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, new_url):
        raise OpsError("Telegram unexpectedly redirected the request; no credential was printed.")


class Telegram:
    def __init__(self, token):
        # Only Telegram receives this value; it never appears in subprocess argv.
        self.token = token

    def call(self, method, payload=None):
        if method not in {"getMe", "getWebhookInfo", "setWebhook", "deleteWebhook"}:
            raise OpsError("Unsupported Telegram operator method.")
        request = urllib.request.Request("https://api.telegram.org/bot" + self.token + "/" + method,
                                         data=json.dumps(payload or {}).encode(),
                                         headers={"Content-Type": "application/json"}, method="POST")
        try:
            with urllib.request.build_opener(NoRedirect()).open(request, timeout=15) as response:
                if response.geturl() != request.full_url:
                    raise OpsError("Telegram unexpectedly redirected the request; no credential was printed.")
                raw = response.read(262145)
                if len(raw) > 262144:
                    raise OpsError("Telegram response exceeded the operator size limit.")
                body = json.loads(raw)
        except OpsError:
            raise
        except (OSError, ValueError, urllib.error.URLError):
            raise OpsError("Telegram API request failed; check the token, HTTPS and network access. No credential was printed.") from None
        if not isinstance(body, dict) or body.get("ok") is not True or "result" not in body:
            raise OpsError("Telegram API rejected the request; no remote error text or credential was printed.")
        return body["result"]


def verified_identity(client, values=None):
    result = client.call("getMe")
    if (not isinstance(result, dict) or type(result.get("id")) is not int or result["id"] <= 0
            or result["id"] > 9223372036854775807 or result.get("is_bot") is not True
            or not isinstance(result.get("username"), str)
            or not re.fullmatch(r"[A-Za-z][A-Za-z0-9_]{1,28}[Bb][Oo][Tt]", result["username"])):
        raise OpsError("Telegram did not return a valid bot identity.")
    if values and (values["TELEGRAM_BOT_USERNAME"].lower() != result["username"].lower()
                   or int(values["TELEGRAM_BOT_TOKEN"].partition(":")[0]) != result["id"]):
        raise OpsError("Telegram identity does not match the prepared private bot config.")
    return {"id": result["id"], "username": result["username"]}


def webhook_info(client):
    info = client.call("getWebhookInfo")
    if (not isinstance(info, dict) or not isinstance(info.get("url"), str)
            or type(info.get("pending_update_count")) is not int or info["pending_update_count"] < 0):
        raise OpsError("Telegram returned unsupported webhook metadata.")
    if info["url"]:
        parsed = urllib.parse.urlsplit(info["url"])
        if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password:
            raise OpsError("The existing webhook is not a supported HTTPS endpoint; inspect it privately.")
    return info


def report_webhook(info, target):
    if not info["url"]:
        print("Telegram preflight: no existing webhook; pending updates=" + str(info["pending_update_count"]) + ".")
    else:
        origin = urllib.parse.urlsplit(info["url"]).hostname
        digest = hashlib.sha256(info["url"].encode()).hexdigest()
        print("Telegram preflight: webhook host=" + origin + "; target=" + ("same" if info["url"] == target else "different")
              + "; URL SHA256=" + digest + "; pending updates=" + str(info["pending_update_count"]) + ".")
    print("Pending updates will be retained; existing webhook paths and remote error text are hidden.")


def public_check(origin, connected=None):
    for endpoint in ("/healthz", "/readyz", "/api/v1/system/status"):
        target = origin + endpoint
        try:
            with urllib.request.build_opener(NoRedirect()).open(target, timeout=15) as response:
                if response.geturl() != target:
                    raise OpsError("Public readiness unexpectedly redirected.")
                raw = response.read(65537)
                if len(raw) > 65536:
                    raise ValueError()
                result = json.loads(raw)
        except OpsError:
            raise
        except (OSError, ValueError, urllib.error.URLError):
            raise OpsError("Public HTTPS readiness failed; inspect DNS, TLS and private service logs.") from None
        if not isinstance(result, dict):
            raise OpsError("Public readiness returned an unsupported response.")
        if endpoint in ("/healthz", "/readyz") and result.get("status") != ("alive" if endpoint == "/healthz" else "ready"):
            raise OpsError("Public health/readiness failed.")
        if endpoint.endswith("/status") and (result.get("environment") != "production" or result.get("databaseReady") is not True
                or connected is not None and result.get("botConnected") is not connected):
            raise OpsError("Public production status did not match the requested bot state.")


def cli_error(error):
    if isinstance(error, OpsError):
        return str(error)
    return "Operation failed; inspect private paths, permissions and service health. No credential was printed."


def stop_delivery():
    """Emergency stop needs no bot token or database connection."""
    env=clean_environment();prefix=['docker','--host','unix:///var/run/docker.sock']
    try:
        ids=[]
        for service in ('worker','sender'):
            r=subprocess.run(prefix+['ps','-q','--filter','label=com.docker.compose.project=unibot-web','--filter','label=com.docker.compose.service='+service],capture_output=True,text=True,check=True,env=env,timeout=20)
            ids.extend(r.stdout.split())
        if any(not re.fullmatch('[a-f0-9]{12,64}',cid) for cid in ids):raise OpsError('Unexpected Docker service identifier.')
        if ids:subprocess.run(prefix+['stop',*ids],capture_output=True,check=True,env=env,timeout=45)
    except (OSError,subprocess.SubprocessError):raise OpsError('Could not stop delivery containers; inspect the local Docker daemon.') from None

#!/usr/bin/env python3
"""Exercise bootstrap guards with a private filesystem and mocked host commands.

Run: python3 tests/ubuntu-installer.py
No real package manager, network, Docker daemon or service manager is invoked.
"""
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
INSTALLER = ROOT / "tools/deploy/install-ubuntu.sh"
HELPER = ROOT / "tools/deploy/ubuntu-web.py"
REVISION = "a" * 40
OTHER_REVISION = "b" * 40

MOCK = r'''import json, os, pathlib, shutil, subprocess, sys
name=pathlib.Path(sys.argv[0]).name
args=sys.argv[1:]
root=pathlib.Path(os.environ['UNIBOT_TEST_ROOT'])
def log(command, arguments):
    with (root/'calls.jsonl').open('a') as stream:
        stream.write(json.dumps([command, *arguments])+'\n')
if name=='python3':
    if args and args[0].endswith(('/tools/deploy/install-data.py','/tools/deploy/enable-telegram.py','/tools/deploy/replace-web-proxy.py')):
        log('staged-helper',args)
        sys.exit(0)
    if args and args[0].endswith('/tools/deploy/ubuntu-web.py'):
        action=args[1]
        log('helper', args[1:])
        if os.environ.get('UNIBOT_TEST_FAIL')==action:
            sys.exit(2)
        if action not in ('init', 'validate'):
            sys.exit(0)
    sys.exit(subprocess.call([sys.executable, *args]))
log(name, args)
if name=='dpkg':
    print(os.environ.get('UNIBOT_TEST_ARCH', 'amd64'))
elif name=='df':
    print('Filesystem 1024-blocks Used Available Capacity Mounted on')
    print('/dev/test 52428800 1000 '+os.environ.get('UNIBOT_TEST_SPACE', '20000000')+' 1% /')
elif name=='docker':
    if 'compose' in args and 'version' in args:
        print(os.environ.get('UNIBOT_TEST_COMPOSE', '2.24.0'))
elif name=='git':
    if args[0]=='ls-remote':
        (root/'git-terminal-prompt').write_text(os.environ.get('GIT_TERMINAL_PROMPT', ''))
        if os.environ.get('UNIBOT_TEST_AUTH_DENIED'):
            print('credential-secret-fixture: repository authentication failed', file=sys.stderr)
            sys.exit(128)
        print(os.environ['UNIBOT_TEST_REVISION']+'\trefs/heads/main')
    elif args[0]=='clone':
        release=pathlib.Path(args[-1])
        (release/'.git').mkdir(parents=True)
        (release/'.git/HEAD').write_text(os.environ['UNIBOT_TEST_REVISION'])
        destination=release/'tools/deploy/ubuntu-web.py'
        destination.parent.mkdir(parents=True)
        shutil.copyfile(root/'helper.py', destination)
    elif args[0]=='-C':
        release=pathlib.Path(args[1])
        command=args[2:]
        if command[0]=='checkout':
            (release/'.git/HEAD').write_text(command[-1])
        elif command[0]=='rev-parse':
            print((release/'.git/HEAD').read_text())
        elif command[0]=='remote':
            print('https://github.com/Haj-amirreza/tg_go.git')
        elif command[0]=='status':
            print(' M local-file' if os.environ.get('UNIBOT_TEST_DIRTY') else '', end='')
        else:
            sys.exit('Unexpected mocked git command')
    else:
        sys.exit('Unexpected mocked git command')
elif name=='curl':
    pathlib.Path(args[args.index('-o')+1]).write_text('test key')
elif name=='dpkg-query':
    sys.exit(1)
elif name not in ('apt-get', 'systemctl'):
    sys.exit('Unexpected mocked host command')
'''


class UbuntuBootstrap(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="unibot-bootstrap-test-")
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        self.bin = self.directory / "bin"
        self.bin.mkdir()
        self.config = self.directory / "etc/unibot-web/web.env"
        self.releases = self.directory / "releases"
        self.release = self.releases / REVISION
        paths = {
            "/etc/os-release": str(self.directory / "os-release"),
            "/opt/unibot/releases": str(self.releases),
            "/etc/unibot-web": str(self.config.parent),
            "/etc/apt/keyrings": str(self.directory / "etc/apt/keyrings"),
            "/etc/apt/sources.list.d": str(self.directory / "etc/apt/sources.list.d"),
            "/var/backups/unibot-web": str(self.directory / "backups"),
        }
        source = INSTALLER.read_text()
        source = re.sub("|".join(re.escape(path) for path in paths), lambda m: paths[m[0]], source)
        source = source.replace("df -Pk /opt", "df -Pk " + str(self.directory))
        # Only this private copy simulates sudo. All privileged host commands are mocks.
        self.assertEqual(source.count("((EUID == 0))"), 1)
        source = source.replace("((EUID == 0))", '[[ ${UNIBOT_TEST_ROOT_USER:-0} == 1 ]]')
        self.script = self.directory / "install-ubuntu.sh"
        self.script.write_text(source)
        (self.directory / "helper.py").write_text(HELPER.read_text().replace(
            "/etc/unibot-web/web.env", str(self.config)))
        for command in ("apt-get", "curl", "docker", "dpkg", "dpkg-query", "df", "git", "systemctl", "python3"):
            executable = self.bin / command
            executable.write_text("#!" + sys.executable + "\n" + MOCK)
            executable.chmod(0o700)
        self.environment = os.environ.copy()
        self.environment.update(PATH=str(self.bin) + ":/usr/bin:/bin", UNIBOT_TEST_ROOT=str(self.directory),
                                UNIBOT_TEST_REVISION=REVISION, UNIBOT_TEST_ROOT_USER="1")
        self.os_release()

    def os_release(self, version="24.04", system="ubuntu"):
        (self.directory / "os-release").write_text(f'ID={system}\nVERSION_ID="{version}"\n')

    def call(self, *arguments):
        return subprocess.run(["/bin/bash", str(self.script), *arguments], env=self.environment,
                              stdin=subprocess.DEVNULL, capture_output=True, text=True)

    def calls(self):
        path = self.directory / "calls.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def clear_calls(self):
        (self.directory / "calls.jsonl").unlink(missing_ok=True)

    def assert_no_host_changes(self, allow_repository_probe=False):
        calls = [call for call in self.calls() if not (allow_repository_probe and call[:2] == ["git", "ls-remote"])]
        self.assertFalse(any(call[0] in ("apt-get", "curl", "git", "docker", "systemctl", "helper") for call in calls))
        self.assertFalse(self.releases.exists())
        self.assertFalse((self.directory / "backups").exists())

    def create_config(self):
        self.config.parent.mkdir(parents=True, exist_ok=True)
        result = subprocess.run([sys.executable, str(HELPER), "--env-file", str(self.config), "init",
                                 "--domain", "panel.example.org", "--image", "unibot-next:web-" + REVISION],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def simulate_missing_docker(self):
        source = self.script.read_text()
        condition = "if ! command -v docker >/dev/null; then"
        self.assertEqual(source.count(condition), 1)
        self.script.write_text(source.replace(condition, "if true; then"))
        (self.directory / "etc/apt/sources.list.d").mkdir(parents=True)

    def test_supported_systems_check_without_host_changes(self):
        self.environment["UNIBOT_TEST_ROOT_USER"] = "0"
        for version, architecture in (("22.04", "amd64"), ("24.04", "arm64")):
            with self.subTest(version=version, architecture=architecture):
                self.os_release(version)
                self.environment["UNIBOT_TEST_ARCH"] = architecture
                self.clear_calls()
                result = self.call("--domain", "panel.example.org", "--check")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("Preflight passed", result.stdout)
                self.assert_no_host_changes()
                self.assertFalse(self.config.exists())

    def test_unsupported_host_and_insufficient_disk_fail_before_changes(self):
        for version, system, architecture, space in (("24.04", "debian", "amd64", "20000000"),
                ("20.04", "ubuntu", "amd64", "20000000"), ("24.04", "ubuntu", "i386", "20000000"),
                ("24.04", "ubuntu", "amd64", "15728639")):
            with self.subTest(version=version, system=system, architecture=architecture, space=space):
                self.os_release(version, system)
                self.environment.update(UNIBOT_TEST_ARCH=architecture, UNIBOT_TEST_SPACE=space)
                self.clear_calls()
                self.assertNotEqual(self.call("--domain", "panel.example.org", "--check").returncode, 0)
                self.assert_no_host_changes()

    def test_invalid_arguments_fail_before_changes(self):
        arguments = [("--domain", value, "--check") for value in ("localhost", "127.0.0.1", "panel.example.org.",
                     "https://panel.example.org", "panel.example.org:443", "bad_host.example.org")]
        arguments += [("--domain", "panel.example.org", "--ref", value, "--check")
                      for value in ("main", "--help", "a" * 39, "g" * 40)]
        arguments += [("--domain",), ("--unknown",), ()]
        for args in arguments:
            with self.subTest(args=args):
                self.clear_calls()
                self.assertNotEqual(self.call(*args).returncode, 0)
                self.assert_no_host_changes()

    def test_headless_first_install_requires_explicit_skip(self):
        result = self.call("--domain", "panel.example.org", "--ref", REVISION)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("--skip-bootstrap", result.stderr)
        self.assert_no_host_changes()

    def test_repository_authentication_failure_stops_before_host_changes(self):
        self.environment.update(UNIBOT_TEST_AUTH_DENIED="1", GIT_TERMINAL_PROMPT="1")
        result = self.call("--domain", "panel.example.org", "--ref", REVISION, "--skip-bootstrap")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("gh auth login", result.stdout + result.stderr)
        self.assertNotIn("credential-secret-fixture", result.stdout + result.stderr)
        self.assertEqual((self.directory / "git-terminal-prompt").read_text(), "0")
        self.assertEqual([call for call in self.calls() if call[0] == "git"], [["git", "ls-remote", "--exit-code",
                         "https://github.com/Haj-amirreza/tg_go.git", "refs/heads/main"]])
        self.assert_no_host_changes(allow_repository_probe=True)

    def test_missing_docker_installs_official_repository_in_order(self):
        self.simulate_missing_docker()
        self.os_release("22.04")
        self.environment["UNIBOT_TEST_ARCH"] = "arm64"
        result = self.call("--domain", "panel.example.org", "--ref", REVISION, "--skip-bootstrap")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        self.assertEqual([call[1:] for call in calls if call[0] == "apt-get"], [
            ["update"], ["install", "-y", "ca-certificates", "curl", "git", "python3"],
            ["update"], ["install", "-y", "docker-ce", "docker-ce-cli", "containerd.io",
                        "docker-buildx-plugin", "docker-compose-plugin"]])
        download = next(call for call in calls if call[0] == "curl")
        self.assertEqual(download[1:-2], ["--fail", "--show-error", "--silent", "--location", "--proto", "=https",
                                        "--proto-redir", "=https", "https://download.docker.com/linux/ubuntu/gpg"])
        self.assertEqual(download[-2], "-o")
        key = self.directory / "etc/apt/keyrings/docker.asc"
        self.assertEqual(key.read_text(), "test key")
        self.assertEqual(key.stat().st_mode & 0o777, 0o644)
        self.assertFalse(list(key.parent.glob(".unibot-docker.*")))
        sources = (self.directory / "etc/apt/sources.list.d/docker.sources").read_text()
        self.assertEqual(sources, "Types: deb\nURIs: https://download.docker.com/linux/ubuntu\n"
                         "Suites: jammy\nComponents: stable\nArchitectures: arm64\nSigned-By: " + str(key) + "\n")
        important = [call[0] for call in calls if call[0] in ("apt-get", "curl", "systemctl", "docker", "helper")]
        self.assertEqual(important[:8], ["apt-get", "apt-get", "curl", "apt-get", "apt-get", "systemctl", "docker", "docker"])

    def test_missing_docker_preserves_existing_repository_files_and_links(self):
        self.simulate_missing_docker()
        source = self.directory / "etc/apt/sources.list.d/docker.sources"
        source.write_text("existing operator repository\n")
        result = self.call("--domain", "panel.example.org", "--ref", REVISION, "--skip-bootstrap")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(source.read_text(), "existing operator repository\n")
        self.assert_no_host_changes(allow_repository_probe=True)
        source.unlink()
        key = self.directory / "etc/apt/keyrings/docker.asc"
        key.parent.mkdir()
        key.symlink_to(self.directory / "missing-operator-key")
        self.clear_calls()
        result = self.call("--domain", "panel.example.org", "--ref", REVISION, "--skip-bootstrap")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(key.is_symlink())
        self.assertEqual(key.readlink(), self.directory / "missing-operator-key")
        self.assert_no_host_changes(allow_repository_probe=True)

    def test_existing_config_is_checked_and_preserved_before_changes(self):
        self.create_config()
        original = self.config.read_bytes()
        cases = ("public", "wrong-domain", "incomplete", "symlink", "wrong-release")
        for case in cases:
            with self.subTest(case=case):
                if self.config.is_symlink():
                    self.config.unlink()
                self.config.write_bytes(original)
                self.config.chmod(0o600)
                revision = REVISION
                if case == "public":
                    self.config.chmod(0o644)
                elif case == "wrong-domain":
                    self.config.write_bytes(original.replace(b"panel.example.org", b"other.example.org"))
                elif case == "incomplete":
                    self.config.write_text("APP_DOMAIN=panel.example.org\n")
                elif case == "symlink":
                    target = self.directory / "target.env"
                    self.config.rename(target)
                    self.config.symlink_to(target)
                else:
                    revision = OTHER_REVISION
                before = self.config.read_bytes()
                self.clear_calls()
                result = self.call("--domain", "panel.example.org", "--ref", revision, "--skip-bootstrap", "--check")
                self.assertNotEqual(result.returncode, 0, case + ": " + result.stdout)
                self.assertEqual(self.config.read_bytes(), before)
                self.assert_no_host_changes()

    def test_install_and_same_revision_resume_preserve_credentials(self):
        args = ("--domain", "panel.example.org", "--ref", REVISION, "--skip-bootstrap")
        result = self.call(*args)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([call[1] for call in self.calls() if call[0] == "helper"],
                         ["init", "validate", "build", "up", "check"])
        self.assertEqual(self.config.stat().st_mode & 0o777, 0o600)
        original = self.config.read_bytes()
        self.clear_calls()
        result = self.call(*args)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.config.read_bytes(), original)
        self.assertEqual([call[1:] for call in self.calls() if call[0] == "helper"],
                         [["validate", "--offline"], ["validate"], ["build"], ["up"], ["check"]])
        self.assertFalse(any(call[0] == "git" and call[1] == "clone" for call in self.calls()))
        for line in original.decode().splitlines():
            if line.startswith(("APP_KEY=", "POSTGRES_PASSWORD=", "APP_DB_PASSWORD=")):
                self.assertNotIn(line.partition("=")[2], result.stdout + result.stderr)

    def test_dirty_existing_release_is_preserved(self):
        args = ("--domain", "panel.example.org", "--ref", REVISION, "--skip-bootstrap")
        self.assertEqual(self.call(*args).returncode, 0)
        original = self.config.read_bytes()
        self.environment["UNIBOT_TEST_DIRTY"] = "1"
        self.clear_calls()
        result = self.call(*args)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("local edits", result.stderr)
        self.assertFalse(any(call[0] == "helper" for call in self.calls()))
        self.assertEqual(self.config.read_bytes(), original)

    def test_failed_build_stops_before_up_and_can_resume(self):
        args = ("--domain", "panel.example.org", "--ref", REVISION, "--skip-bootstrap")
        self.environment["UNIBOT_TEST_FAIL"] = "build"
        self.assertNotEqual(self.call(*args).returncode, 0)
        self.assertEqual([call[1] for call in self.calls() if call[0] == "helper"], ["init", "validate", "build"])
        original = self.config.read_bytes()
        del self.environment["UNIBOT_TEST_FAIL"]
        self.clear_calls()
        result = self.call(*args)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.config.read_bytes(), original)


    def test_occupied_web_port_stops_before_package_or_service_changes(self):
        probe = self.bin / 'ss'
        probe.write_text('#!/bin/sh\nprintf "LISTEN 0 511 0.0.0.0:80 0.0.0.0:*\\n"\n')
        probe.chmod(0o700)
        result = self.call('--domain', 'panel.example.org', '--ref', REVISION, '--skip-bootstrap')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('occupied', result.stderr)
        self.assertFalse(any(c[0] in ('apt-get', 'systemctl', 'git', 'helper') for c in self.calls()))
        self.assertFalse(self.releases.exists())


    def test_with_bot_runs_data_prepare_activate_in_order(self):
        # The private copy simulates a TTY; all staged helpers remain mocked.
        self.script.write_text(self.script.read_text().replace('[[ ! -t 0 ]]','false'))
        result=self.call('--domain','panel.example.org','--ref',REVISION,'--skip-bootstrap','--with-bot')
        self.assertEqual(result.returncode,0,result.stderr)
        stages=[c for c in self.calls() if c[0]=='staged-helper']
        self.assertEqual(len(stages),3)
        self.assertTrue(stages[0][1].endswith('/install-data.py'))
        self.assertEqual(stages[1][2],'prepare')
        self.assertEqual(stages[2][2],'activate')

if __name__ == "__main__":
    unittest.main()

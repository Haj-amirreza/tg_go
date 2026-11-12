#!/usr/bin/env python3
"""Installer safety checks using temporary private files and a fake Docker CLI.

Run: python3 tests/ubuntu-web.py
No packages, images, containers, database or host configuration are changed.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "tools/deploy/ubuntu-web.py"


class InstallerGuards(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="unibot-install-test-")
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.config = self.directory / "web.env"
        self.environment = os.environ.copy()
        self.assertEqual(self.call("init", "--domain", "web.example.org", "--image", "unibot-next:web-test").returncode, 0)

    def call(self, *arguments):
        return subprocess.run([sys.executable, str(SCRIPT), "--env-file", str(self.config), *arguments],
                              env=self.environment, capture_output=True, text=True)

    def fake_docker(self):
        executable = self.directory / "docker"
        executable.write_text("""#!/usr/bin/env python3
import json, os, sys
with open(os.environ['UNIBOT_TEST_CALLS'], 'a') as stream:
    stream.write(json.dumps({'args': sys.argv[1:], 'selectors': [key for key in os.environ
      if (key.startswith(('DOCKER_', 'COMPOSE_', 'BUILDX_', 'BUILDKIT_', 'BAKE_')) and key != 'DOCKER_CONFIG')
      or key in ('APP_DOMAIN', 'APP_KEY', 'APP_IMAGE')],
      'registry_config_preserved': os.environ.get('DOCKER_CONFIG') == os.environ.get('UNIBOT_TEST_DOCKER_CONFIG')}) + '\\n')
if os.environ.get('UNIBOT_TEST_FAIL_MIGRATE') and 'migrate' in sys.argv:
    sys.exit(2)
""")
        executable.chmod(0o700)
        self.environment.update(PATH=str(self.directory) + os.pathsep + os.environ["PATH"],
                                UNIBOT_TEST_CALLS=str(self.directory / "calls"))

    def test_new_private_config_preserves_existing_credentials(self):
        original = self.config.read_bytes()
        self.assertEqual(self.config.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.call("validate", "--offline").returncode, 0)
        result = self.call("init", "--domain", "other.example.org", "--image", "unibot-next:web-other")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.config.read_bytes(), original)
        for line in original.decode().splitlines():
            if line.startswith(("POSTGRES_PASSWORD=", "APP_DB_PASSWORD=", "APP_KEY=")):
                self.assertFalse(line.partition("=")[2] in result.stdout + result.stderr,
                                 "An installer response disclosed a generated credential")

    def test_invalid_domains_cannot_create_config(self):
        self.config.unlink()
        for hostname in ("localhost", "127.0.0.1", "https://web.example.org", "web.example.org:443", "bad_host.example.org"):
            with self.subTest(hostname=hostname):
                self.assertNotEqual(self.call("init", "--domain", hostname, "--image", "unibot-next:web-test").returncode, 0)
                self.assertFalse(self.config.exists())

    def test_public_permissions_and_symlinks_are_refused(self):
        self.config.chmod(0o644)
        self.assertNotEqual(self.call("validate", "--offline").returncode, 0)
        self.config.chmod(0o600)
        target = self.directory / "target.env"
        self.config.rename(target)
        original = target.read_bytes()
        self.config.symlink_to(target)
        self.assertNotEqual(self.call("validate", "--offline").returncode, 0)
        self.assertNotEqual(self.call("init", "--domain", "web.example.org", "--image", "unibot-next:web-test").returncode, 0)
        self.assertEqual(target.read_bytes(), original)

    def test_telegram_configuration_is_refused(self):
        with self.config.open("a") as stream:
            stream.write("TELEGRAM_SEND_ENABLED=true\n")
        self.assertNotEqual(self.call("validate", "--offline").returncode, 0)

    def test_shell_cannot_select_remote_docker_or_override_config(self):
        self.fake_docker()
        self.environment.update(DOCKER_HOST="tcp://remote.invalid:2375", DOCKER_CONTEXT="remote",
                                COMPOSE_FILE="/does/not/exist", COMPOSE_PROJECT_NAME="wrong",
                                BUILDX_BUILDER="remote", APP_DOMAIN="wrong.example.org", APP_KEY="wrong",
                                DOCKER_CONFIG=str(self.directory), UNIBOT_TEST_DOCKER_CONFIG=str(self.directory))
        self.assertEqual(self.call("validate").returncode, 0)
        call = json.loads((self.directory / "calls").read_text())
        self.assertEqual(call["args"][:3], ["--host", "unix:///var/run/docker.sock", "compose"])
        self.assertIn("unibot-web", call["args"])
        self.assertEqual(call["args"][-2:], ["config", "--quiet"])
        self.assertEqual(call["selectors"], [])
        self.assertTrue(call["registry_config_preserved"])

    def test_failed_migration_leaves_web_stopped(self):
        self.fake_docker()
        self.environment["UNIBOT_TEST_FAIL_MIGRATE"] = "1"
        self.assertNotEqual(self.call("up").returncode, 0)
        calls = [json.loads(line)["args"] for line in (self.directory / "calls").read_text().splitlines()]
        self.assertTrue(any("stop" in call and "api" in call for call in calls))
        self.assertFalse(any("up" in call and "api" in call for call in calls))


if __name__ == "__main__":
    unittest.main()

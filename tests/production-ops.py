#!/usr/bin/env python3
"""Production operator safety checks; all Docker and Telegram calls are mocked.

Run: python3 tests/production-ops.py
No host configuration, containers, services or network endpoints are changed.
"""
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import sys
import tempfile
import unittest
from unittest import mock
import urllib.error


ROOT = Path(__file__).resolve().parents[1]
DEPLOY = ROOT / "tools/deploy"
sys.path.insert(0, str(DEPLOY))


def load_module(name, filename):
    spec = importlib.util.spec_from_file_location(name, DEPLOY / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


ops = load_module("production_ops", "production_ops.py")
sys.modules["production_ops"] = ops


class PrivateConfiguration(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="unibot-ops-test-")
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.web = self.directory / "web.env"
        self.bot = self.directory / "bot.env"
        self.web_values = dict(APP_DOMAIN="panel.example.org", APP_ORIGIN="https://panel.example.org",
                               APP_IMAGE="unibot-next:web-test", POSTGRES_PASSWORD="1" * 48,
                               APP_DB_PASSWORD="2" * 48, APP_KEY="3" * 64)
        self.bot_values = dict(TELEGRAM_BOT_TOKEN="123456789:" + "A" * 35,
                               TELEGRAM_WEBHOOK_SECRET="0123456789abcdef" * 4,
                               TELEGRAM_BOT_USERNAME="unibot_test_bot")
        self.write(self.web, self.web_values)

    @staticmethod
    def write(path, values):
        path.write_text("".join(key + "=" + value + "\n" for key, value in values.items()))
        path.chmod(0o600)

    def test_original_six_key_web_config_stays_compatible(self):
        self.assertEqual(ops.load_web(self.web), self.web_values)
        with self.web.open("a") as stream:
            stream.write("TELEGRAM_BOT_TOKEN=must-not-be-in-web-config\n")
        with self.assertRaises(ops.OpsError):
            ops.load_web(self.web)

    def test_bot_file_is_private_and_create_never_overwrites(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            ops.write_bot(self.bot, self.bot_values)
        self.assertEqual(stat.S_IMODE(self.bot.stat().st_mode), 0o600)
        self.assertEqual(ops.load_bot(self.bot), self.bot_values)
        original = self.bot.read_bytes()
        with self.assertRaises((ops.OpsError, FileExistsError)):
            ops.write_bot(self.bot, {**self.bot_values, "TELEGRAM_WEBHOOK_SECRET": "fedcba9876543210" * 4})
        self.assertEqual(self.bot.read_bytes(), original)
        for value in self.bot_values.values():
            self.assertNotIn(value, output.getvalue())

    def test_bot_permissions_links_and_unexpected_keys_are_rejected(self):
        self.write(self.bot, self.bot_values)
        self.bot.chmod(0o644)
        with self.assertRaises(ops.OpsError):
            ops.load_bot(self.bot)
        self.bot.chmod(0o600)
        real = self.directory / "real-bot.env"
        self.bot.rename(real)
        self.bot.symlink_to(real)
        with self.assertRaises(ops.OpsError):
            ops.load_bot(self.bot)
        self.bot.unlink()
        self.write(self.bot, {**self.bot_values, "TELEGRAM_SEND_ENABLED": "true"})
        with self.assertRaises(ops.OpsError):
            ops.load_bot(self.bot)

    def test_parent_shell_cannot_override_private_settings_or_daemon(self):
        injected = {**self.web_values, **self.bot_values, "DOCKER_HOST": "tcp://remote.invalid:2375",
                    "COMPOSE_FILE": "/wrong.yaml", "BUILDX_BUILDER": "remote",
                    "DOCKER_CONFIG": str(self.directory), "PATH": os.environ.get("PATH", "")}
        with mock.patch.dict(os.environ, injected, clear=True):
            result = ops.clean_environment()
        self.assertEqual(result.get("DOCKER_CONFIG"), str(self.directory))
        for key in injected:
            if key not in ("DOCKER_CONFIG", "PATH"):
                self.assertNotIn(key, result)

    def test_stack_invocation_keeps_credentials_out_of_arguments(self):
        self.write(self.bot, self.bot_values)
        with mock.patch.object(ops.subprocess, "run") as run:
            run.return_value = mock.Mock(returncode=0, stdout="", stderr="")
            stack = ops.Stack(self.web, self.bot)
            stack.mode("bot-prepare", {"id": 123456789, "username": "unibot_test_bot"})
        arguments = run.call_args.args[0]
        self.assertEqual(arguments[:3], ["docker", "--host", "unix:///var/run/docker.sock"])
        for value in list(self.web_values.values()) + list(self.bot_values.values()):
            self.assertNotIn(value, " ".join(arguments))
        self.assertIn("bot-prepare", arguments)
        self.assertEqual(json.loads(run.call_args.kwargs["input"]),
                         {"id": 123456789, "username": "unibot_test_bot"})


    def test_docker_error_records_stage_privately_without_credentials(self):
        self.write(self.bot,self.bot_values)
        stack=ops.Stack(self.web,self.bot)
        secret=self.web_values['APP_KEY'];token=self.bot_values['TELEGRAM_BOT_TOKEN']
        error=ops.subprocess.CalledProcessError(1,['docker'],stderr='build failure '+secret+' '+token)
        with mock.patch.object(ops.subprocess,'run',side_effect=error):
            with self.assertRaises(ops.OpsError) as caught:stack.run('build','api')
        log=self.directory/'last-operation-error.log'
        self.assertEqual(log.stat().st_mode&0o777,0o600)
        self.assertIn('stage: build',log.read_text())
        for value in (secret,token):
            self.assertNotIn(value,log.read_text());self.assertNotIn(value,str(caught.exception))

class TelegramBoundaries(unittest.TestCase):
    TOKEN = "123456789:" + "A" * 35

    def response(self, payload, url=None):
        response = mock.MagicMock()
        response.__enter__.return_value = response
        response.read.return_value = json.dumps(payload).encode()
        response.geturl.return_value = url or "https://api.telegram.org/bot" + self.TOKEN + "/getMe"
        return response

    def test_identity_request_is_fixed_https_bounded_and_not_in_subprocess(self):
        response = self.response({"ok": True, "result": {"id": 123456789, "is_bot": True, "username": "unibot_test_bot"}})
        opener = mock.Mock()
        opener.open.return_value = response
        with mock.patch.object(ops.urllib.request, "build_opener", return_value=opener), \
                mock.patch.object(ops.subprocess, "run", side_effect=AssertionError("No subprocess for token")):
            result = ops.Telegram(self.TOKEN).call("getMe")
        self.assertEqual(result["id"], 123456789)
        request = opener.open.call_args.args[0]
        self.assertEqual(request.full_url, "https://api.telegram.org/bot" + self.TOKEN + "/getMe")
        self.assertGreater(opener.open.call_args.kwargs["timeout"], 0)
        self.assertLessEqual(opener.open.call_args.kwargs["timeout"], 30)
        self.assertGreater(response.read.call_args.args[0], 0)
        self.assertLessEqual(response.read.call_args.args[0], 1024 * 1024)

    def test_http_and_server_errors_cannot_disclose_token_or_description(self):
        error = urllib.error.HTTPError("https://api.telegram.org/bot" + self.TOKEN + "/getMe", 401,
                                       "sensitive " + self.TOKEN, {}, None)
        for outcome in (error, self.response({"ok": False, "description": "sensitive " + self.TOKEN})):
            with self.subTest(kind=type(outcome).__name__):
                opener = mock.Mock()
                if isinstance(outcome, Exception):
                    opener.open.side_effect = outcome
                else:
                    opener.open.return_value = outcome
                with mock.patch.object(ops.urllib.request, "build_opener", return_value=opener):
                    with self.assertRaises(ops.OpsError) as caught:
                        ops.Telegram(self.TOKEN).call("getMe")
                self.assertNotIn(self.TOKEN, str(caught.exception))
                self.assertNotIn("sensitive", str(caught.exception))

    def test_registration_retains_pending_updates(self):
        response = self.response({"ok": True, "result": True},
                                 "https://api.telegram.org/bot" + self.TOKEN + "/setWebhook")
        opener = mock.Mock()
        opener.open.return_value = response
        payload = {"url": "https://panel.example.org/api/v1/telegram/webhook", "secret_token": "fedcba9876543210" * 4,
                   "drop_pending_updates": False}
        with mock.patch.object(ops.urllib.request, "build_opener", return_value=opener):
            self.assertTrue(ops.Telegram(self.TOKEN).call("setWebhook", payload))
        self.assertIs(json.loads(opener.open.call_args.args[0].data)["drop_pending_updates"], False)


if __name__ == "__main__":
    unittest.main()

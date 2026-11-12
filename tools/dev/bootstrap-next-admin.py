"""Offline first-admin setup for an EMPTY local development database; no secrets in argv."""
from pathlib import Path
import getpass
import json
import subprocess
root = Path(__file__).resolve().parents[2]
username = input('Admin username: ').strip()
name = input('Display name: ').strip()
telegram = input('Registered numeric Telegram ID (optional): ').strip()
password = getpass.getpass('New password (12–72 bytes): ')
if password != getpass.getpass('Repeat password: '):
    raise SystemExit('Passwords differ.')
subprocess.run([
    'docker', 'compose', '--env-file', '.env.next', '-f', 'compose.next.yaml',
    'run', '--rm', '-T', '--no-deps', 'migrate', '-mode', 'admin-bootstrap'
], cwd=root, input=json.dumps(dict(username=username, name=name, password=password, telegram_id=telegram)), text=True, check=True)
print('First local administrator created.')

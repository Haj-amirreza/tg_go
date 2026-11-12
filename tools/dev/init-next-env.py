"""Create or explicitly extend private LOCAL development config; never prints values."""
from pathlib import Path
import argparse
import os
import secrets
parser = argparse.ArgumentParser()
parser.add_argument('--upgrade', action='store_true', help='Add missing identity settings to an existing local development env')
args = parser.parse_args()
root = Path(__file__).resolve().parents[2]
path = root / '.env.next'
if args.upgrade:
    # Refuse symlinks or permissive files. Existing values and passwords remain intact.
    if path.is_symlink() or not path.is_file() or path.stat().st_mode & 0o077:
        raise SystemExit('A private regular .env.next (0600) is required.')
    existing = path.read_text()
    names = {line.split('=', 1)[0] for line in existing.splitlines() if '=' in line}
    additions = ''
    if 'APP_KEY' not in names:
        additions += 'APP_KEY=' + secrets.token_hex(32) + '\n'
    if 'APP_ORIGIN' not in names:
        additions += 'APP_ORIGIN=http://127.0.0.1:8080\n'
    if additions:
        with path.open('a') as stream:
            stream.write(('' if existing.endswith('\n') else '\n') + additions)
    print('Local identity settings checked; existing values preserved.')
else:
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write('POSTGRES_PASSWORD=' + secrets.token_hex(24) + '\n')
        stream.write('APP_DB_PASSWORD=' + secrets.token_hex(24) + '\nNEXT_HTTP_PORT=8080\n')
        stream.write('APP_KEY=' + secrets.token_hex(32) + '\nAPP_ORIGIN=http://127.0.0.1:8080\n')
    print('Created private .env.next for development; values were not printed.')

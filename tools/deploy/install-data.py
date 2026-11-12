#!/usr/bin/env python3
"""Step 2: back up and import only this release's original bundled catalogues."""
import argparse
import os
from pathlib import Path
import subprocess
import tempfile
import sys
from production_ops import Stack, OpsError, operation_lock, cli_error, ROOT

def release_image():
    result=subprocess.run(['git','rev-parse','HEAD'],cwd=ROOT,capture_output=True,text=True,check=True)
    revision=result.stdout.strip()
    import re
    if not re.fullmatch('[a-f0-9]{40}',revision): raise OpsError('Run from a published Git release checkout.')
    dirty=subprocess.run(['git','status','--porcelain','--untracked-files=normal'],cwd=ROOT,capture_output=True,text=True,check=True)
    if dirty.stdout.strip(): raise OpsError('The release has local edits; preserve them and use a clean checkout.')
    return 'unibot-next:web-'+revision

def replace_config(path, content):
    fd,name=tempfile.mkstemp(prefix='.web-env-',dir=path.parent)
    try:
        os.fchmod(fd,0o600)
        with os.fdopen(fd,'w') as out: out.write(content);out.flush();os.fsync(out.fileno())
        os.replace(name,path)
    finally:
        if os.path.exists(name): os.unlink(name)

def install(web_env, backup, prebuilt=False):
    stack = Stack(web_env, prebuilt=prebuilt)
    with operation_lock(web_env):
        stack.run('config', '--quiet')
        services = stack.running()
        if 'db' not in services:
            raise OpsError('Start the web database with step 1 first.')
        if set(services) & {'worker','sender'}:
            raise OpsError('Stop Telegram with step 3 disable before importing; no running bot is modified.')
        state = stack.run('exec','-T','db','psql','-U','postgres','-d','unibot_web','-At','-c',
            '''SELECT COALESCE((SELECT value FROM settings WHERE name='bot_state'),'"off"')''',capture=True).strip()
        if state != '"off"':
            raise OpsError('The bot must be disabled before import.')
        image=release_image()
        original=Path(web_env).read_text()
        writers = [s for s in ('caddy','api','scheduler') if s in services]
        migration_started=False; migrated=False
        try:
            if writers: stack.run('stop', *writers)
            stack.backup(Path(backup), services)
            if stack.values['APP_IMAGE'] != image:
                replace_config(Path(web_env),''.join('APP_IMAGE='+image+'\n' if line.startswith('APP_IMAGE=') else line+'\n' for line in original.splitlines()))
                stack=Stack(web_env,prebuilt=prebuilt)
                stack.run('build','--pull','api',timeout=1200)
            migration_started=True
            stack.run('run','--rm','--no-deps','migrate','-mode','migrate')
            migrated=True
            stack.mode('bot-disable')
            report = stack.mode('import-bundled')
            if report.get('books') != 7991 or report.get('contents') != 670:
                raise OpsError('The image returned unexpected bundled import counts.')
        finally:
            if not migration_started:
                replace_config(Path(web_env),original)
                stack=Stack(web_env,prebuilt=prebuilt)
                if writers: stack.run('start',*writers)
            elif migrated and writers:
                stack.run('up','-d','--no-build','--wait','--wait-timeout','180',*writers)
        print('Bundled data imported: 7991 books, 670 contacts/links (55 published, 615 drafts).')
        print('Original source is encrypted privately; admin edits are preserved on identical replay. Telegram remains disabled.')
        return report

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--env-file',type=Path,default=Path('/etc/unibot-web/web.env'))
    p.add_argument('--backup-directory',type=Path,required=True,help='NEW path under a private 0700 backup parent')
    p.add_argument('--prebuilt',action='store_true')
    a=p.parse_args();install(a.env_file,a.backup_directory,a.prebuilt)

if __name__=='__main__':
    try: main()
    except Exception as error: sys.exit(cli_error(error))

#!/usr/bin/env python3
"""Replace only owners of host TCP 80/443; restore the previous proxy on failure."""
import argparse
import json
from pathlib import Path
import re
import subprocess
import sys
import time
from production_ops import public_check, OpsError

DOCKER=['docker','--host','unix:///var/run/docker.sock']

def run(args):
    return subprocess.run(args,check=True,capture_output=True,text=True).stdout

def replace(command, origin=None):
    containers=[]; units=set(); stopped_containers=[]; stopped_units=[]
    # Inspect port mappings only, never environment variables or tokens.
    for cid in run(DOCKER+['ps','-q']).split():
        ports=json.loads(run(DOCKER+['inspect','--format','{{json .NetworkSettings.Ports}}',cid])) or {}
        if not any(binding.get('HostPort') in ('80','443') for bindings in ports.values() for binding in (bindings or [])): continue
        labels=json.loads(run(DOCKER+['inspect','--format','{{json .Config.Labels}}',cid])) or {}
        if labels.get('com.docker.compose.project')=='unibot-web' and labels.get('com.docker.compose.service')=='caddy': continue
        containers.append(cid)
    listeners=run(['ss','-H','-ltnp','( sport = :80 or sport = :443 )'])
    for line in listeners.splitlines():
        if 'docker-proxy' in line: continue
        pids=re.findall(r'pid=(\d+)',line)
        if not pids: raise RuntimeError('Cannot identify a listener; run this command with sudo.')
        for pid in pids:
            groups=Path('/proc',pid,'cgroup').read_text()
            match=re.search(r'/system.slice/(nginx|apache2|httpd|caddy)\.service(?:/|$)',groups,re.M)
            if not match: raise RuntimeError('An unrecognized process owns TCP 80/443; inspect ss output before replacing it.')
            units.add(match[1]+'.service')
    try:
        for unit in sorted(units): run(['systemctl','stop',unit]);stopped_units.append(unit)
        for cid in containers: run(DOCKER+['stop',cid]);stopped_containers.append(cid)
        result=subprocess.run(command)
        if result.returncode: raise RuntimeError('New proxy startup failed.')
        if origin:
            for attempt in range(6):
                try:
                    public_check(origin,False)
                    break
                except OpsError:
                    if attempt==5: raise RuntimeError('New public HTTPS did not become ready.') from None
                    time.sleep(3)
    except (Exception,KeyboardInterrupt):
        # Release the ports before restarting the previous owners.
        try:
            for cid in run(DOCKER+['ps','-q','--filter','label=com.docker.compose.project=unibot-web',
                    '--filter','label=com.docker.compose.service=caddy']).split(): run(DOCKER+['stop',cid])
        except (OSError,subprocess.SubprocessError): pass
        failures=[]
        for cid in stopped_containers:
            try: run(DOCKER+['start',cid])
            except (OSError,subprocess.SubprocessError): failures.append('container')
        for unit in stopped_units:
            try: run(['systemctl','start',unit])
            except (OSError,subprocess.SubprocessError): failures.append(unit)
        if failures: raise RuntimeError('Rollback needs operator attention; inspect the previous proxy services.') from None
        raise RuntimeError('New proxy failed; previous proxy services were restarted.') from None
    for unit in stopped_units: run(['systemctl','disable',unit])
    for cid in stopped_containers: run(DOCKER+['update','--restart=no',cid])
    if stopped_containers or stopped_units:
        print('Previous port owners stopped and their automatic startup disabled; the new proxy is running. Previous configuration files were preserved.')

def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--verify-origin');p.add_argument('command',nargs=argparse.REMAINDER);a=p.parse_args()
    command=a.command[1:] if a.command[:1]==['--'] else a.command
    if not command: p.error('Provide the startup command after --')
    replace(command,a.verify_origin)

if __name__=='__main__':
    try: main()
    except (Exception,KeyboardInterrupt) as error:
        sys.exit(str(error) if isinstance(error,RuntimeError) else 'Proxy replacement failed; inspect service ownership and permissions.')

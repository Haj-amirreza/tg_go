#!/usr/bin/env python3
"""Step 3: prepare, activate, inspect or disable Telegram without losing pending updates."""
import argparse
import getpass
import json
from pathlib import Path
import re
import secrets
import subprocess
import sys
from production_ops import (Stack, Telegram, OpsError, load_bot, write_bot, validate_bot,
    verified_identity, webhook_info, report_webhook, operation_lock, public_check, cli_error, stop_delivery)

def prepare(web_env, bot_env, prebuilt=False, owner_id=None, admin_ids=(), expected_bot=None):
    base=Stack(web_env,prebuilt=prebuilt)
    with operation_lock(web_env):
        if set(base.running()) & {'worker','sender'}:
            raise OpsError('Disable the running bot before preparing another identity.')
        if bot_env.exists():
            values=load_bot(bot_env)
        else:
            if not sys.stdin.isatty(): raise OpsError('Use an interactive terminal to enter the private BotFather token.')
            token=getpass.getpass('BotFather token (hidden): ').strip()
            if not re.fullmatch(r'[1-9][0-9]{0,18}:[A-Za-z0-9_-]{30,100}',token):
                raise OpsError('Invalid token format; nothing was stored.')
            identity=verified_identity(Telegram(token))
            if expected_bot and identity['username'].lower()!=expected_bot.lstrip('@').lower(): raise OpsError('Token belongs to a different bot; nothing was stored.')
            if int(token.partition(':')[0])!=identity['id']: raise OpsError('Bot identity mismatch.')
            values={'TELEGRAM_BOT_TOKEN':token,'TELEGRAM_BOT_USERNAME':identity['username'],
                    'TELEGRAM_WEBHOOK_SECRET':secrets.token_hex(32)}
            write_bot(bot_env,values)
        identity=verified_identity(Telegram(values['TELEGRAM_BOT_TOKEN']),values)
        if expected_bot and identity['username'].lower()!=expected_bot.lstrip('@').lower(): raise OpsError('Prepared token belongs to a different bot.')
        status=base.mode('bot-status')
        if status.get('enabled') is not False or status.get('import_ready') is not True:
            raise OpsError('Run step 2 successfully and keep the bot disabled before preparation.')
        base.mode('bot-disable')
        if status.get('web_admin_ready') is not True:
            from production_ops import ROOT
            result=subprocess.run([sys.executable,str(ROOT/'tools/deploy/ubuntu-web.py'),'--env-file',str(web_env),'bootstrap'])
            if result.returncode: raise OpsError('Create an active web administrator before linking Telegram.')
        if status.get('admin_ready') is not True:
            username=input('Existing web superadmin username: ').strip()
            numeric=owner_id or input('That administrator\'s numeric Telegram user ID: ').strip()
            if not re.fullmatch(r'[1-9][0-9]{0,18}',numeric) or int(numeric)>9223372036854775807:
                raise OpsError('Use a valid numeric Telegram ID; usernames do not grant administrator access.')
            base.mode('admin-link',{'username':username,'telegram_id':numeric})
        if owner_id: base.mode('admin-add-telegram',{'telegram_id':owner_id})
        for numeric in dict.fromkeys(admin_ids):
            if not re.fullmatch(r'[1-9][0-9]{0,18}',numeric) or int(numeric)>9223372036854775807: raise OpsError('Invalid additional administrator ID.')
            base.mode('admin-add-telegram',{'telegram_id':numeric})
        status=base.mode('bot-prepare',identity)
        if status.get('ready') is not True:
            raise OpsError('Bot preparation has blockers. Run status and review the queue; nothing was discarded.')
        print('Identity and numeric administrator link prepared. Webhook unchanged; queue remains paused.')

def activate(web_env,bot_env,backup,prebuilt=False):
    base=Stack(web_env,prebuilt=prebuilt); bot=Stack(web_env,bot_env,prebuilt)
    with operation_lock(web_env):
        values=load_bot(bot_env);client=Telegram(values['TELEGRAM_BOT_TOKEN'])
        identity=verified_identity(client,values)
        target=base.values['APP_ORIGIN']+'/api/v1/telegram/webhook'
        info=webhook_info(client);report_webhook(info,target)
        before=base.mode('bot-status')
        if before.get('enabled') is not False or before.get('ready') is not True:
            raise OpsError('Prepare the disabled bot first; active delivery and unresolved queue jobs block activation.')
        if before.get('bot_id')!=identity['id'] or before.get('username')!=identity['username']:
            raise OpsError('Prepared bot identity differs from the verified Telegram identity.')
        services=bot.running()
        if set(services)&{'worker','sender'}: raise OpsError('Existing Telegram processes must be disabled first.')
        registered=False; stopped=False
        try:
            bot.run('stop','caddy','api','scheduler');stopped=True
            bot.backup(Path(backup),services)
            base.mode('bot-disable')
            bot.run('config','--quiet')
            bot.run('up','-d','--no-build','--wait','--wait-timeout','180','api','scheduler','caddy')
            public_check(base.values['APP_ORIGIN'],False)
            # Old PHP webhook replacement is explicit in the activate operation; never discard pending updates.
            registered=True
            if client.call('setWebhook',{'url':target,'secret_token':values['TELEGRAM_WEBHOOK_SECRET'],
                'drop_pending_updates':False,'allowed_updates':['message','callback_query'],'max_connections':20}) is not True:
                raise OpsError('Webhook registration was not acknowledged.')
            after=webhook_info(client)
            if after['url']!=target or after.get('has_custom_certificate') is True:
                raise OpsError('Telegram webhook verification failed.')
            enabled=base.mode('bot-enable',{**identity,'allow_pending_updates':True})
            if enabled.get('enabled') is not True or enabled.get('paused') is not False:
                raise OpsError('Database activation was not acknowledged.')
            bot.run('up','-d','--no-build','worker','sender')
            active=bot.running()
            if not {'api','scheduler','caddy','worker','sender'}.issubset(active):
                raise OpsError('Not all Telegram services are running.')
            public_check(base.values['APP_ORIGIN'],True)
        except (Exception, KeyboardInterrupt):
            # Keep both delivery gates closed and preserve the incoming queue.
            try: stop_delivery()
            except OpsError: pass
            try: base.mode('bot-disable')
            except OpsError: pass
            if registered:
                try:
                    current=webhook_info(client)
                    if current['url']==target: client.call('deleteWebhook',{'drop_pending_updates':False})
                except OpsError: pass
            if stopped:
                try:
                    bot.run('stop','caddy','api','scheduler')
                    base.run('up','-d','--no-build','api','scheduler','caddy')
                except OpsError: pass
            raise OpsError('Activation failed: delivery was paused/stopped where reachable. Pending updates were retained. Old PHP webhook secrets cannot be restored automatically; inspect status before retrying.') from None
        print('Telegram activated. Pending updates retained; webhook verified and delivery services started.')

def disable(web_env,bot_env,prebuilt=False):
    stop_delivery()
    base=Stack(web_env,prebuilt=prebuilt);bot=Stack(web_env,bot_env,prebuilt)
    with operation_lock(web_env):
        base.mode('bot-disable')
        bot.run('stop','worker','sender')
        values=load_bot(bot_env);client=Telegram(values['TELEGRAM_BOT_TOKEN'])
        verified_identity(client,values)
        target=base.values['APP_ORIGIN']+'/api/v1/telegram/webhook'
        info=webhook_info(client)
        if info['url']==target: client.call('deleteWebhook',{'drop_pending_updates':False})
        bot.run('stop','caddy','api','scheduler')
        base.run('up','-d','--no-build','--wait','--wait-timeout','180','api','scheduler','caddy')
        print('Telegram disabled. Jobs and pending updates preserved; web-only services restored.')

def backup_live(web_env,bot_env,destination,prebuilt=False):
    stack=Stack(web_env,bot_env,prebuilt)
    with operation_lock(web_env):
        services=stack.running()
        if 'db' not in services: raise OpsError('The database must be running before backup.')
        writers=[name for name in ('worker','sender','caddy','api','scheduler') if name in services]
        try:
            if writers: stack.run('stop',*writers)
            stack.backup(destination,services)
        finally:
            if writers: stack.run('start',*writers)
        print('Private consistent backup completed; previous services restarted. Restore must keep delivery paused until reviewed.')

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--env-file',type=Path,default=Path('/etc/unibot-web/web.env'))
    p.add_argument('--bot-env',type=Path,default=Path('/etc/unibot-web/bot.env'))
    p.add_argument('--prebuilt',action='store_true')
    actions=p.add_subparsers(dest='action',required=True)
    prep=actions.add_parser('prepare');prep.add_argument('--owner-id');prep.add_argument('--admin-id',action='append',default=[]);prep.add_argument('--expected-bot')
    a=actions.add_parser('activate');a.add_argument('--backup-directory',type=Path,required=True)
    b=actions.add_parser('backup');b.add_argument('--backup-directory',type=Path,required=True)
    actions.add_parser('status');actions.add_parser('disable')
    a=p.parse_args()
    if a.action=='prepare': prepare(a.env_file,a.bot_env,a.prebuilt,a.owner_id,a.admin_id,a.expected_bot)
    elif a.action=='activate': activate(a.env_file,a.bot_env,a.backup_directory,a.prebuilt)
    elif a.action=='disable': disable(a.env_file,a.bot_env,a.prebuilt)
    elif a.action=='backup': backup_live(a.env_file,a.bot_env,a.backup_directory,a.prebuilt)
    else:
        base=Stack(a.env_file,prebuilt=a.prebuilt);print(json.dumps(base.mode('bot-status'),ensure_ascii=False))
        if a.bot_env.exists():
            v=load_bot(a.bot_env);c=Telegram(v['TELEGRAM_BOT_TOKEN']);verified_identity(c,v)
            report_webhook(webhook_info(c),base.values['APP_ORIGIN']+'/api/v1/telegram/webhook')

if __name__=='__main__':
    try: main()
    except (Exception,KeyboardInterrupt) as error: sys.exit(cli_error(error))

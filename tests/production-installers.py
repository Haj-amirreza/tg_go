#!/usr/bin/env python3
"""Workflow regressions: no Docker, packages, services or Telegram calls are real."""
import contextlib,importlib.util,io,json,os,sys,tempfile,unittest
from pathlib import Path
from unittest import mock
D=Path(__file__).resolve().parents[1]/'tools/deploy';sys.path.insert(0,str(D))
import production_ops as ops

def load(name):
 s=importlib.util.spec_from_file_location(name,D/(name+'.py'));m=importlib.util.module_from_spec(s);s.loader.exec_module(m);return m
Data=load('install-data');Bot=load('enable-telegram');Proxy=load('replace-web-proxy')
META={'id':123456789,'username':'university_test_bot'}

class Workflows(unittest.TestCase):
 def setUp(self):
  t=tempfile.TemporaryDirectory();self.addCleanup(t.cleanup);self.root=Path(t.name);self.web=self.root/'web.env';self.bot=self.root/'bot.env'
  self.values=dict(APP_DOMAIN='panel.example.org',APP_ORIGIN='https://panel.example.org',APP_IMAGE='unibot-next:web-old',POSTGRES_PASSWORD='1'*48,APP_DB_PASSWORD='2'*48,APP_KEY='3'*64)
  self.web.write_text(''.join(k+'='+v+'\n' for k,v in self.values.items()));self.web.chmod(0o600)
  self.botvalues=dict(TELEGRAM_BOT_TOKEN='123456789:'+'A'*35,TELEGRAM_WEBHOOK_SECRET='0123456789abcdef'*4,TELEGRAM_BOT_USERNAME=META['username']);ops.write_bot(self.bot,self.botvalues)
  stopper=mock.patch.object(Bot,'stop_delivery',side_effect=lambda:self.calls.append(('emergency-stop',)))
  stopper.start();self.addCleanup(stopper.stop)
  self.calls=[];self.services=['db','api','scheduler','caddy'];self.url='https://legacy.example.org/private-secret-path';self.fail='';self.ready=True;self.owner=self
  self.state={**META,'bot_id':META['id'],'ready':True,'enabled':False,'paused':True,'import_ready':True,'admin_ready':True,'web_admin_ready':True}
  owner=self
  class Stack:
   def __init__(s,web_env,bot_env=None,prebuilt=False):s.values=ops.load_web(web_env)
   def running(s):return list(owner.services)
   def run(s,*a,**kw):
    owner.calls.append(('run',a))
    if a[:1]==('exec',):return '"off"\n'
    if a[:1]==('build',) and owner.fail=='build':raise ops.OpsError('build failed')
    if a[:3]==('up','-d','--no-build'):
     for v in a:
      if v in ('worker','sender','api','scheduler','caddy') and v not in owner.services:owner.services.append(v)
    if a[:1]==('stop',):owner.services[:]=[v for v in owner.services if v not in a[1:]]
   def backup(s,*a):owner.calls.append(('backup',a))
   def mode(s,name,body=None):
    owner.calls.append(('mode',name,body))
    if name=='bot-status':return dict(owner.state)
    if name=='bot-disable':owner.state.update(enabled=False,paused=True);return {'disabled':True}
    if name=='bot-enable':
     if owner.fail=='enable':raise ops.OpsError('enable failed')
     owner.state.update(enabled=True,paused=False);return dict(owner.state)
    if name=='import-bundled':return dict(books=7991,contents=670)
    return dict(owner.state)
  self.Stack=Stack
  class Telegram:
   def __init__(s,token):pass
   def call(s,method,body=None):
    owner.calls.append(('telegram',method,body))
    if method=='getMe':return {**META,'is_bot':True}
    if method=='getWebhookInfo':return dict(url=owner.url,pending_update_count=2)
    if method=='setWebhook':owner.url=body['url'];return True
    if method=='deleteWebhook':owner.url='';return True
  self.Telegram=Telegram
 def patches(self,module):
  return mock.patch.object(module,'Stack',self.Stack)
 def test_import_backs_up_and_upgrades_only_image_before_atomic_mode(self):
  with self.patches(Data),mock.patch.object(Data,'release_image',return_value='unibot-next:web-new'):
   Data.install(self.web,self.root/'backup')
  kinds=[x[0] for x in self.calls];self.assertLess(kinds.index('backup'),next(i for i,x in enumerate(self.calls) if x[:2]==('mode','import-bundled')))
  after=ops.load_web(self.web);self.assertEqual(after['APP_IMAGE'],'unibot-next:web-new')
  for key in ops.WEB_KEYS-{'APP_IMAGE'}:self.assertEqual(after[key],self.values[key])
  self.assertTrue(any(x[0]=='mode' and x[1]=='bot-disable' for x in self.calls))
 def test_failed_build_restores_original_config_and_services_without_import(self):
  original=self.web.read_bytes();self.fail='build'
  with self.patches(Data),mock.patch.object(Data,'release_image',return_value='unibot-next:web-new'),self.assertRaises(ops.OpsError):Data.install(self.web,self.root/'backup')
  self.assertTrue(self.web.read_bytes()==original)
  self.assertFalse(any(x[:2]==('mode','import-bundled') for x in self.calls))
  self.assertTrue(any(x[0]=='run' and x[1][:1]==('start',) for x in self.calls))
 def test_live_workers_block_data_import_before_backup(self):
  self.services.append('sender')
  with self.patches(Data),self.assertRaises(ops.OpsError):Data.install(self.web,self.root/'backup')
  self.assertFalse(any(x[0]=='backup' for x in self.calls))
 def test_activation_preserves_updates_and_starts_sender_after_registration(self):
  with self.patches(Bot),mock.patch.object(Bot,'Telegram',self.Telegram),mock.patch.object(Bot,'public_check'):
   Bot.activate(self.web,self.bot,self.root/'backup')
  registration=next(i for i,x in enumerate(self.calls) if x[:2]==('telegram','setWebhook'))
  enable=next(i for i,x in enumerate(self.calls) if x[:2]==('mode','bot-enable'))
  sender=next(i for i,x in enumerate(self.calls) if x[0]=='run' and 'sender' in x[1] and x[1][0]=='up')
  self.assertLess(registration,enable);self.assertLess(enable,sender)
  for x in self.calls:
   if x[:2]==('telegram','setWebhook'):self.assertIs(x[2]['drop_pending_updates'],False)
 def test_activation_failure_pauses_stops_and_deletes_only_new_webhook(self):
  self.fail='enable'
  with self.patches(Bot),mock.patch.object(Bot,'Telegram',self.Telegram),mock.patch.object(Bot,'public_check'),self.assertRaises(ops.OpsError):Bot.activate(self.web,self.bot,self.root/'backup')
  self.assertFalse(self.state['enabled']);self.assertTrue(self.state['paused']);self.assertEqual(self.url,'')
  deletion=[x for x in self.calls if x[:2]==('telegram','deleteWebhook')];self.assertTrue(deletion);self.assertIs(deletion[0][2]['drop_pending_updates'],False)
  self.assertTrue(any(x[0]=='emergency-stop' for x in self.calls))
 def test_proxy_failure_restarts_previous_owner_without_killing_unknown_process(self):
  def run(args):
   self.calls.append(args)
   if args[:2]==['ss','-H']:return 'LISTEN users:(("nginx",pid=123,fd=1))'
   if args[:2]==['systemctl','stop']:return ''
   return ''
  with mock.patch.object(Proxy,'run',side_effect=run),mock.patch.object(Proxy.Path,'read_text',return_value='0::/system.slice/nginx.service\n'),mock.patch.object(Proxy.subprocess,'run',return_value=mock.Mock(returncode=1)),self.assertRaises(RuntimeError):Proxy.replace(['new-proxy'])
  self.assertIn(['systemctl','stop','nginx.service'],self.calls);self.assertIn(['systemctl','start','nginx.service'],self.calls)
 def test_successful_proxy_replacement_disables_previous_autostart(self):
  def run(args):
   self.calls.append(args)
   return 'LISTEN users:(("nginx",pid=123,fd=1))' if args[:2]==['ss','-H'] else ''
  with mock.patch.object(Proxy,'run',side_effect=run),mock.patch.object(Proxy.Path,'read_text',return_value='0::/system.slice/nginx.service\n'),mock.patch.object(Proxy.subprocess,'run',return_value=mock.Mock(returncode=0)):
   Proxy.replace(['new-proxy'])
  self.assertIn(['systemctl','disable','nginx.service'],self.calls)
  self.assertNotIn(['systemctl','start','nginx.service'],self.calls)
 def test_live_backup_stops_delivery_before_dump_and_restarts_previous_services(self):
  self.services+=['worker','sender']
  with self.patches(Bot):Bot.backup_live(self.web,self.bot,self.root/'backup')
  backup=next(i for i,c in enumerate(self.calls) if c[0]=='backup')
  self.assertEqual(self.calls[0][1][:3],('stop','worker','sender'))
  self.assertEqual(self.calls[backup+1][1][0],'start')
 def test_two_explicit_administrators_and_bot_identity_are_verified(self):
  self.state['admin_ready']=False
  with self.patches(Bot),mock.patch.object(Bot,'Telegram',self.Telegram),mock.patch.object(Bot,'input',return_value='operator'):
   Bot.prepare(self.web,self.bot,owner_id='7777777',admin_ids=['7777778'],expected_bot=META['username'])
  links=[c for c in self.calls if c[:2]==('mode','admin-link')]
  self.assertEqual(links[0][2]['telegram_id'],'7777777')
  extra=[c[2]['telegram_id'] for c in self.calls if c[:2]==('mode','admin-add-telegram')]
  self.assertEqual(extra,['7777777','7777778'])
 def test_wrong_bot_identity_is_rejected_before_admin_changes(self):
  with self.patches(Bot),mock.patch.object(Bot,'Telegram',self.Telegram),self.assertRaises(ops.OpsError):
   Bot.prepare(self.web,self.bot,expected_bot='another_test_bot')
  self.assertFalse(any(c[0]=='mode' for c in self.calls))
 def test_unknown_listener_is_not_stopped(self):
  def run(args):return 'LISTEN users:(("other",pid=123,fd=1))' if args[:2]==['ss','-H'] else ''
  with mock.patch.object(Proxy,'run',side_effect=run) as r,mock.patch.object(Proxy.Path,'read_text',return_value='0::/user.slice/unknown\n'),mock.patch.object(Proxy.subprocess,'run') as start,self.assertRaises(RuntimeError):Proxy.replace(['new-proxy'])
  start.assert_not_called();self.assertFalse(any(c.args[0][:2]==['systemctl','stop'] for c in r.call_args_list))

if __name__=='__main__':unittest.main()

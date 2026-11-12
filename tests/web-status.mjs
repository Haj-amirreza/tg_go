import assert from 'node:assert/strict';
import test from 'node:test';
import { getSystemStatus } from '../frontend/admin/src/lib/api.ts';

test('production status supports active and inactive bots; malformed status stays rejected', async () => {
  const original = globalThis.fetch;
  const base = {service:'university-bot',stage:4,environment:'production',databaseReady:true,authenticationReady:true,botConnected:true};
  try {
    for (const environment of ['production','development','test']) {
      for (const botConnected of [true,false]) {
        const expected = {...base,environment,botConnected};
        globalThis.fetch = async () => Response.json(expected);
        assert.deepEqual(await getSystemStatus(),expected);
      }
    }
    for (const changes of [{environment:'invalid'},{botConnected:'true'},{databaseReady:1},{authenticationReady:null},{service:'unknown'}]) {
      globalThis.fetch = async () => Response.json({...base,...changes});
      await assert.rejects(getSystemStatus(),/invalid_status/);
    }
  } finally {globalThis.fetch=original;}
});

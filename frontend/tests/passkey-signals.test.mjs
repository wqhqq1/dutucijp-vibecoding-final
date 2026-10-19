import test from 'node:test';
import assert from 'node:assert/strict';
import {signalRemovedPasskeys} from '../src/passkey-signals.ts';

test('deletion signals only the revoked credentials and deduplicates IDs',async()=>{
 const calls=[];
 const result=await signalRemovedPasskeys(['revoked-a','revoked-b','revoked-a'],{async signalUnknownCredential(options){calls.push(options)}});
 assert.deepEqual(calls,[{rpId:'localhost',credentialId:'revoked-a'},{rpId:'localhost',credentialId:'revoked-b'}]);
 assert.match(result,/2 条/);assert.match(result,/由设备决定/);
});
test('custom domain cleanup uses its own RP instead of localhost',async()=>{
 const calls=[];
 await signalRemovedPasskeys(['new-domain-key'],{async signalUnknownCredential(value){calls.push(value)}},2000,'recon.example.com');
 assert.deepEqual(calls,[{rpId:'recon.example.com',credentialId:'new-domain-key'}]);
});
test('unsupported providers and accounts without passkeys give manual cleanup guidance',async()=>{
 assert.match(await signalRemovedPasskeys(['revoked-a'],{}),/不支持/);
 assert.match(await signalRemovedPasskeys([],{}),/没有已登记/);
});
test('a rejected or synchronously throwing provider never reports account deletion failed',async()=>{
 const rejected=await signalRemovedPasskeys(['revoked-a'],{async signalUnknownCredential(){throw new Error('test provider rejects')}});
 assert.match(rejected,/账户已删除/);assert.match(rejected,/手动清理/);
 const thrown=await signalRemovedPasskeys(['revoked-a'],{signalUnknownCredential(){throw new Error('test provider throws')}});
 assert.match(thrown,/账户已删除/);
});
test('partial success and a stalled provider remain bounded and accurately reported',async()=>{
 const result=await signalRemovedPasskeys(['accepted','stalled'],{signalUnknownCredential({credentialId}){return credentialId==='accepted'?Promise.resolve():new Promise(()=>{})}},5);
 assert.match(result,/1 条/);assert.match(result,/部分通知未完成/);
 const timeout=await signalRemovedPasskeys(['stalled'],{signalUnknownCredential(){return new Promise(()=>{})}},5);
 assert.match(timeout,/暂未响应/);assert.match(timeout,/账户已删除/);
});

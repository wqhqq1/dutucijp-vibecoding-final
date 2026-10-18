import {spawn} from 'node:child_process';
import fs from 'node:fs'; import path from 'node:path'; import os from 'node:os';
export async function start(base,outDir){
  fs.mkdirSync(outDir,{recursive:true});
  const chrome=path.join(process.env.ProgramFiles,'Google','Chrome','Application','chrome.exe');
  const profile=fs.mkdtempSync(path.join(os.tmpdir(),'recon-shot-'));
  const proc=spawn(chrome,['--headless=new','--disable-gpu','--no-first-run','--no-default-browser-check','--remote-debugging-pipe',`--user-data-dir=${profile}`,'--lang=zh-CN','about:blank'],{stdio:['ignore','ignore','ignore','pipe','pipe']});
  const sleep=ms=>new Promise(r=>setTimeout(r,ms));
  let seq=0;const pending=new Map();let buffer='';
  proc.stdio[4].setEncoding('utf8');proc.stdio[4].on('data',c=>{buffer+=c;let i;while((i=buffer.indexOf('\0'))>=0){const m=JSON.parse(buffer.slice(0,i));buffer=buffer.slice(i+1);if(m.id&&pending.has(m.id)){const {resolve,reject}=pending.get(m.id);pending.delete(m.id);m.error?reject(new Error(m.error.message)):resolve(m.result)}}});
  const send=(method,params={},sessionId)=>new Promise((resolve,reject)=>{const id=++seq;pending.set(id,{resolve,reject});proc.stdio[3].write(JSON.stringify({id,method,params,sessionId})+'\0')});
  const {targetId}=await send('Target.createTarget',{url:'about:blank'});
  const {sessionId}=await send('Target.attachToTarget',{targetId,flatten:true});
  const s=(m,p)=>send(m,p,sessionId);
  await s('Page.enable');await s('Runtime.enable');
  await s('Emulation.setDeviceMetricsOverride',{width:1440,height:900,deviceScaleFactor:1,mobile:false});
  const ev=async e=>{const r=await s('Runtime.evaluate',{expression:e,awaitPromise:true,returnByValue:true});if(r.exceptionDetails)throw new Error(e.slice(0,80)+' -> '+(r.exceptionDetails.exception?.description||r.exceptionDetails.text));return r.result.value};
  const waitFor=async(e,t=8000)=>{const t0=Date.now();while(Date.now()-t0<t){if(await ev(`!!(${e})`))return;await sleep(100)}throw new Error('timeout: '+e)};
  const open=async v=>{await s('Page.navigate',{url:`${base}/__mock/view?v=${v}`});await sleep(500);await waitFor(`document.querySelector('#root')?.children.length`);await sleep(700)};
  const shot=async name=>{const r=await s('Page.captureScreenshot',{format:'png'});fs.writeFileSync(path.join(outDir,name+'.png'),Buffer.from(r.data,'base64'));console.log('shot',name)};
  const close=()=>{proc.kill()};
  return {ev,waitFor,open,shot,sleep,close};
}

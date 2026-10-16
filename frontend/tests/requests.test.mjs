import assert from 'node:assert/strict';
import test from 'node:test';
import {startResourceRequest} from '../src/requests.ts';

function deferred(){
  let resolve,reject;
  const promise=new Promise((yes,no)=>{resolve=yes;reject=no});
  return {promise,resolve,reject};
}
const settle=()=>new Promise(resolve=>setImmediate(resolve));

test('the first server page is available while the overview is still pending',async()=>{
  const summary=deferred(),page=deferred(),summaries=[],pages=[];
  const stopSummary=startResourceRequest(()=>summary.promise,value=>summaries.push(value));
  const stopPage=startResourceRequest(()=>page.promise,value=>pages.push(value));
  page.resolve({items:[{id:'current-page'}],total:1038818,page:1,page_size:50});
  await settle();
  assert.deepEqual(pages.at(-1),{loading:false,value:{items:[{id:'current-page'}],total:1038818,page:1,page_size:50}});
  assert.deepEqual(summaries,[{loading:true}]);
  summary.resolve({cases:{total:42}});
  await settle();
  assert.deepEqual(summaries.at(-1),{loading:false,value:{cases:{total:42}}});
  stopPage();stopSummary();
});

test('changing query cancels the old request and discards a late response',async()=>{
  const old=deferred(),current=deferred(),values=[];
  let oldSignal;
  const cancelOld=startResourceRequest(signal=>{oldSignal=signal;return old.promise},value=>values.push(value));
  cancelOld();
  assert.equal(oldSignal.aborted,true);
  const cancelCurrent=startResourceRequest(()=>current.promise,value=>values.push(value));
  current.resolve('new filter result');
  await settle();
  old.resolve('old unfiltered result');
  await settle();
  assert.deepEqual(values,[{loading:true},{loading:true},{loading:false,value:'new filter result'}]);
  cancelCurrent();
});

test('logout cancellation suppresses both pending data and pending failures',async()=>{
  const data=deferred(),audit=deferred(),values=[];
  const stopData=startResourceRequest(()=>data.promise,value=>values.push(value));
  const stopAudit=startResourceRequest(()=>audit.promise,value=>values.push(value));
  stopData();stopAudit();
  data.resolve({account:'previous account'});
  audit.reject(new Error('previous account request failed'));
  await settle();
  assert.deepEqual(values,[{loading:true},{loading:true}]);
});

test('overview failure does not discard a successful table response',async()=>{
  const summaries=[],pages=[];
  startResourceRequest(async()=>{throw new Error('summary unavailable')},value=>summaries.push(value));
  startResourceRequest(async()=>({items:[{id:'visible'}],total:1}),value=>pages.push(value));
  await settle();
  assert.deepEqual(summaries.at(-1),{loading:false,error:'summary unavailable'});
  assert.deepEqual(pages.at(-1),{loading:false,value:{items:[{id:'visible'}],total:1}});
});

test('cancelling transport abort errors never produces a failure notice',async()=>{
  const values=[];
  const cancel=startResourceRequest(signal=>new Promise((resolve,reject)=>{
    signal.addEventListener('abort',()=>reject(new DOMException('Aborted','AbortError')),{once:true});
  }),value=>values.push(value));
  cancel();
  await settle();
  assert.deepEqual(values,[{loading:true}]);
});

test('synchronous request failure leaves a useful terminal state',()=>{
  const values=[];
  startResourceRequest(()=>{throw new Error('request setup failed')},value=>values.push(value));
  assert.deepEqual(values,[{loading:true},{loading:false,error:'request setup failed'}]);
});

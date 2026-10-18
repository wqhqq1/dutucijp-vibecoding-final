import {start} from './lib.mjs';
const b=await start('http://127.0.0.1:4174','./out');
const {ev,waitFor,open,shot,sleep}=b;
const call=(f,...a)=>ev('('+f.toString()+')('+a.map(x=>JSON.stringify(x)).join(',')+')');
const setStatus=v=>call(v=>{const s=[...document.querySelectorAll('label')].find(l=>l.textContent.trim().startsWith('状态')).querySelector('select');Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set.call(s,v);s.dispatchEvent(new Event('change',{bubbles:true}));[...document.querySelectorAll('button')].find(e=>e.textContent.trim()==='查询').click()},v);
const openRow=id=>call(id=>{[...document.querySelectorAll('.data-table tbody tr')].find(r=>r.innerText.includes(id)).querySelector('button').click()},id);
const btn=t=>call(t=>{const b=[...document.querySelectorAll('.drawer button')].find(e=>e.textContent.trim()===t);if(!b)throw new Error('no '+t);b.click()},t);
const scrollTo=t=>call(t=>{const el=[...document.querySelectorAll('.drawer h3,.drawer h4,.drawer strong,.drawer div')].find(e=>e.textContent.trim().startsWith(t));const sc=document.querySelector('.drawer-body');sc.scrollTop=el.getBoundingClientRect().top-sc.getBoundingClientRect().top+sc.scrollTop-20},t);
const top=()=>call(()=>{document.querySelector('.drawer-body').scrollTop=0});
const head=()=>call(()=>document.querySelector('.drawer').innerText.slice(0,260).replace(/\s+/g,' '));
try{
 await open('finance');await waitFor(`document.querySelector('.data-table tbody tr')`);
 await setStatus('pending');await sleep(1200);
 await openRow('mock-s0-r000443');await waitFor(`document.querySelector('.drawer')`);await sleep(1200);
 await scrollTo('销售候选对比');await sleep(500);await shot('G2-多候选对比-未自动');
 await top();await btn('派单');await sleep(1500);await shot('H1-派单成功');console.log('H1',await head());
 try{await btn('再次派单');await sleep(1500);await top();await shot('H2-再次派单被拒');console.log('H2',await head());}catch(e){console.log('H2 err',e.message)}
 await open('warehouse');await waitFor(`document.querySelector('.data-table tbody tr')`);
 await setStatus('in_progress');await sleep(1200);
 await openRow('mock-s0-r000254');await waitFor(`document.querySelector('.drawer')`);await sleep(1200);
 await call(()=>{const t=document.querySelector('.drawer textarea');Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(t,'   ');t.dispatchEvent(new Event('input',{bubbles:true}))});
 await btn('保存结论并关闭');await sleep(1500);await top();await sleep(300);await shot('I-空结论被拒');console.log('I',await head());
}catch(e){console.error(e)}finally{b.close()}

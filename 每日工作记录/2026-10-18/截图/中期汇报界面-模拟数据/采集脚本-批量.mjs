import {start} from './lib.mjs';
const b=await start('http://127.0.0.1:4174','./out');
const {ev,waitFor,open,shot,sleep}=b;
const call=(f,...a)=>ev('('+f.toString()+')('+a.map(x=>JSON.stringify(x)).join(',')+')');
const click=t=>call(t=>{const e=[...document.querySelectorAll('button')].filter(e=>e.textContent.trim().startsWith(t)&&!e.disabled);if(!e.length)throw new Error('no '+t);e[0].click();return e[0].textContent.trim()},t);
const dlg=()=>call(()=>{const d=document.querySelector('dialog[open]')||document.querySelector('[role=dialog]');return d?d.innerText.replace(/\s+/g,' ').slice(0,600):'none'});
try{
 await open('finance');await waitFor(`document.querySelector('.data-table tbody tr')`);
 await click('打开批量工作台');await sleep(2000);
 await click('选择当前筛选全部');await sleep(800);
 await call(()=>{const t=[...document.querySelectorAll('textarea')].find(t=>t.placeholder.includes('复核依据'));Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(t,'模拟演示：已逐项核对推荐依据。');t.dispatchEvent(new Event('input',{bubbles:true}))});await sleep(300);
 console.log(await click('预览所选'));await sleep(2500);
 await shot('K1-预览可提交');console.log('K1',await dlg());
 try{console.log(await click('将跳过'));await sleep(1200);await shot('K2-预览将跳过');console.log('K2',await dlg())}catch(e){console.log(e.message)}
 try{console.log(await click('确认提交'));await sleep(3000);await shot('K3-提交结果');console.log('K3',await dlg())}catch(e){console.log(e.message)}
}catch(e){console.error(e)}finally{b.close()}

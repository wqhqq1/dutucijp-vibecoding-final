// Development-only design preview. Runs only with `--mode mock`; it is never
// imported by src/ and never reaches dist. Every record here is generated sample
// data, clearly labelled on screen, and nothing talks to the real :8080 service.
import fs from 'node:fs';
import path from 'node:path';
import type {Connect,Plugin} from 'vite';
import type {IncomingMessage,ServerResponse} from 'node:http';
import {accountProfile,allows,handleAccounts,isAll,resetAccounts,setupAccounts,visibleUsers} from './mock-accounts.ts';

// Business views sign in as one sample account each: admin, an all-country
// finance account, a finance account limited to United Kingdom/France, and the
// United Kingdom warehouse.
type View='login'|'setup'|'setup-password'|'setup-factor'|'admin'|'finance'|'finance-uk'|'warehouse';
const views:View[]=['login','setup','setup-password','setup-factor','admin','finance','finance-uk','warehouse'];
const businessIDs:Partial<Record<View,string>>={admin:'admin',finance:'finance','finance-uk':'finance-uk',warehouse:'warehouse'};
// Same policy as backend/internal/web/server.go, so the built bundle is checked
// under the headers it will actually be served with.
const csp="default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'";
const badgeCSS='#mock-badge{position:fixed;left:12px;bottom:12px;z-index:99;background:#7a2e1f;color:#fff;font:600 12px/1.4 system-ui,sans-serif;padding:6px 12px;border-radius:999px;box-shadow:0 4px 14px #0003;pointer-events:none}';
const badge='<link rel="stylesheet" href="/__mock/badge.css"><div id="mock-badge">开发预览 · 模拟数据（非真实业务记录）</div>';

function rand(seed:number){let s=seed>>>0;return ()=>{s=(s*1664525+1013904223)>>>0;return s/2**32}}
const countries=['United Kingdom','France','Germany','EIRE','Netherlands','Spain','Belgium',''];
// Same codes as backend/internal/reconcile/engine.go (conservative-1.0.0).
const reasons=['ambiguous_candidates','missing_identity','insufficient_remaining_quantity','no_strictly_earlier_sale','suspected_duplicate_sale','no_identity_match','suspected_duplicate_cancel'];
const sheets=['Year 2009-2010','Year 2010-2011'];
const run={run_id:'mock-run-1',dataset_id:'mock-dataset',scope:'year1',cleaning_version:'cr-1.0.1',rule_version:'mock-preview'};
const runs=[run,{run_id:'mock-run-0',dataset_id:'mock-dataset-b',scope:'benchmark',cleaning_version:'cr-1.0.1',rule_version:'mock-preview'}];

function txn(i:number){
  const r=rand(i*7919+3),cancel=i%9===0,country=countries[Math.floor(r()*countries.length)];
  const day=new Date(Date.UTC(2009,11,1)+Math.floor(i/40)*86400000+Math.floor(r()*36000)*1000);
  return {id:`mock-s0-r${String(i+2).padStart(6,'0')}`,sheet:sheets[0],source_row:i+2,invoice:(cancel?'C':'')+String(489000+Math.floor(i/6)),
    stock:'M'+String(10000+Math.floor(r()*900)),description:'模拟商品 '+String(1+Math.floor(r()*240)).padStart(3,'0'),
    quantity:(cancel?-1:1)*(1+Math.floor(r()*24)),time:day.toISOString().slice(0,19),price:(0.29+Math.floor(r()*1200)/100).toFixed(2),
    customer:r()<.08?'':String(12000+Math.floor(r()*6000)),country,kind:cancel?'cancel':'sale'};
}
const TXN_TOTAL=1284;
function candidateFor(index:number,cancel:ReturnType<typeof txn>,offset:number){
  const source=txn(index*9+1+offset),remaining=Math.max(Math.abs(cancel.quantity),Math.abs(source.quantity))+(offset%2);
  return {...source,customer:cancel.customer,stock:cancel.stock,price:cancel.price,country:cancel.country,
    time:new Date(Date.parse(cancel.time+'Z')-(offset+1)*3600000).toISOString().slice(0,19),
    kind:'sale',quantity:remaining+(offset%3),original:remaining+(offset%3),allocated:offset%3,current_remaining:remaining};
}
type MockCase={id:string;run_id:string;cancel_id:string;reason:string;status:string;version:number;assignee:string;conclusion:string;source_row:number;sheet:string;waiting?:boolean;missing_materials?:string;next_step?:string};
const cases:MockCase[]=Array.from({length:137},(_,i)=>{
  const t=txn(i*9),status=i<23?'closed':i<41?'in_progress':'pending';
  return {id:'mock-case-'+(i+1),run_id:run.run_id,cancel_id:t.id,reason:reasons[i%reasons.length],status,version:status==='closed'?3:status==='in_progress'?2:i%3===0?1:0,
    assignee:status!=='pending'||i%3===0?'warehouse':'',conclusion:status==='closed'?'模拟结论：已核对来源行与候选，资料不足，保留原因后结案。':'',source_row:t.source_row,sheet:t.sheet};
});
const audits=new Map<string,any[]>();
const confirmations=new Map<string,any>();
function seedAudit(c:MockCase){
  if(audits.has(c.id))return audits.get(c.id)!;
  const list:any[]=[];let v=0;const push=(action:string,from:string,to:string,actor:string)=>{list.push({id:list.length+1,actor,action,occurred_at:`2026-10-1${5+list.length}T10:0${list.length}:00+08:00`,before:{status:from,version:v},after:{status:to,version:v+1}});v++};
  if(c.assignee)push('assign','pending','pending','finance');
  if(c.status!=='pending')push('start','pending','in_progress','warehouse');
  if(c.status==='closed')push('close','in_progress','closed','warehouse');
  audits.set(c.id,list);return list;
}
const allocations=Array.from({length:118},(_,i)=>{const c=txn(i*9),s=txn(i*9+1);return {run_id:run.run_id,cancel_id:c.id,sale_id:s.id,quantity:Math.abs(c.quantity),amount:(Math.abs(c.quantity)*Number(c.price)).toFixed(2),method:i%17===0?'manual':'automatic',actor:i%17===0?'warehouse':'',rule_version:run.rule_version,cancel_sheet:c.sheet,cancel_source_row:c.source_row,sale_sheet:s.sheet,sale_source_row:s.source_row}});
const jobs=new Map<string,{id:string;polls:number;scope:string}>();
const reviewSets=new Map<string,any>();
const reviewBatches=new Map<string,any>();
const stages=[['queued','queued'],['running','preparing'],['running','importing'],['running','reconciling'],['succeeded','published']];

function cookie(req:IncomingMessage,name:string){return new RegExp('(?:^|;\\s*)'+name+'=([^;]*)').exec(req.headers.cookie||'')?.[1]||''}
function viewOf(req:IncomingMessage):View{const v=cookie(req,'mock_view') as View;return views.includes(v)?v:'finance'}
// Test switches for loading states, kept in cookies so a reload keeps them:
// mock_fail lists resources that answer 503 (countries, accounts, requests,
// audit, users, write); mock_slow delays account routes by that many ms.
function flagsOf(req:IncomingMessage){return {fail:new Set(decodeURIComponent(cookie(req,'mock_fail')).split(',').filter(Boolean)),slow:Math.min(15000,Number(cookie(req,'mock_slow'))||0)}}
const flagCookies=(q:URLSearchParams)=>[`mock_fail=${encodeURIComponent(q.get('fail')||'')}; Path=/; SameSite=Strict`,`mock_slow=${Number(q.get('slow'))||0}; Path=/; SameSite=Strict`];
function send(res:ServerResponse,status:number,body:unknown,cookie?:View,extra:string[]=[]){res.statusCode=status;res.setHeader('Content-Type','application/json; charset=utf-8');if(cookie||extra.length)res.setHeader('Set-Cookie',[...(cookie?[`mock_view=${cookie}; Path=/; SameSite=Strict`]:[]),...extra]);res.end(JSON.stringify(body))}
function readBody(req:IncomingMessage){return new Promise<any>(resolve=>{let raw='';req.on('data',c=>raw+=c);req.on('end',()=>{try{resolve(raw?JSON.parse(raw):{})}catch{resolve({})}})})}
function page<T>(items:T[],q:URLSearchParams){const p=Math.max(1,Number(q.get('page'))||1),size=50;return {items:items.slice((p-1)*size,p*size),total:items.length,page:p,page_size:size}}
function sessionFor(v:View,setupID='admin'){
  const base={csrf:'mock-csrf',has_totp:false,has_passkey:false,permissions_version:1};
  const id=businessIDs[v];
  if(id){const p=accountProfile(id);if(!p)return null;return {...base,...p,state:'complete',scope:'business',has_passkey:true,can_manage_data_jobs:p.role==='admin'||(p.role==='finance'&&isAll(p))}}
  if(v==='setup-password'||v==='setup-factor'){const p=accountProfile(setupID)||{id:setupID,role:'admin',display_name:setupID,country_scope:'all' as const,countries:[],permissions_version:1};return {...base,...p,state:v==='setup-password'?'new':'password',scope:'setup',can_manage_data_jobs:false}}
  return null;
}
const refuse=(res:ServerResponse,what:string)=>send(res,422,{code:'mock_preview',message:`模拟预览不执行${what}；请在真实部署中由本人操作。`});

async function handle(req:IncomingMessage,res:ServerResponse,next:Connect.NextFunction){
  const url=new URL(req.url||'/','http://mock.local'),p=url.pathname,q=url.searchParams;
  if(p==='/__mock/badge.css'){res.setHeader('Content-Type','text/css');res.end(badgeCSS);return}
  if(p==='/__mock/view'){const v=views.includes(q.get('v') as View)?q.get('v') as View:'finance';res.statusCode=302;res.setHeader('Set-Cookie',[`mock_view=${v}; Path=/; SameSite=Strict`,...flagCookies(q)]);res.setHeader('Location','/'+(q.get('next')?'?'+q.get('next'):''));res.end();return}
  if(p==='/__mock/flags')return send(res,200,{fail:q.get('fail')||'',slow:Number(q.get('slow'))||0},undefined,flagCookies(q));
  if(p==='/__mock/reset'){resetAccounts();return send(res,200,{reset:true})}
  if(!p.startsWith('/api/'))return next();
  const v=viewOf(req),s=sessionFor(v,cookie(req,'mock_setup')||'admin'),api=p.slice(4),body=req.method==='POST'||req.method==='PATCH'||req.method==='PUT'?await readBody(req):{};
  const flags=flagsOf(req),accountRoute=/^\/(countries|users|admin\/|warehouse-requests)/.test(api);
  await new Promise(r=>setTimeout(r,(accountRoute&&flags.slow)||120+Math.random()*180));
  if(accountRoute){const key=req.method!=='GET'?'write':api.startsWith('/admin/account-audit')?'audit':api.startsWith('/admin/accounts')?'accounts':api.startsWith('/warehouse-requests')?'requests':api.slice(1);if(flags.fail.has(key))return send(res,503,{code:'mock_failure',message:`模拟故障：${({write:'保存',audit:'操作记录',accounts:'账户列表',requests:'申请列表',countries:'国家/地区列表',users:'责任人列表'} as Record<string,string>)[key]||key}暂时不可用（仅开发预览）`})}
  if(api==='/health')return send(res,200,{application:'returns-recon',runtime_id:'mock-preview'});
  if(api==='/auth/me')return s?send(res,200,s):send(res,401,{code:'unauthenticated',message:'未登录'});
  if(api==='/auth/setup/accounts')return send(res,200,v==='setup'?setupAccounts():[]);
  if(api==='/auth/setup/start'){if(!setupAccounts().some(a=>a.id===body.id))return send(res,409,{code:'conflict',message:'该账户已设置密码，请使用密码登录续办'});return send(res,200,{session:sessionFor('setup-password',body.id)},'setup-password',[`mock_setup=${encodeURIComponent(body.id)}; Path=/; SameSite=Strict`])}
  if(api==='/auth/logout')return send(res,200,{},'login');
  if(api.startsWith('/auth/'))return refuse(res,'认证、密码或认证因素操作');
  if(!s||s.scope!=='business')return send(res,401,{code:'unauthenticated',message:'未登录'});
  const finance=s.role!=='warehouse',visible=cases.filter(c=>(finance||c.assignee===s.id)&&allows(s,txn(cases.indexOf(c)*9).country));
  const openCases=(id:string)=>cases.filter(c=>c.assignee===id&&c.status!=='closed').map(c=>txn(cases.indexOf(c)*9).country);
  const deletionCases=(id:string,remove:boolean)=>{const open=cases.filter(c=>c.assignee===id&&c.status!=='closed'),closed_cases=cases.filter(c=>c.assignee===id&&c.status==='closed').length;if(remove)for(const c of open){const before={...c};c.status='pending';c.assignee='';c.conclusion='';c.version++;audits.get(c.id)?.push({id:(audits.get(c.id)?.length||0)+1,actor:s.id,action:'account_deleted_unassign',occurred_at:new Date().toISOString(),before,after:{...c}})}return {open_cases:open.length,closed_cases}};
  if(handleAccounts(api,req.method||'GET',body,s,(status,value)=>send(res,status,value),openCases,deletionCases))return;
  if(api==='/runs')return send(res,200,runs);
  if(api==='/users')return finance?send(res,200,visibleUsers(s)):send(res,403,{code:'forbidden',message:'需要财务或管理员权限'});
  // Sample UI fixtures only. Production transactions/authorization are verified
  // by the Go SQLite/HTTP tests; these routes never enter the production bundle.
  const reviewMatch=(it:any,f:any)=>['status','reason','assignee'].every(k=>!f[k]||it.case[k]===f[k])&&['customer','country','stock','invoice'].every(k=>!f[k]||it.cancel[k]===f[k]);
  const reviewKey=(it:any,by:string)=>by==='invoice'?it.cancel.invoice:by==='customer'?JSON.stringify([it.cancel.customer,it.cancel.country]):by==='reason'?it.case.reason:it.cancel.stock;
  if(api==='/review-sets'&&req.method==='POST'){
    const items=visible.map(c=>{const index=cases.indexOf(c),cancel=txn(index*9),sale=candidateFor(index,cancel,0),kind=['unique','duplicate','basket','nearest'][index%4],option:any={id:kind+'-'+c.id,kind,sale_id:sale.id,invoice:sale.invoice,time:sale.time,gap_seconds:3600,remaining:sale.current_remaining,warning:kind==='nearest'?'最近时间仅供推荐，未证明真实原单':kind==='basket'?'共同原单属于推断；与最近推荐不同':''};
      if(kind==='duplicate')option.duplicate={capacity:sale.quantity,allocated:0,remaining:sale.current_remaining,canonical_id:sale.id,members:[{sale_id:sale.id,sheet:sale.sheet,source_row:sale.source_row},{sale_id:sale.id+'-copy',sheet:sale.sheet,source_row:sale.source_row+1}]};
      if(kind==='basket'){option.basket_id='mock-basket-'+Math.floor(index/8);option.basket_cases=visible.filter(other=>{const j=cases.indexOf(other);return other.status!=='closed'&&j%4===2&&Math.floor(j/8)===Math.floor(index/8)}).map(other=>other.id)}
      const tied=index===135,none=index===136;
      const options=c.status==='closed'||none?[]:[option,...(kind==='basket'||tied?[{...option,id:'nearest-alternative-'+c.id,kind:'nearest',invoice:'模拟较近销售',sale_id:sale.id+'-newer',warning:tied?'最近时间并列，请明确选择候选':'最近时间仅供推荐'}]:[])];return {case:{...c},cancel,options,choice_id:tied?'':options[0]?.id||'',has_allocation:c.status==='closed',notice:tied?'最近时间并列，请明确选择候选':none?'无合格销售，保留核查':''}});
    const id='mock-review-'+(reviewSets.size+1),counts:Record<string,number>={total:items.length,recommended:0,tied:0};for(const it of items){if(it.choice_id){counts.recommended++;counts[it.options[0].kind]=(counts[it.options[0].kind]||0)+1}else if(it.options.length)counts.tied++;if(it.case.status==='closed')counts.closed=(counts.closed||0)+1}const set={id,actor:s.id,run_id:body.run_id,policy_version:'mock-review-only',counts,items};reviewSets.set(id,set);return send(res,200,set);
  }
  if(api==='/review-sets/latest'){const set=[...reviewSets.values()].filter(x=>x.actor===s.id&&x.run_id===q.get('run_id')).at(-1);return send(res,200,set||{id:''})}
  const reviewRoute=/^\/review-sets\/([^/]+)(\/groups)?$/.exec(api);
  if(reviewRoute){const set=reviewSets.get(reviewRoute[1]);if(!set||set.actor!==s.id)return send(res,404,{message:'模拟快照不存在'});const matched=set.items.filter((it:any)=>reviewMatch(it,Object.fromEntries(q))&&(q.get('recommended_only')!=='true'||it.options.length));let items=matched.filter((it:any)=>it.case.status!=='closed');if(reviewRoute[2]){const g=new Map<string,any>();for(const it of items){const key=reviewKey(it,q.get('group_by')||'invoice');if(!g.has(key))g.set(key,{key,label:key,count:0,recommended:0});g.get(key).count++;g.get(key).recommended+=!!it.choice_id}return send(res,200,page([...g.values()],q))}if(q.has('group_key'))items=items.filter((it:any)=>reviewKey(it,q.get('group_by')||'invoice')===q.get('group_key'));return send(res,200,{...page(items,q),set_id:set.id,counts:set.counts,policy_version:set.policy_version,readonly:matched.length-items.length})}
  if(api==='/case-batches/preview'){
    const set=reviewSets.get(body.set_id);if(!set||set.actor!==s.id)return send(res,404,{message:'模拟快照不存在'});let action=body.action,conclusion=body.conclusion,assignee=body.assignee;let ids=body.ids||[];if(body.retry_batch_id){const old=reviewBatches.get(body.retry_batch_id);ids=old.items.filter((it:any)=>it.status!=='succeeded').map((it:any)=>it.case_id);action=old.action;conclusion=old.conclusion;assignee=old.assignee}
    const chosen=set.items.filter((it:any)=>(body.all||ids.includes(it.case.id))&&!(body.exclude_ids||[]).includes(it.case.id)&&reviewMatch(it,body.filter||{})&&(!body.recommended_only||it.options.length)&&(!body.group_by||reviewKey(it,body.group_by)===body.group_key));
    const open=chosen.filter((it:any)=>it.case.status!=='closed'),readonly=chosen.length-open.length;
    if(!open.length)return send(res,422,{code:readonly?'closed':'invalid_input',message:readonly?'所选项目均已关闭，只读，不能批量修改':'请选择当前筛选内的项目'});
    const groupIDs=new Map<string,number>();
    const items:any[]=open.map((it:any)=>{const option=it.options.find((o:any)=>o.id===(body.choices?.[it.case.id]??it.choice_id));const key=option?.basket_id||it.case.id;if(!groupIDs.has(key))groupIDs.set(key,groupIDs.size+1);
      const blocked=action==='assign'?(it.case.assignee?'已有责任人：'+it.case.assignee:''):it.case.assignee&&it.case.assignee!==s.id?'已派给其他责任人':(action==='approve'||action==='confirm')&&!option?(it.options.length?'尚未明确选择配对方案（最近时间可能并列）':'没有合格推荐，请打开详情逐条核查'):'';
      return {case_id:it.case.id,cancel_id:it.case.cancel_id,invoice:it.cancel.invoice,stock:it.cancel.stock,version:it.case.version,option,status:blocked?'skipped':'ready',message:blocked,group_id:groupIDs.get(key)}});
    for(const it of items)it.group_size=items.filter(x=>x.group_id===it.group_id).length;
    const id='mock-batch-'+(reviewBatches.size+1),batch={id,set_id:set.id,actor:s.id,run_id:set.run_id,action,conclusion,assignee,status:'preview',readonly,groups:groupIDs.size,items};reviewBatches.set(id,batch);return send(res,200,mockBatchView(batch,q));
  }
  if(api==='/case-batches/latest'){const batch=[...reviewBatches.values()].filter(x=>x.actor===s.id&&x.run_id===q.get('run_id')).at(-1);return send(res,200,batch?mockBatchView(batch,q):{id:''})}
  const batchRoute=/^\/case-batches\/([^/]+)(\/commit)?$/.exec(api);
  if(batchRoute){const batch=reviewBatches.get(batchRoute[1]);if(!batch||batch.actor!==s.id)return send(res,404,{message:'模拟批次不存在'});if(batchRoute[2]){for(const it of batch.items){if(it.status!=='ready')continue;const c=visible.find(x=>x.id===it.case_id);if(!c||c.version!==it.version){it.status='skipped';it.message='版本已变化';continue}if(batch.action==='assign')c.assignee=batch.assignee;else if(batch.action==='start')c.status='in_progress';else if(batch.action==='close'||batch.action==='approve'){c.status='closed';c.conclusion=batch.conclusion;c.assignee||=s.id}c.version++;it.status='succeeded';it.message='模拟处理完成'}batch.status='completed'}return send(res,200,mockBatchView(batch,q))}
  if(api==='/overview'){const n=(st:string)=>visible.filter(c=>c.status===st).length;return send(res,200,{run_id:q.get('run_id'),caveat:'规则推断；没有真实原订单关联号。人工处理记录为课程模拟。（此处为模拟数据）',cases:{total:visible.length,pending:n('pending'),in_progress:n('in_progress'),closed:n('closed')},...(finance?{initial_reconciliation:{input:{raw:TXN_TOTAL+41,accepted:TXN_TOTAL,excluded:41},matched:118,suspected:96,no_original:41}}:{})})}
  if(api==='/cases'){const st=q.get('status'),why=q.get('reason'),waiting=q.get('waiting');if(why&&!reasons.includes(why))return send(res,422,{code:'invalid_input',message:'未知差异原因'});return send(res,200,page(visible.filter(c=>(!st||c.status===st)&&(!why||c.reason===why)&&(!waiting||(waiting==='1')===!!c.waiting)),q))}
  if(api==='/allocations')return send(res,200,page(allocations,q));
  if(api==='/transactions'){
    let rows=Array.from({length:TXN_TOTAL},(_,i)=>txn(i)).filter(r=>allows(s,r.country));
    for(const k of ['customer','stock','invoice','country'] as const){const f=q.get(k);if(f)rows=rows.filter(r=>r[k]===f)}
    const from=q.get('date_from'),to=q.get('date_to');if(from)rows=rows.filter(r=>r.time>=from);if(to)rows=rows.filter(r=>r.time<to);
    const sort=q.get('sort')||'source',dir=q.get('direction')==='desc'?-1:1,key=({date:'time',source:'source_row'} as Record<string,string>)[sort]||sort;
    rows.sort((a:any,b:any)=>(a[key]>b[key]?1:a[key]<b[key]?-1:0)*dir);return send(res,200,page(rows,q));
  }
  if(api==='/exports'){res.setHeader('Content-Type','text/csv; charset=utf-8');res.end('mock_preview,模拟数据\n');return}
  const m=/^\/cases\/([^/]+)\/(evidence|audit|assign|start|close|confirm-match|remediations)$/.exec(api);
  const c=m&&visible.find(x=>x.id===m[1]);
  if(m&&!c)return send(res,404,{code:'not_found',message:'差异不存在或无权查看'});
  if(m&&c){
    const index=cases.indexOf(c),cancel={...txn(index*9),id:c.cancel_id},list=seedAudit(c);
    if(c.reason!=='missing_identity'){cancel.customer||='15581';cancel.country||='United Kingdom'}
    if(m[2]==='evidence'){
      const none=['no_identity_match','no_strictly_earlier_sale','insufficient_remaining_quantity','missing_identity'].includes(c.reason),many=c.reason==='ambiguous_candidates'&&index%2===0,total=none?0:many?63:2+(index%3);
      const confirmation=confirmations.get(c.id);
      const original=Array.from({length:total},(_,i)=>candidateFor(index,cancel,i));
      const all=original.map(x=>x.id===confirmation?.allocation.sale_id?confirmation.sale:x).filter(x=>x.current_remaining>=Math.abs(cancel.quantity));
      const decision=none&&c.reason!=='missing_identity'?[0,1].map(i=>{const t=txn(index*9+1+i);return {...t,original:Math.abs(t.quantity),allocated:0,current_remaining:i,remaining_at_decision:i,eligible_at_decision:false,reason:c.reason==='no_strictly_earlier_sale'?'sale_not_strictly_earlier':'remaining_quantity_too_small'}}):original.slice(0,Math.min(2,total)).map((x,i)=>({...x,current_remaining:x.id===confirmation?.allocation.sale_id?confirmation.sale.current_remaining:x.current_remaining,remaining_at_decision:x.original,eligible_at_decision:true,reason:c.reason==='suspected_duplicate_sale'&&i===0?'suspected_duplicate_sale':'qualified'}));
      return send(res,200,{case:{generation:1,...c},cancel,original_cancel:cancel,effective_cancel:cancel,related_cases:[{generation:1,...c}],remediations:[],decision_evidence:decision,candidates:page(all,q),confirmation,caveat:'规则推断；没有真实原订单关联号。人工处理记录为课程模拟。（此处为模拟数据）'});
    }
    if(m[2]==='audit')return send(res,200,list);
    if(c.status==='closed')return send(res,409,{code:'closed',message:'已关闭差异不能修改'});
    if(m[2]!=='assign'&&c.assignee!==s.id)return send(res,403,{code:'forbidden',message:'仅当前责任人可以处理'});
    if(m[2]!=='assign'&&body.version!==c.version)return send(res,409,{code:'version_conflict',message:'差异版本已变化，请刷新'});
    if(m[2]==='assign'&&c.assignee)return send(res,409,{code:'already_assigned',message:'已有责任人：'+c.assignee});
    if(['close','confirm-match'].includes(m[2])&&c.status!=='in_progress')return send(res,409,{code:'invalid_transition',message:'开始处理后才能继续操作'});
    const before={status:c.status,version:c.version};
    if(m[2]==='remediations'){
      if(body.action!=='wait_materials')return refuse(res,'真实业务资料修正与重算（仅隔离Go/HTTP测试执行）');
      if(c.status!=='in_progress'||!body.evidence?.trim()||!body.missing_materials?.trim()||!body.next_step?.trim())return send(res,422,{message:'请开始处理并填写依据、缺少材料和下一步'});
      c.waiting=true;c.missing_materials=body.missing_materials;c.next_step=body.next_step;c.version++;
      return send(res,200,{case:c,message:'模拟数据：已保存待补充信息，未执行真实业务写入。'});
    }
    if(m[2]==='assign'){c.assignee=body.assignee||'warehouse'}
    else if(m[2]==='start'){c.status='in_progress'}
    else if(m[2]==='close'){if(!confirmations.has(c.id))return send(res,409,{code:'unresolved',message:'未解决问题须补充资料或保存待补充信息'});if(!String(body.conclusion||'').trim())return send(res,422,{code:'invalid_input',message:'请填写核查结论'});c.status='closed';c.conclusion=body.conclusion}
    else if(m[2]==='confirm-match'){
      if(confirmations.has(c.id))return send(res,409,{code:'already_allocated',message:'该取消行已经分配'});
      const saleIndex=Number(String(body.sale_row_id).split('-r')[1])-2;
      const source=candidateFor(index,cancel,saleIndex-index*9-1),quantity=Math.abs(cancel.quantity),allocation={sale_id:body.sale_row_id,quantity,amount:(quantity*Number(cancel.price)).toFixed(2),actor:s.id};
      const sale={...source,allocated:source.allocated+quantity,current_remaining:source.current_remaining-quantity};
      confirmations.set(c.id,{allocation,sale});
      list.push({id:list.length+1,actor:s.id,action:'confirm_candidate',occurred_at:new Date().toISOString(),before,after:{status:c.status,version:c.version+1},after_evidence:{manual_confirmation:{sale_id:body.sale_row_id,quantity}}});c.version++;return send(res,200,{case:c,allocation});
    }
    c.version++;list.push({id:list.length+1,actor:s.id,action:m[2],occurred_at:new Date().toISOString(),before,after:{status:c.status,version:c.version}});
    return send(res,200,c);
  }
  if(s.can_manage_data_jobs&&api==='/jobs'&&req.method==='POST'){const id='mock-job-'+(jobs.size+1);jobs.set(id,{id,polls:0,scope:body.scope});return send(res,200,{id,status:'queued',stage:'queued',progress:{}})}
  const j=/^\/jobs\/([^/]+)$/.exec(api);
  if(s.can_manage_data_jobs&&j){const job=jobs.get(j[1]);if(!job)return send(res,404,{code:'not_found',message:'任务不存在'});const [status,stage]=stages[Math.min(job.polls++,stages.length-1)];const n=Math.min(job.polls,4);return send(res,200,{id:job.id,status,stage,run_id:run.run_id,progress:{input:{rows_read:n*321,accepted:n*310,excluded:n*11},matched:n*29,suspected:n*24}})}
  return send(res,404,{code:'not_found',message:'模拟预览没有此接口'});
}

// Mirrors backend web.batchView: one result status per page, reasons summed.
function mockBatchView(batch:any,q:URLSearchParams){
  const count=(s:string)=>batch.items.filter((it:any)=>it.status===s).length;
  const order=batch.status==='preview'?['ready','skipped','failed']:['succeeded','ready','failed','skipped'];
  const view=q.get('status')||order.find(s=>count(s)>0)||'';
  const rows=batch.items.filter((it:any)=>!view||it.status===view),reasons=new Map<string,any>();
  for(const it of batch.items)if(it.status==='skipped'||it.status==='failed'){if(!reasons.has(it.message))reasons.set(it.message,{code:'mock',message:it.message,count:0});reasons.get(it.message).count++}
  return {...batch,...page(rows,q),total:batch.items.length,view,view_total:rows.length,reasons:[...reasons.values()],ready:count('ready'),succeeded:count('succeeded'),skipped:count('skipped'),failed:count('failed')}
}

export function mockApi():Plugin{
  return {
    name:'recon-mock-api',
    configureServer(server){server.middlewares.use(handle)},
    transformIndexHtml:html=>html.replace('<body>','<body>'+badge),
    configurePreviewServer(server){
      const indexFile=path.resolve(server.config.root,server.config.build.outDir,'index.html');
      server.middlewares.use((req,res,next)=>{
        res.setHeader('Content-Security-Policy',csp);
        const p=(req.url||'/').split('?')[0];
        if(req.method==='GET'&&(p==='/'||p==='/index.html')){res.setHeader('Content-Type','text/html; charset=utf-8');res.end(fs.readFileSync(indexFile,'utf8').replace('<body>','<body>'+badge));return}
        handle(req,res,next);
      });
    },
  };
}

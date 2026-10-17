// Development-only design preview. Runs only with `--mode mock`; it is never
// imported by src/ and never reaches dist. Every record here is generated sample
// data, clearly labelled on screen, and nothing talks to the real :8080 service.
import fs from 'node:fs';
import path from 'node:path';
import type {Connect,Plugin} from 'vite';
import type {IncomingMessage,ServerResponse} from 'node:http';

type View='login'|'setup'|'setup-password'|'setup-factor'|'finance'|'warehouse';
const views:View[]=['login','setup','setup-password','setup-factor','finance','warehouse'];
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
type MockCase={id:string;run_id:string;cancel_id:string;reason:string;status:string;version:number;assignee:string;conclusion:string;source_row:number;sheet:string};
const cases:MockCase[]=Array.from({length:137},(_,i)=>{
  const t=txn(i*9),status=i<23?'closed':i<41?'in_progress':'pending';
  return {id:'mock-case-'+(i+1),run_id:run.run_id,cancel_id:t.id,reason:reasons[i%reasons.length],status,version:status==='closed'?3:status==='in_progress'?2:i%3===0?1:0,
    assignee:status!=='pending'||i%3===0?'warehouse':'',conclusion:status==='closed'?'模拟结论：已核对来源行与候选，资料不足，保留原因后结案。':'',source_row:t.source_row,sheet:t.sheet};
});
const audits=new Map<string,any[]>();
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
const stages=[['queued','queued'],['running','preparing'],['running','importing'],['running','reconciling'],['succeeded','published']];

function viewOf(req:IncomingMessage):View{const m=/(?:^|;\s*)mock_view=([\w-]+)/.exec(req.headers.cookie||'');return views.includes(m?.[1] as View)?m![1] as View:'finance'}
function send(res:ServerResponse,status:number,body:unknown,cookie?:View){res.statusCode=status;res.setHeader('Content-Type','application/json; charset=utf-8');if(cookie)res.setHeader('Set-Cookie',`mock_view=${cookie}; Path=/; SameSite=Strict`);res.end(JSON.stringify(body))}
function readBody(req:IncomingMessage){return new Promise<any>(resolve=>{let raw='';req.on('data',c=>raw+=c);req.on('end',()=>{try{resolve(raw?JSON.parse(raw):{})}catch{resolve({})}})})}
function page<T>(items:T[],q:URLSearchParams){const p=Math.max(1,Number(q.get('page'))||1),size=50;return {items:items.slice((p-1)*size,p*size),total:items.length,page:p,page_size:size}}
function sessionFor(v:View){
  const base={csrf:'mock-csrf',has_totp:false,has_passkey:false};
  if(v==='finance')return {...base,id:'finance',role:'finance',state:'complete',scope:'business',has_passkey:true};
  if(v==='warehouse')return {...base,id:'warehouse',role:'warehouse',state:'complete',scope:'business',has_passkey:true};
  if(v==='setup-password')return {...base,id:'admin',role:'admin',state:'new',scope:'setup'};
  if(v==='setup-factor')return {...base,id:'admin',role:'admin',state:'password',scope:'setup'};
  return null;
}
const refuse=(res:ServerResponse,what:string)=>send(res,422,{code:'mock_preview',message:`模拟预览不执行${what}；请在真实部署中由本人操作。`});

async function handle(req:IncomingMessage,res:ServerResponse,next:Connect.NextFunction){
  const url=new URL(req.url||'/','http://mock.local'),p=url.pathname,q=url.searchParams;
  if(p==='/__mock/badge.css'){res.setHeader('Content-Type','text/css');res.end(badgeCSS);return}
  if(p==='/__mock/view'){const v=views.includes(q.get('v') as View)?q.get('v') as View:'finance';res.statusCode=302;res.setHeader('Set-Cookie',`mock_view=${v}; Path=/; SameSite=Strict`);res.setHeader('Location','/'+(q.get('next')?'?'+q.get('next'):''));res.end();return}
  if(!p.startsWith('/api/'))return next();
  const v=viewOf(req),s=sessionFor(v),api=p.slice(4),body=req.method==='POST'?await readBody(req):{};
  await new Promise(r=>setTimeout(r,120+Math.random()*180));
  if(api==='/health')return send(res,200,{application:'returns-recon',runtime_id:'mock-preview'});
  if(api==='/auth/me')return s?send(res,200,s):send(res,401,{code:'unauthenticated',message:'未登录'});
  if(api==='/auth/setup/accounts')return send(res,200,v==='setup'?[{id:'admin',role:'admin'},{id:'warehouse',role:'warehouse'}]:[]);
  if(api==='/auth/setup/start')return send(res,200,{session:{...sessionFor('setup-password'),id:body.id}},'setup-password');
  if(api==='/auth/logout')return send(res,200,{},'login');
  if(api.startsWith('/auth/'))return refuse(res,'认证、密码或认证因素操作');
  if(!s||s.scope!=='business')return send(res,401,{code:'unauthenticated',message:'未登录'});
  const finance=s.role!=='warehouse',visible=cases.filter(c=>finance||c.assignee===s.id);
  if(api==='/runs')return send(res,200,runs);
  if(api==='/users')return send(res,200,[{id:'warehouse',role:'warehouse'},{id:'finance',role:'finance'},{id:'admin',role:'admin'}]);
  if(api==='/overview'){const n=(st:string)=>visible.filter(c=>c.status===st).length;return send(res,200,{run_id:q.get('run_id'),caveat:'规则推断；没有真实原订单关联号。人工处理记录为课程模拟。（此处为模拟数据）',cases:{total:visible.length,pending:n('pending'),in_progress:n('in_progress'),closed:n('closed')},...(finance?{initial_reconciliation:{input:{raw:TXN_TOTAL+41,accepted:TXN_TOTAL,excluded:41},matched:118,suspected:96,no_original:41}}:{})})}
  if(api==='/cases'){const st=q.get('status'),why=q.get('reason');if(why&&!reasons.includes(why))return send(res,422,{code:'invalid_input',message:'未知差异原因'});return send(res,200,page(visible.filter(c=>(!st||c.status===st)&&(!why||c.reason===why)),q))}
  if(api==='/allocations')return send(res,200,page(allocations,q));
  if(api==='/transactions'){
    let rows=Array.from({length:TXN_TOTAL},(_,i)=>txn(i));
    for(const k of ['customer','stock','invoice','country'] as const){const f=q.get(k);if(f)rows=rows.filter(r=>r[k]===f)}
    const from=q.get('date_from'),to=q.get('date_to');if(from)rows=rows.filter(r=>r.time>=from);if(to)rows=rows.filter(r=>r.time<to);
    const sort=q.get('sort')||'source',dir=q.get('direction')==='desc'?-1:1,key=({date:'time',source:'source_row'} as Record<string,string>)[sort]||sort;
    rows.sort((a:any,b:any)=>(a[key]>b[key]?1:a[key]<b[key]?-1:0)*dir);return send(res,200,page(rows,q));
  }
  if(api==='/exports'){res.setHeader('Content-Type','text/csv; charset=utf-8');res.end('mock_preview,模拟数据\n');return}
  const m=/^\/cases\/([^/]+)\/(evidence|audit|assign|start|close|confirm-match)$/.exec(api);
  const c=m&&visible.find(x=>x.id===m[1]);
  if(m&&!c)return send(res,404,{code:'not_found',message:'差异不存在或无权查看'});
  if(m&&c){
    const index=cases.indexOf(c),cancel={...txn(index*9),id:c.cancel_id},list=seedAudit(c);
    if(m[2]==='evidence'){
      const none=['no_identity_match','no_strictly_earlier_sale','insufficient_remaining_quantity','missing_identity'].includes(c.reason),many=c.reason==='ambiguous_candidates'&&index%2===0,total=none?0:many?63:2+(index%3);
      const all=Array.from({length:total},(_,i)=>{const t=txn(index*9+1+i);return {...t,original:Math.abs(t.quantity),allocated:0,current_remaining:Math.abs(t.quantity)-(i%2)}});
      const decision=none&&c.reason!=='missing_identity'?[0,1].map(i=>{const t=txn(index*9+1+i);return {...t,original:Math.abs(t.quantity),allocated:0,current_remaining:i,remaining_at_decision:i,eligible_at_decision:false,reason:c.reason==='no_strictly_earlier_sale'?'sale_not_strictly_earlier':'remaining_quantity_too_small'}}):all.slice(0,Math.min(2,total)).map((x,i)=>({...x,remaining_at_decision:x.original,eligible_at_decision:true,reason:c.reason==='suspected_duplicate_sale'&&i===0?'suspected_duplicate_sale':'qualified'}));
      return send(res,200,{case:c,cancel,decision_evidence:decision,candidates:page(all,q),caveat:'规则推断；没有真实原订单关联号。人工处理记录为课程模拟。（此处为模拟数据）'});
    }
    if(m[2]==='audit')return send(res,200,list);
    const before={status:c.status,version:c.version};
    if(m[2]==='assign'){c.assignee=body.assignee||'warehouse'}
    else if(m[2]==='start'){c.status='in_progress'}
    else if(m[2]==='close'){if(!String(body.conclusion||'').trim())return send(res,422,{code:'invalid_input',message:'请填写核查结论'});c.status='closed';c.conclusion=body.conclusion}
    else if(m[2]==='confirm-match'){list.push({id:list.length+1,actor:s.id,action:'confirm_candidate',occurred_at:new Date().toISOString(),before,after:{status:c.status,version:c.version+1},after_evidence:{manual_confirmation:{sale_id:body.sale_row_id,quantity:Math.abs(cancel.quantity)}}});c.version++;return send(res,200,{case:c})}
    c.version++;list.push({id:list.length+1,actor:s.id,action:m[2],occurred_at:new Date().toISOString(),before,after:{status:c.status,version:c.version}});
    return send(res,200,c);
  }
  if(finance&&api==='/jobs'&&req.method==='POST'){const id='mock-job-'+(jobs.size+1);jobs.set(id,{id,polls:0,scope:body.scope});return send(res,200,{id,status:'queued',stage:'queued',progress:{}})}
  const j=/^\/jobs\/([^/]+)$/.exec(api);
  if(finance&&j){const job=jobs.get(j[1]);if(!job)return send(res,404,{code:'not_found',message:'任务不存在'});const [status,stage]=stages[Math.min(job.polls++,stages.length-1)];const n=Math.min(job.polls,4);return send(res,200,{id:job.id,status,stage,run_id:run.run_id,progress:{input:{rows_read:n*321,accepted:n*310,excluded:n*11},matched:n*29,suspected:n*24}})}
  return send(res,404,{code:'not_found',message:'模拟预览没有此接口'});
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

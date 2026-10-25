import React,{useEffect,useRef,useState} from 'react';
import {accountName,allCountries,countryLabel,countrySummary,type Account} from './accounts';
import {startResourceRequest} from './requests';
import {ActivityColumns,CountryCoverage,StackedBar,coverage,type Segment} from './AccountCharts';
import PasskeyDomainsPanel from './PasskeyDomainsPanel';
import DeleteAccountDialog,{type DeletionImpact} from './DeleteAccountDialog';
import {signalRemovedPasskeys} from './passkey-signals';

type API=(path:string,body?:unknown,signal?:AbortSignal,method?:string)=>Promise<any>;
type Request={id:string;requester:string;account_id:string;display_name:string;country_scope:string;countries:string[];reason:string;status:string;created_at:string;reviewed_by:string;reviewed_at:string;rejection_reason:string;account_state?:string};
type Audit={id:number;actor:string;action:string;target:string;request_id:string;before:any;after:any;occurred_at:string};
type Form={id:string;role:string;display_name:string;country_scope:string;countries:string[];reason:string};
type Feedback={scope:string;tone:'success'|'error';text:string};
type Resource<T>={value?:T;loading:boolean;error?:string};

// Account configuration (auth_users.state) and request approval are two separate
// vocabularies; keep them apart so "approved" never reads as "ready to use".
const setupStates:[string,string,string,string][]=[
 ['new','待首次配置','step-1','尚未设置密码；使用者从登录页的首次配置入口开始'],
 ['password','待安全验证','step-2','已设置密码，尚未绑定并验证Passkey或认证器'],
 ['factor','待保存恢复码','step-3','已验证认证方式，尚未确认保存恢复码'],
 ['complete','已完成配置','step-4','可以进入业务'],
 ['recovering','恢复配置中','recovering','正在用恢复码重新绑定认证方式'],
];
const setupStep:Record<string,number>={new:0,password:1,factor:2,complete:3,recovering:1};
const requestStates:[string,string,string][]=[['pending','待审批','pending'],['approved','已批准并开户','approved'],['rejected','已驳回','rejected']];
const roles:Record<string,string>={finance:'财务',warehouse:'仓库',admin:'管理员'};
const auditActions:Record<string,string>={create:'创建账户',update:'修改账户',delete:'删除账户',request:'申请仓库',approved:'批准并开户',rejected:'驳回申请',passkey_domains:'修改Passkey域名'};
const idPattern=/^[a-z0-9][a-z0-9_.-]{2,63}$/;
const label=(list:[string,string,...string[]][],key:string)=>list.find(x=>x[0]===key)?.[1]||key;
const when=(value:string)=>{const d=new Date(value);return value&&!Number.isNaN(d.getTime())?d.toLocaleString('zh-CN',{hour12:false}):value||'—'};
const matches=(query:string,...fields:(string|undefined)[])=>!query||fields.some(f=>f?.toLowerCase().includes(query.toLowerCase()));

// Each section owns its request: one failing resource leaves the others usable.
// A refresh keeps the previous value on screen (same owner, since the panel is
// keyed by session) until the new one arrives.
function useLoad<T>(enabled:boolean,revision:number,load:(signal:AbortSignal)=>Promise<T>):Resource<T>{
 const [state,setState]=useState<Resource<T>>({loading:enabled});
 useEffect(()=>{if(!enabled)return;return startResourceRequest(load,r=>setState(prev=>r.loading?{value:prev.value,loading:true}:{value:r.value!==undefined?r.value:prev.value,loading:false,error:r.error}))},[enabled,revision]);
 return state;
}

function LoadState({resource,what,onRetry}:{resource:Resource<unknown>;what:string;onRetry:()=>void}){
 if(resource.error)return <p role="alert" className="inline-alert load-error">{resource.value!==undefined?`${what}刷新失败，下方为上次加载的结果：`:`${what}加载失败：`}{resource.error} <button type="button" className="text-button" onClick={onRetry}>重试</button></p>;
 if(resource.loading&&resource.value===undefined)return <p role="status" className="muted load-state"><span className="spinner" aria-hidden="true"/>正在读取{what}…</p>;
 return null;
}

// Inside a chart card: spinner while loading, a quiet placeholder on failure
// (the overview shows the error and retry once, above the figures).
function ChartPending({resource}:{resource:Resource<unknown>}){return resource.error?<p className="muted chart-pending">数据未加载，图表暂不可用。</p>:<p role="status" className="muted load-state"><span className="spinner" aria-hidden="true"/>正在读取…</p>}

function CountryChips({account,max=3}:{account:Account;max?:number}){
 const [open,setOpen]=useState(false);
 if(allCountries(account))return <span className="country-chips"><span className="country-chip all">全部国家/地区</span></span>;
 const list=account.countries||[],shown=open?list:list.slice(0,max);
 if(!list.length)return <span className="muted">未指定国家/地区</span>;
 return <span className="country-chips">{shown.map(c=><span key={c} className="country-chip">{c}</span>)}{list.length>max&&<button type="button" className="country-chip more" aria-expanded={open} aria-label={open?'收起国家/地区列表':`另有 ${list.length-max} 个国家/地区：${list.slice(max).join('、')}`} onClick={()=>setOpen(!open)}>{open?'收起':`+${list.length-max}`}</button>}</span>;
}

function SetupMeter({state}:{state?:string}){
 const step=setupStep[state||'']??0,done=state==='complete';
 return <span className={'setup-meter'+(state==='recovering'?' is-recovering':'')} title={setupStates.find(s=>s[0]===state)?.[3]}>
  <span className="setup-ticks" aria-hidden="true">{[0,1,2].map(i=><i key={i} className={i<step||done?'on':''}/>)}</span>
  <span>{label(setupStates,state||'')}</span>
 </span>;
}

function CountryPicker({canAll,scope,selected,countries,resource,limit,error,onScope,onChange,onRetry}:{canAll:boolean;scope:string;selected:string[];countries:string[];resource:Resource<string[]>;limit:string;error?:string;onScope:(scope:string)=>void;onChange:(list:string[])=>void;onRetry:()=>void}){
 const [query,setQuery]=useState('');
 const options=[...new Set([...countries,...selected])],filtered=options.filter(c=>matches(query,c));
 return <fieldset className={'country-picker'+(error?' has-error':'')} aria-describedby={error?'country-error':undefined}>
  <legend>负责国家/地区</legend>
  {canAll?<div className="scope-choice">
   <label className={'choice-card'+(scope==='all'?' is-chosen':'')}><input type="radio" name="country-scope" checked={scope==='all'} onChange={()=>onScope('all')}/><span><strong>全部国家/地区</strong><small>国家/地区范围不设限制，仍遵守账户角色权限</small></span></label>
   <label className={'choice-card'+(scope==='selected'?' is-chosen':'')}><input type="radio" name="country-scope" checked={scope==='selected'} onChange={()=>onScope('selected')}/><span><strong>指定国家/地区</strong><small>按所选国家/地区限制业务范围，仍遵守角色权限</small></span></label>
  </div>:<p className="picker-note">你的负责范围是 {limit}，只能在此范围内选择。</p>}
  {scope==='selected'&&<div className="picker-body">
   <div className="picked" aria-live="polite">{selected.length?<><span className="picked-count">已选 {selected.length} 个</span>{selected.map(c=><button type="button" key={c} className="country-chip removable" aria-label={'移除 '+c} onClick={()=>onChange(selected.filter(x=>x!==c))}>{c}<span aria-hidden="true">×</span></button>)}</>:<span className="muted">尚未选择国家/地区</span>}</div>
   <LoadState resource={resource} what="国家/地区列表" onRetry={onRetry}/>
   {options.length>0&&<>
    <div className="picker-tools">{options.length>8&&<input type="search" aria-label="搜索国家/地区" placeholder="搜索国家/地区" value={query} onChange={e=>setQuery(e.target.value)}/>}
     <button type="button" className="text-button" disabled={!filtered.length} onClick={()=>onChange([...new Set([...selected,...filtered])])}>{query?'选中搜索结果':'全选'}</button>
     <button type="button" className="text-button" disabled={!selected.length} onClick={()=>onChange([])}>清空</button></div>
    <div className="country-options">{filtered.map(c=><label className="checkbox" key={c}><input type="checkbox" checked={selected.includes(c)} onChange={e=>onChange(e.target.checked?[...selected,c]:selected.filter(x=>x!==c))}/>{c}</label>)}{!filtered.length&&<p className="muted">没有匹配的国家/地区。</p>}</div>
   </>}
   {!resource.loading&&!resource.error&&!options.length&&<p className="muted">当前没有可选国家/地区，请先发布业务数据{canAll?'，或使用全部国家/地区':''}。</p>}
  </div>}
  {error&&<p id="country-error" className="field-error">{error}</p>}
 </fieldset>;
}

export default function AccountPanel({api,session,onChanged}:{api:API;session:Account;onChanged:()=>void}){
 const admin=session.role==='admin',canAll=allCountries(session);
 const [rev,setRev]=useState({countries:0,requests:0,accounts:0,audit:0});
 const countries=useLoad<string[]>(true,rev.countries,signal=>api('/countries',undefined,signal));
 const requests=useLoad<Request[]>(true,rev.requests,signal=>api('/warehouse-requests',undefined,signal));
 const accounts=useLoad<Account[]>(admin,rev.accounts,signal=>api('/admin/accounts',undefined,signal));
 const audit=useLoad<Audit[]>(admin,rev.audit,signal=>api('/admin/account-audit',undefined,signal));
 const reload=(...keys:(keyof typeof rev)[])=>setRev(r=>{const next={...r};for(const k of keys)next[k]++;return next});
 const blank=():Form=>({id:'',role:'warehouse',display_name:'',country_scope:canAll?'all':'selected',countries:[],reason:''});
 const [tab,setTab]=useState('directory'),[form,setForm]=useState<Form>(blank),[editing,setEditing]=useState<Account|null>(null),[fieldErrors,setFieldErrors]=useState<Record<string,string>>({});
 const [query,setQuery]=useState(''),[roleFilter,setRoleFilter]=useState(''),[stateFilter,setStateFilter]=useState(''),[countryFilter,setCountryFilter]=useState('');
 const [requestFilter,setRequestFilter]=useState(admin?'pending':''),[requestQuery,setRequestQuery]=useState(''),[requestLimit,setRequestLimit]=useState(20),[auditFilter,setAuditFilter]=useState('');
 const [confirming,setConfirming]=useState(''),[rejecting,setRejecting]=useState(''),[rejections,setRejections]=useState<Record<string,string>>({});
 const [busy,setBusy]=useState(false),[working,setWorking]=useState(""),[feedback,setFeedback]=useState<Feedback|null>(null);
 const busyRef=useRef(false),mounted=useRef(true),editorRef=useRef<HTMLElement>(null),nameRef=useRef<HTMLInputElement>(null);
 const [deleting,setDeleting]=useState<Account|null>(null),deleteOpener=useRef<HTMLElement|null>(null);
 useEffect(()=>()=>{mounted.current=false},[]);
 const accountList=accounts.value||[],requestList=requests.value||[],countryList=countries.value||[],auditList=audit.value||[];
 const byID=new Map(accountList.map(a=>[a.id,a]));

 // One write at a time. The ref blocks a second click before React re-renders;
 // a failed write is never replayed, only the reads are refreshed.
 async function run(scope:string,work:()=>Promise<string>,errorScope=scope){
  if(busyRef.current)return;busyRef.current=true;setBusy(true);setWorking(scope);setFeedback(null);
  try{const text=await work();if(!mounted.current)return;setFeedback({scope,tone:'success',text});reload('requests','accounts','audit');onChanged()}
  catch(e){if(!mounted.current)return;setFeedback({scope:errorScope,tone:'error',text:e instanceof Error?e.message:'操作未完成'});reload('requests','accounts')}
  finally{busyRef.current=false;if(mounted.current){setBusy(false);setWorking("")}}
 }
 const notice=(scope:string)=>feedback?.scope===scope&&<div role={feedback.tone==='error'?'alert':'status'} className={'notice'+(feedback.tone==='error'?' error':'')}>{feedback.text}<button type="button" aria-label="关闭提示" onClick={()=>setFeedback(null)}>×</button></div>;

 function startCreate(){setEditing(null);setForm(blank());setFieldErrors({});if(feedback?.scope==='form')setFeedback(null)}
 function startEdit(a:Account){setTab('directory');setEditing(a);setForm({id:a.id,role:a.role,display_name:a.display_name||a.id,country_scope:allCountries(a)?'all':'selected',countries:a.countries||[],reason:''});setFieldErrors({});setFeedback(null);
  requestAnimationFrame(()=>{editorRef.current?.scrollIntoView({behavior:'smooth',block:'nearest'});nameRef.current?.focus({preventScroll:true})})}
 const scopeChanged=!!editing&&(form.country_scope!==(allCountries(editing)?'all':'selected')||JSON.stringify([...form.countries].sort())!==JSON.stringify([...(editing.countries||[])].sort()));
 function validate(){
  const errors:Record<string,string>={};
  if(!editing&&!idPattern.test(form.id))errors.id='登录账户名须为3至64位，以小写字母或数字开头，只含小写字母、数字、点、下划线或连字符。';
  const name=form.display_name.trim();
  if(!name)errors.display_name='请填写显示名称。';else if([...name].length>64)errors.display_name='显示名称最多64个字符。';
  if(form.country_scope==='selected'&&!form.countries.length)errors.countries=canAll?'请至少选择一个国家/地区，或改为全部国家/地区。':'请至少选择一个国家/地区；你的账户不能申请全部国家/地区。';
  setFieldErrors(errors);return !Object.keys(errors).length;
 }
 function submit(e:React.FormEvent){
  e.preventDefault();if(busyRef.current||!validate())return;
  const scope=canAll?form.country_scope:'selected',list=scope==='selected'?form.countries:[],display_name=form.display_name.trim();
  const target={id:editing?.id||form.id,role:editing?.role||(admin?form.role:'warehouse'),display_name,country_scope:scope,countries:list};
  void run('form',async()=>{
   if(editing){const changed=scopeChanged;const v=await api('/admin/accounts/'+encodeURIComponent(editing.id),{display_name,country_scope:scope,countries:list},undefined,'PATCH');if(mounted.current){setEditing(v);setForm(f=>({...f,display_name:v.display_name,country_scope:allCountries(v)?'all':'selected',countries:v.countries||[]}))}
    return `已保存 ${accountName(v)}（${v.id}）。`+(changed?'负责国家/地区已变化：该账户的旧会话已退出，旧推荐和批次需重新生成。':'')}
   if(admin){const v=await api('/admin/accounts',target);if(mounted.current)setForm(blank());return `已创建${roles[v.role]}账户 ${accountName(v)}（${v.id}，${countrySummary(v)}）。当前为“待首次配置”：使用者需在登录页的首次配置入口设置强密码，再验证Passkey或认证器并保存恢复码。`}
   const v=await api('/warehouse-requests',{...target,reason:form.reason.trim()});if(mounted.current){setForm(blank());setRequestFilter('')}
   return `已提交 ${v.display_name}（${v.account_id}）的开户申请，等待管理员审批。审批前该账户名被占用，申请提交后不能修改。`;
  });
 }

 // ---- derived figures -------------------------------------------------------
 const roleCounts=(r:string)=>accountList.filter(a=>a.role===r).length;
 const stateCounts=(s:string)=>accountList.filter(a=>a.state===s).length;
 const requestCounts=(s:string)=>requestList.filter(r=>r.status===s).length;
 const unfinished=accountList.filter(a=>a.state!=='complete').length;
 const {gaps}=coverage(countryList,accountList);
 const setupSegments:Segment[]=setupStates.filter(([k])=>k!=='recovering'||stateCounts(k)>0).map(([key,name,tone,hint])=>({key,label:name,value:stateCounts(key),tone,hint}));
 const roleSegments:Segment[]=[['finance','财务'],['warehouse','仓库'],['admin','管理员']].map(([key,name])=>({key,label:name,value:roleCounts(key),tone:key}));
 const requestSegments:Segment[]=requestStates.map(([key,name,tone])=>({key,label:name,value:requestCounts(key),tone}));
 const directory=accountList.filter(a=>(!roleFilter||a.role===roleFilter)&&(!stateFilter||a.state===stateFilter)&&(!countryFilter||allCountries(a)||a.countries?.includes(countryFilter))&&matches(query,a.id,a.display_name,...(a.countries||[])));
 const directoryFiltered=!!(query||roleFilter||stateFilter||countryFilter);
 const shownRequests=requestList.filter(r=>(!requestFilter||r.status===requestFilter)&&matches(requestQuery,r.account_id,r.display_name,r.requester,byID.get(r.requester)?.display_name,r.reason,...r.countries));
 const auditShown=auditList.filter(a=>!auditFilter||a.action===auditFilter);
 const pending=requestCounts('pending');
 const fig=(n:number|undefined)=>n===undefined?'—':n.toLocaleString('zh-CN');

 // ---- sections --------------------------------------------------------------
 const editor=<section ref={editorRef} className={'panel account-editor'+(editing?' is-editing':'')} aria-labelledby="account-editor-title" aria-busy={busy}>
  <div className="section-head"><h2 id="account-editor-title">{editing?'编辑账户':admin?'新建账户':'申请新的仓库账户'}</h2>{editing&&<button type="button" className="text-button" disabled={busy} onClick={startCreate}>改为新建账户</button>}</div>
  <p className="muted editor-intro">{editing?'可修改显示名称和负责国家/地区。':admin?'创建后账户为“待首次配置”，由使用者自己设置强密码并完成安全验证。':'管理员批准后立即创建账户，使用者随后完成首次安全配置。'}</p>
  <form className="account-form" noValidate onSubmit={submit}>
   <fieldset disabled={busy} className="account-fields">
    {editing?<dl className="locked-facts">
     <div><dt>登录账户名</dt><dd className="mono">{editing.id}</dd></div>
     <div><dt>角色</dt><dd><span className={'role-pill '+editing.role}>{roles[editing.role]}</span></dd></div>
     <div><dt>安全配置</dt><dd><SetupMeter state={editing.state}/></dd></div>
     <p className="locked-note">登录账户名和角色创建后不可更改。</p>
    </dl>:<>
     <label>登录账户名<input value={form.id} onChange={e=>setForm({...form,id:e.target.value.trim()})} maxLength={64} autoComplete="off" spellCheck={false} placeholder={admin?'例如 finance-uk':'例如 warehouse-uk'} aria-invalid={!!fieldErrors.id} aria-describedby="account-id-help"/>
      <small id="account-id-help" className={fieldErrors.id?'field-error':''}>{fieldErrors.id||'3至64位小写字母、数字、点、下划线或连字符，创建后不可更改。'}</small></label>
     {admin&&<fieldset className="role-choice"><legend>账户角色</legend>
      {[['warehouse','仓库','处理派给自己的差异'],['finance','财务','派单、复核、导出，可申请仓库账户']].map(([key,name,hint])=><label key={key} className={'choice-card'+(form.role===key?' is-chosen':'')}><input type="radio" name="account-role" checked={form.role===key} onChange={()=>setForm({...form,role:key})}/><span><strong>{name}</strong><small>{hint}</small></span></label>)}
     </fieldset>}
    </>}
    <label>显示名称<input ref={nameRef} value={form.display_name} onChange={e=>setForm({...form,display_name:e.target.value})} maxLength={64} placeholder={admin?'例如 英国财务':'例如 英国仓库'} aria-invalid={!!fieldErrors.display_name} aria-describedby="display-name-help"/>
     <small id="display-name-help" className={fieldErrors.display_name?'field-error':''}>{fieldErrors.display_name||'用于顶栏、派单列表和Passkey名称，最多64个字符。'}</small></label>
    <CountryPicker canAll={canAll} scope={canAll?form.country_scope:'selected'} selected={form.countries} countries={countryList} resource={countries} limit={countryLabel(session)} error={fieldErrors.countries}
     onScope={scope=>setForm({...form,country_scope:scope,countries:scope==='all'?[]:form.countries})} onChange={list=>setForm({...form,countries:list})} onRetry={()=>reload('countries')}/>
    {!admin&&<label>申请理由（可选）<textarea value={form.reason} maxLength={1000} onChange={e=>setForm({...form,reason:e.target.value})} placeholder="说明新仓库账户的用途，管理员审批时可见"/><small className="counter">{form.reason.length} / 1000</small></label>}
    {scopeChanged&&<div className="change-warning" role="note"><strong>负责国家/地区将改变</strong>保存后该账户的已登录会话会退出，旧推荐和批次失效需重建。范围外仍有未结案派单时，系统会拒绝缩小范围。</div>}
    {notice('form')}
    <div className="button-row editor-actions"><button>{working==="form"?'正在保存…':editing?'保存修改':admin?'创建账户':'提交申请'}</button>{editing&&<button type="button" className="secondary" onClick={startCreate}>取消编辑</button>}</div>
   </fieldset>
  </form>
 </section>;

 const directoryPanel=<section className="panel account-directory" aria-labelledby="directory-title">
  {notice('directory')}
  <div className="section-head"><h2 id="directory-title">账户名录</h2><button type="button" className="secondary compact" disabled={accounts.loading} onClick={()=>reload('accounts','countries')}>{accounts.loading&&accounts.value?'正在刷新…':'刷新'}</button></div>
  <div className="directory-tools">
   <input type="search" aria-label="搜索账户" placeholder="搜索显示名、登录名或国家/地区" value={query} onChange={e=>setQuery(e.target.value)}/>
   <div className="segmented" role="group" aria-label="按角色筛选">{[['','全部',accountList.length],['finance','财务',roleCounts('finance')],['warehouse','仓库',roleCounts('warehouse')],['admin','管理员',roleCounts('admin')]].map(([key,name,n])=><button type="button" key={key} aria-pressed={roleFilter===key} onClick={()=>setRoleFilter(String(key))}>{name} <span className="seg-count">{n}</span></button>)}</div>
   <select aria-label="按配置状态筛选" value={stateFilter} onChange={e=>setStateFilter(e.target.value)}><option value="">全部配置状态</option>{setupStates.map(([k,name])=><option key={k} value={k}>{name}</option>)}</select>
   <select aria-label="按负责国家/地区筛选" value={countryFilter} onChange={e=>setCountryFilter(e.target.value)}><option value="">全部负责国家/地区</option>{[...new Set([...countryList,...accountList.flatMap(a=>a.countries||[])])].sort((a,b)=>a.localeCompare(b)).map(c=><option key={c} value={c}>{c}</option>)}</select>
  </div>
  {(countryFilter||directoryFiltered)&&<div className="active-filters">{countryFilter&&<span className="filter-chip">负责 {countryFilter}（含全部国家/地区账户）<button type="button" aria-label="取消国家/地区筛选" onClick={()=>setCountryFilter('')}>×</button></span>}<span className="muted">显示 {directory.length} / {accountList.length} 个账户</span><button type="button" className="text-button" onClick={()=>{setQuery('');setRoleFilter('');setStateFilter('');setCountryFilter('')}}>清除筛选</button></div>}
  {accounts.error&&!accounts.value?<p className="muted empty-inline">账户名录未加载，请使用页面顶部的重试。</p>:<LoadState resource={accounts} what="账户" onRetry={()=>reload('accounts')}/>}
  {accounts.value&&<div className={'table-scroll'+(accounts.loading?' is-loading':'')}><table className="data-table account-table">
   <thead><tr><th>账户与角色</th><th>负责国家/地区</th><th>安全配置</th><th className="act"><span className="sr-only">操作</span></th></tr></thead>
   <tbody>{directory.map(a=><tr key={a.id} className={editing?.id===a.id?'is-editing':undefined}>
    <td className="account-cell"><strong>{accountName(a)}</strong><span className="account-meta">{a.display_name&&a.display_name!==a.id&&<span className="mono">{a.id}</span>}<span className={'role-pill '+a.role}>{roles[a.role]||a.role}</span></span></td>
    <td className="countries-cell"><CountryChips account={a}/></td>
    <td><SetupMeter state={a.state}/></td>
    <td className="act">{a.role==='admin'?<span className="muted">管理员受保护</span>:<span className="account-actions"><button type="button" className="row-action" disabled={busy} aria-label={'编辑 '+accountName(a)} onClick={()=>startEdit(a)}>{editing?.id===a.id?'编辑中':'编辑'}</button><button type="button" className="row-action delete-account-action" disabled={busy} aria-label={'删除 '+accountName(a)} onClick={e=>{deleteOpener.current=e.currentTarget;setFeedback(null);setDeleting(a)}}>删除</button></span>}</td>
   </tr>)}</tbody>
  </table>{!directory.length&&<p className="empty-inline">{accountList.length?'没有符合筛选条件的账户。':'还没有账户。'}</p>}</div>}
 </section>;

 const requestCard=(r:Request)=>{
  const made=byID.get(r.account_id),requester=byID.get(r.requester),acting=confirming===r.id||rejecting===r.id;
  const approval=r.status==='pending'?'current':r.status==='approved'?'done':'stopped';
  const accountState=r.account_state||made?.state;
  const setup=r.status!=='approved'?'todo':accountState==='deleted'?'stopped':accountState==='complete'?'done':accountState?'current':'todo';
  return <article key={r.id} className={'request-card status-'+r.status+(acting?' is-acting':'')} aria-labelledby={'req-'+r.id}>
   <header><div className="request-title"><h3 id={'req-'+r.id}>{r.display_name}</h3><span className="mono">{r.account_id}</span></div><span className={'request-status '+r.status}>{label(requestStates,r.status)}</span></header>
   <dl className="request-facts">
    <div><dt>申请人</dt><dd>{requester?<>{accountName(requester)}<small className="mono">{r.requester}</small></>:<span className="mono">{r.requester}</span>}</dd></div>
    <div><dt>负责国家/地区</dt><dd><CountryChips account={{id:r.account_id,role:'warehouse',country_scope:r.country_scope,countries:r.countries}}/></dd></div>
   </dl>
   {r.reason?<blockquote className="request-reason">{r.reason}</blockquote>:<p className="muted request-no-reason">未填写申请理由。</p>}
   <ol className="request-timeline" aria-label="办理进度">
    <li className="done"><span>提交申请</span><small>{when(r.created_at)}</small></li>
    <li className={approval}><span>{r.status==='pending'?'等待管理员审批':r.status==='approved'?'已批准并开户':'已驳回'}</span><small>{r.reviewed_at?`${byID.get(r.reviewed_by)?accountName(byID.get(r.reviewed_by)!):r.reviewed_by} · ${when(r.reviewed_at)}`:'尚未审批'}</small></li>
    <li className={setup}><span>首次安全配置</span><small>{r.status==='rejected'?'未开户':r.status==='pending'?'批准后由使用者完成':accountState==='deleted'?'账户已删除，业务历史保留':accountState?label(setupStates,accountState):'尚未读取到配置状态，请刷新申请'}</small></li>
   </ol>
   {r.status==='rejected'&&<p className="rejection"><strong>驳回原因</strong>{r.rejection_reason}</p>}
   {admin&&r.status==='pending'&&(confirming===r.id?<div className="request-confirm" role="group" aria-label="确认批准">
     <p>将立即创建仓库账户 <strong>{r.display_name}</strong>（<span className="mono">{r.account_id}</span>，{countrySummary({id:r.account_id,role:'warehouse',country_scope:r.country_scope,countries:r.countries})}）。账户仍需使用者完成首次安全配置。</p>
     <div className="button-row"><button type="button" disabled={busy} onClick={()=>void run('requests',async()=>{await api('/warehouse-requests/'+encodeURIComponent(r.id)+'/approve',{});if(mounted.current)setConfirming('');return `已批准并创建仓库账户 ${r.display_name}（${r.account_id}）。账户现为“待首次配置”，使用者需在登录页首次配置入口设置强密码，并验证Passkey或认证器、保存恢复码后才能使用。`},'request:'+r.id)}>{busy?'正在开户…':'确认批准并开户'}</button><button type="button" className="secondary" disabled={busy} onClick={()=>setConfirming('')}>取消</button></div>
    </div>:rejecting===r.id?<div className="request-reject">
     <label>驳回原因（申请人可见）<textarea autoFocus value={rejections[r.id]||''} maxLength={1000} onChange={e=>setRejections({...rejections,[r.id]:e.target.value})} placeholder="说明需要调整的地方，例如国家/地区范围或账户用途"/><small className="counter">{(rejections[r.id]||'').length} / 1000</small></label>
     <div className="button-row"><button type="button" disabled={busy||!rejections[r.id]?.trim()} onClick={()=>void run('requests',async()=>{await api('/warehouse-requests/'+encodeURIComponent(r.id)+'/reject',{reason:rejections[r.id].trim()});if(mounted.current)setRejecting('');return `已驳回 ${r.display_name}（${r.account_id}）的申请，申请人可重新提交。`},'request:'+r.id)}>{busy?'正在驳回…':'确认驳回'}</button><button type="button" className="secondary" disabled={busy} onClick={()=>setRejecting('')}>取消</button></div>
    </div>:<div className="button-row request-actions"><button type="button" disabled={busy} onClick={()=>{setRejecting('');setConfirming(r.id)}}>批准并创建账户</button><button type="button" className="secondary" disabled={busy} onClick={()=>{setConfirming('');setRejecting(r.id)}}>驳回…</button></div>)}
   {notice('request:'+r.id)}
  </article>;
 };

 const requestsPanel=<section className="panel requests-panel" aria-labelledby="requests-title">
  <div className="section-head"><h2 id="requests-title">{admin?'仓库开户申请':'我的仓库申请'}</h2><button type="button" className="secondary compact" disabled={requests.loading} onClick={()=>reload('requests','accounts')}>{requests.loading&&requests.value?'正在刷新…':'刷新'}</button></div>
  {!admin&&<p className="muted">只显示你提交的申请。待审批的申请不能修改；被驳回后可修改后重新提交。</p>}
  {notice('requests')}
  <div className="directory-tools">
   <div className="segmented" role="group" aria-label="按审批状态筛选">{[['','全部',requestList.length],...requestStates.map(([k,name])=>[k,name,requestCounts(k)])].map(([key,name,n])=><button type="button" key={String(key)} aria-pressed={requestFilter===key} onClick={()=>{setRequestFilter(String(key));setRequestLimit(20)}}>{name} <span className="seg-count">{n}</span></button>)}</div>
   {requestList.length>5&&<input type="search" aria-label="搜索申请" placeholder="搜索账户、申请人、国家/地区或理由" value={requestQuery} onChange={e=>{setRequestQuery(e.target.value);setRequestLimit(20)}}/>}
  </div>
  <LoadState resource={requests} what="申请" onRetry={()=>reload('requests')}/>
  {requests.value&&<div className={'request-list'+(requests.loading?' is-loading':'')}>
   {shownRequests.slice(0,requestLimit).map(requestCard)}
   {!shownRequests.length&&<div className="empty-inline">{!requestList.length?(admin?'还没有开户申请。':'你还没有提交过仓库账户申请。'):requestFilter==='pending'&&!requestQuery?<>没有待审批的申请。<button type="button" className="text-button" onClick={()=>setRequestFilter('')}>查看全部申请</button></>:'没有符合条件的申请。'}</div>}
   {shownRequests.length>requestLimit&&<button type="button" className="secondary" onClick={()=>setRequestLimit(n=>n+20)}>再显示 {Math.min(20,shownRequests.length-requestLimit)} 条（共 {shownRequests.length} 条）</button>}
  </div>}
 </section>;

 const change=(a:Audit)=>{
  const b=a.before||{},n=a.after||{},parts:string[]=[];
  if(a.action==='update'){if(b.display_name!==n.display_name)parts.push(`显示名称 ${b.display_name||'—'} → ${n.display_name}`);if(countryLabel(b)!==countryLabel(n))parts.push(`负责国家/地区 ${countrySummary(b,3)} → ${countrySummary(n,3)}`)}
  else if(a.action==='create'||a.action==='request')parts.push(`${roles[n.role]||'仓库'} · ${n.display_name||''} · ${countrySummary(n.role?n:{...n,role:'warehouse'},3)}`);
  else if(a.action==='rejected'&&n.rejection_reason)parts.push('原因：'+n.rejection_reason);
  else if(a.action==='passkey_domains')parts.push(`批准入口 ${(n.origins||[]).length} 个；旧本机入口保留`);
  else if(a.action==='delete')parts.push(`退回未派单 ${n.open_cases||0} 条；保留已办结 ${n.closed_cases||0} 条；自动驳回申请 ${n.pending_requests||0} 条`);
  return parts.join('；');
 };
 const auditPanel=<section className="panel audit-panel" aria-labelledby="audit-title">
  <div className="section-head"><h2 id="audit-title">管理操作记录</h2><button type="button" className="secondary compact" disabled={audit.loading} onClick={()=>reload('audit','accounts')}>刷新</button></div>
  <p className="muted">最近200条账户创建、修改、删除、申请与审批，只读，不可删除。</p>
  <LoadState resource={audit} what="操作记录" onRetry={()=>reload('audit')}/>
  {audit.value&&<>
   <ActivityColumns events={auditList}/>
   <div className="segmented" role="group" aria-label="按操作类型筛选">{[['','全部',auditList.length],...Object.entries(auditActions).map(([k,v])=>[k,v,auditList.filter(a=>a.action===k).length])].map(([key,name,n])=><button type="button" key={String(key)} aria-pressed={auditFilter===key} onClick={()=>setAuditFilter(String(key))}>{name} <span className="seg-count">{n}</span></button>)}</div>
   <ol className="audit-list account-audit">{auditShown.map(a=><li key={a.id}><strong>{auditActions[a.action]||<span className="raw-code">{a.action}</span>}</strong> <span className="mono">{a.target}</span><small>{byID.get(a.actor)?accountName(byID.get(a.actor)!):a.actor} · {when(a.occurred_at)}</small>{change(a)&&<p>{change(a)}</p>}</li>)}</ol>
   {!auditShown.length&&<p className="muted empty-inline">暂无管理操作。</p>}
  </>}
 </section>;

 if(!admin){
  return <div className="accounts-workspace">
   <section className="account-overview finance-overview" aria-label="负责范围与申请概况">
    <div className="panel scope-card"><h2>我的负责范围</h2><CountryChips account={session} max={8}/><p className="muted">交易、差异、统计、导出和派单都限于这些国家/地区。申请的仓库账户必须在此范围内。</p></div>
    <figure className="panel chart-card"><figcaption><h3>我的申请</h3><span className="muted">点击图例筛选列表</span></figcaption>{requests.value?<StackedBar label="我的申请审批状态" segments={requestSegments} selected={requestFilter} onSelect={setRequestFilter}/>:<ChartPending resource={requests}/>}</figure>
   </section>
   <div className="accounts-split">{editor}{requestsPanel}</div>
  </div>;
 }
 return <div className="accounts-workspace">
  <section className="account-overview" aria-label="账户概况">
   {(accounts.error||countries.error)&&<div className="overview-errors">{accounts.error&&<LoadState resource={accounts} what="账户" onRetry={()=>reload('accounts')}/>}{countries.error&&<LoadState resource={countries} what="国家/地区列表" onRetry={()=>reload('countries')}/>}</div>}
   <dl className="account-kpis">
    <div><dt>账户</dt><dd className={accounts.value?undefined:'is-missing'}>{fig(accounts.value&&accountList.length)}</dd><small>{accounts.value?`财务 ${roleCounts('finance')} · 仓库 ${roleCounts('warehouse')} · 管理员 ${roleCounts('admin')}`:accounts.error?'账户未加载':'正在读取…'}</small></div>
    <div className={pending?'is-attention':''}><dt>待审批申请</dt><dd>{fig(requests.value&&pending)}</dd>{pending>0?<button type="button" className="text-button" onClick={()=>{setTab('requests');setRequestFilter('pending')}}>去审批</button>:<small>{requests.error?'申请未加载':'没有待处理'}</small>}</div>
    <div><dt>未完成安全配置</dt><dd>{fig(accounts.value&&unfinished)}</dd><small>需账户使用者本人完成</small></div>
    <div className={gaps.length?'is-attention':''}><dt>无仓库覆盖的国家/地区</dt><dd>{fig(accounts.value&&countries.value&&gaps.length)}</dd><small>{countries.error?'国家/地区列表未加载':`已发布 ${countryList.length} 个国家/地区`}</small></div>
   </dl>
   <div className="overview-charts">
    <div className="chart-stack"><figure className="panel chart-card"><figcaption><h3>安全配置进度</h3><span className="muted">点击图例筛选名录</span></figcaption>{accounts.value?<StackedBar label="账户安全配置进度" segments={setupSegments} selected={stateFilter} onSelect={k=>{setTab('directory');setStateFilter(k)}}/>:<ChartPending resource={accounts}/>}</figure>
    <figure className="panel chart-card"><figcaption><h3>角色构成</h3><span className="muted">点击图例筛选名录</span></figcaption>{accounts.value?<StackedBar label="账户角色构成" segments={roleSegments} selected={roleFilter} onSelect={k=>{setTab('directory');setRoleFilter(k)}}/>:<ChartPending resource={accounts}/>}</figure></div>
    <figure className="panel chart-card coverage-card"><figcaption><h3>国家/地区覆盖</h3><span className="muted">每个国家/地区的限定财务 / 限定仓库账户数</span></figcaption>{accounts.value&&countries.value?<CountryCoverage countries={countryList} accounts={accountList} selected={countryFilter} onSelect={c=>{setTab('directory');setCountryFilter(c)}}/>:<ChartPending resource={countries.error?countries:accounts}/>}</figure>
   </div>
  </section>
  <div className="account-tabs" role="tablist" aria-label="账户管理分区">
   {[['directory','账户名录',accounts.value?String(accountList.length):''],['requests','开户申请',pending?`${pending} 待审批`:''],['audit','操作记录',''],['domains','许可入口','']].map(([key,name,extra])=><button type="button" role="tab" key={key} id={'tab-'+key} aria-selected={tab===key} aria-controls={'panel-'+key} className={'account-tab'+(key==='requests'&&pending?' has-pending':'')} onClick={()=>setTab(key)}>{name}{extra&&<span className="tab-count">{extra}</span>}</button>)}
  </div>
  <div role="tabpanel" id={'panel-'+tab} aria-labelledby={'tab-'+tab} className="account-tabpanel">
   {tab==='directory'?<div className="accounts-split">{directoryPanel}{editor}</div>:tab==='requests'?requestsPanel:tab==='domains'?<PasskeyDomainsPanel api={api} onChanged={()=>{reload('audit');onChanged()}}/>:auditPanel}
  </div>
  {deleting&&<DeleteAccountDialog account={deleting} api={api} busy={busy} error={feedback?.scope==='delete'?feedback.text:undefined} opener={deleteOpener} onClose={()=>{if(!busy)setDeleting(null)}} onConfirm={()=>{const target=deleting;void run('directory',async()=>{const result:DeletionImpact=await api('/admin/accounts/'+encodeURIComponent(target.id),{},undefined,'DELETE');if(mounted.current){setDeleting(null);if(editing?.id===target.id)startCreate()}const signal=result.passkey_count&&!result.revoked_credential_ids?.length?'该账户的Passkey属于其他域名，请在设备密码管理器中手动清理。':await signalRemovedPasskeys(result.revoked_credential_ids||[]);return `已删除 ${accountName(target)}（${target.id}）。${result.open_cases} 条未办结差异已退回未派单，${result.closed_cases} 条已办结差异及历史保留。${signal}`},'delete')}}/>}
 </div>;
}

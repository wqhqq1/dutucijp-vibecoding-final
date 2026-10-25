import React,{useEffect,useRef,useState} from 'react';
import {createRoot} from 'react-dom/client';
import QRCode from 'qrcode';
import ReviewPanel from './ReviewPanel';
import RemediationPanel,{outcomeLabels} from './RemediationPanel';
import AccountPanel from './AccountPanel';
import AssigneePicker,{canAssign} from './AssigneePicker';
import {accountName,countryLabel,countrySummary,type Account} from './accounts';
import {caseReasons as reasons,caseReasonLabel,recommendationLabel} from './labels';
import {startResourceRequest,type ResourceResult} from './requests';
import '@fontsource/ibm-plex-mono/latin-400.css';
import '@fontsource/ibm-plex-mono/latin-500.css';
import './style.css';

type Session=Account&{state:string;scope:string;has_totp:boolean;has_passkey:boolean;csrf:string;can_manage_data_jobs:boolean};
type Case={generation?:number;is_current?:boolean;outcome?:string;waiting?:boolean;missing_materials?:string;next_step?:string;id:string;run_id:string;cancel_id:string;reason:string;status:string;version:number;assignee:string;conclusion:string;source_row:number;sheet:string};
type Row={id:string;invoice:string;stock:string;customer:string;country:string;price:string;quantity:number;time:string;sheet:string;source_row:number;current_remaining?:number;remaining_at_decision?:number;reason?:string;raw_json?:string;duplicate_group?:{original:number;allocated:number;remaining:number;members:{sale_id:string;sheet:string;source_row:number}[]}};
type Page<T>={items:T[];total:number;page:number;page_size:number};
type Confirmation={allocation:{sale_id:string;quantity:number;amount:string;actor:string;batch_id?:string;recommendation_kind?:string;policy_version?:string};sale:Row};
type Evidence={original_cancel?:Row;effective_cancel?:Row;duplicate_rows?:Row[];related_cases?:Case[];remediations?:{id:string;action:string;evidence:string;actor:string;occurred_at:string;before:Row;after:Row}[];current_assessment?:{reason:string;candidate_count_lower_bound:number};case:Case;cancel:Row;decision_evidence:Row[];candidates:Page<Row>;confirmation?:Confirmation;caveat:string};
type Run={run_id:string;scope:string;cleaning_version:string;rule_version:string};
const labels:Record<string,string>={pending:'待处理',in_progress:'处理中',closed:'已关闭',finance:'财务',warehouse:'仓库',admin:'管理员'};
const evidenceReasons:Record<string,string>={qualified:'合格候选',suspected_duplicate_sale:'合格，但销售行疑似重复',remaining_quantity_too_small:'剩余数量不足',sale_not_strictly_earlier:'销售不早于取消'};
const auditActions:Record<string,string>={correct_return:'核实修正退货',supplement_sale:'补录销售',verify_duplicate:'核实重复',wait_materials:'保存待补充',recheck:'重新核查',reassess:'重算',remediation_match:'补充后配对',reclassified:'流转新问题',verified_duplicate:'核实重复排除',created_from_reassessment:'重算生成问题',assign:'派单',start:'开始处理',confirm_candidate:'人工确认候选',close:'关闭',account_deleted_unassign:'账户删除，退回未派单'};
const scopeLabels:Record<string,string>={benchmark:'首表固定一万行',year1:'首年度全部',all:'两个年度全部'};
const emptyFilters={waiting:'',customer:'',stock:'',invoice:'',country:'',status:'',reason:'',assignee:'',date_from:'',date_to:'',sort:'source',direction:'asc'};
const jobLabels:Record<string,string>={queued:'排队等待',running:'正在运行',succeeded:'已完成',failed:'未完成',preparing:'准备官方数据',importing:'导入并校验',reconciling:'对账与保存',published:'已发布',interrupted:'上次运行中断'};
const jobCountLabels:Record<string,string>={rows:'已读取原始行',rows_read:'已读取原始行',raw:'原始行',accepted:'接纳行',excluded:'排除行',accepted_sales:'接纳销售',accepted_cancels:'接纳取消',matched:'初始自动配对',suspected:'疑似',no_original:'当前规则下无合格原单'};
const emptyPage:Page<any>={items:[],total:0,page:1,page_size:50};
let csrf='';
function base64(v:ArrayBuffer){return btoa(String.fromCharCode(...new Uint8Array(v))).replaceAll('+','-').replaceAll('/','_').replaceAll('=','')}
function binary(v:string){return Uint8Array.from(atob(v.replaceAll('-','+').replaceAll('_','/')+'='.repeat((4-v.length%4)%4)),c=>c.charCodeAt(0))}
function credentialJSON(c:PublicKeyCredential){const response=c.response;const data:any={id:c.id,rawId:base64(c.rawId),type:c.type,clientExtensionResults:c.getClientExtensionResults(),authenticatorAttachment:c.authenticatorAttachment};if(response instanceof AuthenticatorAttestationResponse){data.response={clientDataJSON:base64(response.clientDataJSON),attestationObject:base64(response.attestationObject),transports:response.getTransports()}}else{const r=response as AuthenticatorAssertionResponse;data.response={clientDataJSON:base64(r.clientDataJSON),authenticatorData:base64(r.authenticatorData),signature:base64(r.signature),userHandle:r.userHandle?base64(r.userHandle):null}}return data}
async function api(path:string,body?:unknown,signal?:AbortSignal,method?:string){const r=await fetch('/api'+path,{method:method||(body===undefined?'GET':'POST'),headers:body===undefined?{}:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:body===undefined?undefined:JSON.stringify(body),credentials:'same-origin',signal});const value=await r.json();if(!r.ok)throw new Error(value.message||value.code||'请求未完成');return value}
function useResource<T>(key:string,load:(signal:AbortSignal)=>Promise<T>):ResourceResult<T>{
 const [state,setState]=useState<{key:string;result:ResourceResult<T>}>({key:'',result:{loading:false}});
 // The key includes account, query and refresh generation. Hide an old result
 // during the render before effect cleanup, as well as aborting its request.
 useEffect(()=>{if(!key)return;return startResourceRequest(load,result=>setState({key,result}))},[key]);
 return state.key===key?state.result:{loading:!!key};
}
function CandidateComparison({rows,confirmation,canConfirm,disabled,onChoose}:{rows:Row[];confirmation?:Confirmation;canConfirm:boolean;disabled:boolean;onChoose:(row:Row)=>void}){
 const selectedID=confirmation?.allocation.sale_id;
 const compared=confirmation?[confirmation.sale,...rows.filter(x=>x.id!==selectedID)]:rows;
 const fields:[keyof Row,string][]=[['invoice','单号'],['time','销售时间'],['customer','客户'],['stock','商品'],['price','单价'],['country','国家/地区'],['quantity','数量'],['current_remaining','当前余量']];
 const differs=(key:keyof Row)=>new Set(compared.map(x=>String(x[key]??''))).size>1;
 const value=(row:Row,key:keyof Row)=>key==='time'?row.time?.replace('T',' ').replace(/\.0+$/,''):key==='current_remaining'&&row.duplicate_group?<>{row.current_remaining}<small>重复组余量 {row.duplicate_group.remaining}／容量 {row.duplicate_group.original}</small></>:String(row[key]??'—');
 const selection=(row:Row)=>row.id===selectedID?<span className="confirmation-label">已确认</span>:<button className="secondary" disabled={disabled||!!confirmation||!canConfirm} title={confirmation?'本取消行已确认销售，不能再选择其他候选':!canConfirm?'仅当前责任人在处理中可确认候选':undefined} onClick={()=>onChoose(row)}>{confirmation?'不可再选':canConfirm?'确认候选':'暂不可选'}</button>;
 const stateClass=(row:Row)=>row.id===selectedID?'candidate-chosen':confirmation?'candidate-disabled':'';
 if(!compared.length)return null;
 // Small sets read side by side without hiding quantities behind a horizontal
 // scroll. Larger pages retain one sale per row for efficient scanning.
 if(compared.length<=4)return <><p className="muted comparison-hint">每列一条销售；浅色底标出候选之间不同的字段。</p><div className="candidate-comparison comparison-columns" role="region" aria-label="销售候选字段对比" tabIndex={0}><table>
  <thead><tr><th scope="col">对比字段</th>{compared.map(x=><th scope="col" key={x.id} className={stateClass(x)}><span className="mono">{x.id}</span><small>{x.sheet}<br/>原行 {x.source_row}</small></th>)}</tr></thead>
  <tbody>{fields.map(([key,label])=><tr key={key}><th scope="row">{label}</th>{compared.map(x=><td key={x.id} className={stateClass(x)+(differs(key)?' comparison-difference':'')} title={String(x[key]??'')}>{value(x,key)}</td>)}</tr>)}
   <tr><th scope="row">选择状态</th>{compared.map(x=><td key={x.id} className={stateClass(x)}>{selection(x)}</td>)}</tr>
  </tbody>
 </table></div></>;
 return <><p className="muted comparison-hint">每行一条销售；浅色底标出本页不同的字段。可横向滚动查看全部列。</p><div className="candidate-comparison" role="region" aria-label="销售候选字段对比" tabIndex={0}><table>
  <thead><tr><th scope="col">销售 / 来源</th>{fields.map(([key,label])=><th scope="col" key={key} className={['price','quantity','current_remaining'].includes(key)?'num':''}>{label}</th>)}<th scope="col">选择状态</th></tr></thead>
  <tbody>{compared.map(x=>{const chosen=x.id===selectedID;return <tr key={x.id} className={chosen?'candidate-chosen':confirmation?'candidate-disabled':''}>
   <th scope="row"><span className="mono">{x.id}</span><small>{x.sheet} · 原行 {x.source_row}</small></th>
   {fields.map(([key])=><td key={key} className={(differs(key)?'comparison-difference ':'')+(['price','quantity','current_remaining'].includes(key)?'num':'')} title={String(x[key]??'')}>{value(x,key)}</td>)}
   <td>{selection(x)}</td>
  </tr>})}</tbody>
 </table></div></>;
}
function App(){
 const [session,setSession]=useState<Session|null>(null),[loading,setLoading]=useState(true),[pendingActions,setPendingActions]=useState(0),[error,setError]=useState(''),[message,setMessage]=useState('');
 const busy=pendingActions>0;const sessionEpoch=useRef(0),jobGeneration=useRef(0),jobIDRef=useRef(''),conclusionKey=useRef('');
 const detailCache=useRef<{owner:string;id:string;value:Evidence}|null>(null);
 const [caseNotice,setCaseNotice]=useState<{id:string;error?:string;message?:string}|null>(null),[candidateChoice,setCandidateChoice]=useState<{id:string;row:Row;version:number}|null>(null);
 const [id,setID]=useState(''),[password,setPassword]=useState(''),[passwordAgain,setPasswordAgain]=useState(''),[showPassword,setShowPassword]=useState(false),[otp,setOTP]=useState(''),[recover,setRecover]=useState(false),[recoveryCode,setRecoveryCode]=useState('');
 const [existingLogin,setExistingLogin]=useState(false),[setupRevision,setSetupRevision]=useState(0);
 const [enrollment,setEnrollment]=useState<{secret:string;uri:string;qr:string}|null>(null),[codes,setCodes]=useState<string[]>([]),[saved,setSaved]=useState(false);
 const [runID,setRunID]=useState(''),[tab,setTab]=useState('cases'),[page,setPage]=useState(1);
 const [filters,setFilters]=useState(emptyFilters),[applied,setApplied]=useState(emptyFilters),[activeCase,setActiveCase]=useState<{id:string;page:number}|null>(null),[assignee,setAssignee]=useState(''),[conclusion,setConclusion]=useState(''),[job,setJob]=useState<any>(null),[scope,setScope]=useState('year1');
 const [revision,setRevision]=useState(0),[queryRevision,setQueryRevision]=useState(0),[overviewRevision,setOverviewRevision]=useState(0),[runsRevision,setRunsRevision]=useState(0),[detailRevision,setDetailRevision]=useState(0),[usersRevision,setUsersRevision]=useState(0),[countriesRevision,setCountriesRevision]=useState(0);
 const complete=session?.scope==='business'&&session.state==='complete';const finance=complete&&session.role!=='warehouse';
 const canManageJobs=complete&&session.can_manage_data_jobs;
 const owner=complete?JSON.stringify([session.id,session.role,session.csrf]):'';
 const runtimeResource=useResource<{runtime_id:string}>('runtime-identity',signal=>api('/health',undefined,signal));
 const jobStorageKey=canManageJobs&&runtimeResource.value?.runtime_id?'recon-job:'+JSON.stringify([runtimeResource.value.runtime_id,session?.id,session?.role]):'';
 function resetWorkspace(){sessionEpoch.current++;jobGeneration.current++;jobIDRef.current='';detailCache.current=null;setCaseNotice(null);setCandidateChoice(null);setRunID('');setTab('cases');setPage(1);setFilters(emptyFilters);setApplied(emptyFilters);setActiveCase(null);conclusionKey.current='';setConclusion('');setAssignee('');setJob(null);setScope('year1');setMessage('');setError('')}
 function accept(value:any){if(value.session){if(session?.id!==value.session.id||session?.scope!==value.session.scope||session?.csrf!==value.session.csrf)resetWorkspace();csrf=value.session.csrf;setSession(value.session)}if(value.recovery_codes){setCodes(value.recovery_codes);setSaved(false)}setPassword('');setPasswordAgain('');setShowPassword(false);setOTP('');setRecoveryCode('');setEnrollment(null)}
 async function action(fn:()=>Promise<unknown>,caseID?:string){const epoch=sessionEpoch.current;setPendingActions(n=>n+1);if(caseID)setCaseNotice(null);else{setError('');setMessage('')}try{await fn()}catch(e){if(epoch===sessionEpoch.current){const error=e instanceof Error?e.message:'操作未完成';if(caseID)setCaseNotice({id:caseID,error});else setError(error)}}finally{setPendingActions(n=>n-1)}}
 function caseAction(fn:()=>Promise<unknown>){return action(fn,activeCase?.id)}
 async function me(){try{const s=await api('/auth/me');csrf=s.csrf;setSession(s)}catch{csrf='';setSession(null)}finally{setLoading(false)}}
 useEffect(()=>{const params=new URLSearchParams(location.hash.slice(1));const token=params.get('bootstrap');if(token){historyReplace();action(async()=>{const value=await api('/auth/bootstrap',{token});accept(value)}).finally(()=>setLoading(false))}else me()},[]);
 const setupResource=useResource<Account[]>(!loading&&!session?'setup-accounts:'+setupRevision:'',signal=>api('/auth/setup/accounts',undefined,signal));
 const setupAccounts=setupResource.value||[];
 const choosingSetup=!session&&!recover&&!existingLogin&&setupAccounts.length>0;
 function historyReplace(){window.history.replaceState(null,'',location.pathname)}
 function query(pageNumber=page){const q=new URLSearchParams({run_id:runID,page:String(pageNumber),page_size:'50'});Object.entries(applied).forEach(([k,v])=>{if(v&&(!['sort','direction'].includes(k)||tab==='transactions'))q.set(k,v)});return q.toString()}
 const runsResource=useResource<Run[]>(owner?JSON.stringify([owner,'runs',runsRevision]):'',signal=>api('/runs',undefined,signal));
 const usersResource=useResource<Account[]>(finance?JSON.stringify([owner,'users',usersRevision]):'',signal=>api('/users',undefined,signal));
 const countriesResource=useResource<string[]>(owner?JSON.stringify([owner,'countries',countriesRevision]):'',signal=>api('/countries',undefined,signal));
 const runs=runsResource.value||[],users=usersResource.value||[];
 useEffect(()=>{if(usersResource.value)setAssignee(current=>usersResource.value!.some(u=>u.id===current)?current:'')},[usersResource.value]);
 useEffect(()=>{setAssignee('')},[activeCase?.id]);
 useEffect(()=>{if(runsResource.value)setRunID(current=>runsResource.value!.some(r=>r.run_id===current)?current:runsResource.value![0]?.run_id||'')},[runsResource.value]);
 const table=['cases','transactions','allocations'].includes(tab)?tab:'';
 const queryString=query();
 const dataResource=useResource<Page<any>>(owner&&runID&&table?JSON.stringify([owner,table,queryString,revision,queryRevision]):'',signal=>api('/'+table+'?'+queryString,undefined,signal));
 // Summary is independent of the page, filters and tab. A successful mutation,
 // published task, account/run change or explicit refresh invalidates it.
 const overviewResource=useResource<any>(owner&&runID?JSON.stringify([owner,'overview',runID,revision,overviewRevision]):'',signal=>api('/overview?run_id='+encodeURIComponent(runID),undefined,signal));
 const data=dataResource.value||emptyPage,overview=overviewResource.value;
 const detailResource=useResource<Evidence>(owner&&activeCase?JSON.stringify([owner,'evidence',activeCase.id,activeCase.page,revision,detailRevision]):'',signal=>api('/cases/'+activeCase!.id+'/evidence?page='+activeCase!.page+'&page_size=50',undefined,signal));
 const auditResource=useResource<any[]>(owner&&activeCase?JSON.stringify([owner,'audit',activeCase.id,revision,detailRevision]):'',signal=>api('/cases/'+activeCase!.id+'/audit',undefined,signal));
 if(detailResource.value&&activeCase)detailCache.current={owner,id:activeCase.id,value:detailResource.value};
 // Keep the same drawer mounted while its server version refreshes. Ownership
 // and case identity must still match before reusing any previous evidence.
 const selected=detailResource.value||(detailCache.current?.owner===owner&&detailCache.current?.id===activeCase?.id?detailCache.current.value:undefined),history=auditResource.value||[];
 useEffect(()=>{if(!selected)return;const key=JSON.stringify([owner,selected.case.id]);if(key!==conclusionKey.current){conclusionKey.current=key;setConclusion(selected.case.conclusion||'')}},[selected,owner]);
 useEffect(()=>{
  if(!owner||!job||['succeeded','failed'].includes(job.status))return;
  const controller=new AbortController();let timer:number|undefined;const epoch=sessionEpoch.current,generation=jobGeneration.current,targetID=job.id;
  async function poll(){try{const v=await api('/jobs/'+job.id,undefined,controller.signal);if(controller.signal.aborted||epoch!==sessionEpoch.current||generation!==jobGeneration.current||jobIDRef.current!==targetID)return;acceptJob(v);if(!['succeeded','failed'].includes(v.status)){timer=window.setTimeout(poll,1200)}}catch(e){if(!controller.signal.aborted&&epoch===sessionEpoch.current&&generation===jobGeneration.current&&jobIDRef.current===targetID)setError(e instanceof Error?e.message:'任务进度查询未完成')}}
  timer=window.setTimeout(poll,1200);return()=>{clearTimeout(timer);controller.abort()};
 },[owner,job?.id,job?.status,jobGeneration.current]);
 async function passkey(register:boolean,legacyID?:string){if(!window.PublicKeyCredential||!window.isSecureContext)throw new Error('此浏览器无法使用Passkey；可选择已绑定的认证器，或在获准配置流程中绑定认证器。');const begin=await api('/auth/passkeys/'+(register?'register':'login')+'/begin',register?{id:session?.id}:legacyID?{id:legacyID}:{});csrf=begin.csrf;const publicKey=begin.options.publicKey;publicKey.challenge=binary(publicKey.challenge);if(register){publicKey.user.id=binary(publicKey.user.id);publicKey.excludeCredentials?.forEach((v:any)=>v.id=binary(v.id))}else publicKey.allowCredentials?.forEach((v:any)=>v.id=binary(v.id));let credential:PublicKeyCredential|null;try{credential=(register?await navigator.credentials.create({publicKey}):await navigator.credentials.get({publicKey,mediation:'required'})) as PublicKeyCredential|null}catch(e){if(e instanceof DOMException&&e.name==='NotAllowedError')throw new Error('未完成Passkey验证，可重新选择凭据或使用其他已绑定方式。');throw e}if(!credential)throw new Error('已取消Passkey验证');accept(await api('/auth/passkeys/'+(register?'register':'login')+'/finish?challenge_id='+encodeURIComponent(begin.challenge_id),credentialJSON(credential)))}
 async function enroll(){const value=await api('/auth/totp/enroll',{});setEnrollment({...value,qr:await QRCode.toDataURL(value.uri,{width:220,margin:2})})}
 function openCase(c:Case,candidatePage=1){if(c.id!==activeCase?.id){setCaseNotice(null);setCandidateChoice(null)}setActiveCase({id:c.id,page:candidatePage})}
 function closeCase(){setActiveCase(null);detailCache.current=null;setCaseNotice(null);setCandidateChoice(null);conclusionKey.current='';setConclusion('')}
 async function mutate(kind:string,body:any,sale?:Row){if(!selected)return;const epoch=sessionEpoch.current;const result=await api('/cases/'+selected.case.id+'/'+kind,body);if(epoch!==sessionEpoch.current)return;
  if(detailCache.current?.id===selected.case.id){const next={...selected,case:result.case||result};if(kind==='confirm-match'&&sale&&result.allocation)next.confirmation={allocation:result.allocation,sale:{...sale,current_remaining:(sale.current_remaining??0)-result.allocation.quantity}};detailCache.current={owner,id:selected.case.id,value:next}}
  setRevision(n=>n+1);setCaseNotice({id:selected.case.id,message:kind==='confirm-match'?`已确认销售 ${sale?.id||body.sale_row_id}，分配数量 ${result.allocation?.quantity??Math.abs(selected.cancel.quantity)}。仍可填写核查结论，完成后关闭差异。`:kind==='start'?'已开始处理，可以确认候选并填写核查结论。':kind==='remediations'?result.message:kind==='close'?'结论已保存，差异已关闭。已关闭差异按规则只读。':'派单已保存。'})}
 function acceptJob(value:any){jobIDRef.current=value.id;setJob(value);if(value.status==='succeeded'){setRunsRevision(n=>n+1);setRunID(value.run_id);setPage(1);setActiveCase(null);setRevision(n=>n+1)}}
 async function createJob(){const epoch=sessionEpoch.current,generation=++jobGeneration.current;const value=await api('/jobs',{scope});if(epoch===sessionEpoch.current&&generation===jobGeneration.current)acceptJob(value)}
 const jobProgress={...(job?.progress||{}),...(job?.progress?.input||job?.progress?.stats||{})};
 useEffect(()=>{if(!jobStorageKey)return;let stored:string|null=null;try{stored=sessionStorage.getItem(jobStorageKey)}catch{}if(!stored)return;const generation=jobGeneration.current;return startResourceRequest(signal=>api('/jobs/'+encodeURIComponent(stored!),undefined,signal),result=>{if(generation!==jobGeneration.current)return;if(result.value)acceptJob(result.value);if(result.error)setError('上次任务查询未完成：'+result.error)})},[owner,jobStorageKey]);
 useEffect(()=>{if(jobStorageKey&&job?.id)try{sessionStorage.setItem(jobStorageKey,job.id)}catch{}},[owner,jobStorageKey,job?.id]);
 const generated=()=>{const bytes=crypto.getRandomValues(new Uint8Array(16));const value=base64(bytes.buffer);setPassword(value);setPasswordAgain(value);setShowPassword(true)};
 const logout=()=>action(async()=>{await api('/auth/logout',{});try{if(jobStorageKey)sessionStorage.removeItem(jobStorageKey)}catch{}resetWorkspace();csrf='';setSession(null);setCodes([]);setSaved(false);setEnrollment(null);setPassword('');setPasswordAgain('');setOTP('');setRecoveryCode('');setID('');setRecover(false);setShowPassword(false);setExistingLogin(false);setSetupRevision(n=>n+1)});
 const tabTitles:Record<string,string>={cases:'差异处理',transactions:'交易检索',allocations:'配对明细',jobs:'数据任务',security:'账户安全',accounts:'账户管理',requests:'仓库申请'};
 const tabHints:Record<string,string>={cases:'按差异原因核实补充，重算后完成配对或流转新问题。',transactions:'在当前运行中按条件检索原始交易，排序和分页由服务端执行。',allocations:'查看取消与销售之间已保存的数量分配。',jobs:'准备官方数据并运行对账，完成后发布为新运行。',security:'管理密码、Passkey、认证器与恢复码。',accounts:'创建财务与仓库账户，设置负责国家/地区并审批仓库申请。',requests:'申请负责国家/地区内的仓库账户，查看管理员审批结果。'};
 const navItems:[string,string][]=[['cases','差异处理'],['transactions','交易检索'],['allocations','配对明细'],...(canManageJobs?[['jobs','数据任务'] as [string,string]]:[]),...(session?.role==='admin'?[['accounts','账户管理'] as [string,string]]:session?.role==='finance'?[['requests','仓库申请'] as [string,string]]:[]),['security','账户安全']];
 const pageCount=Math.max(1,Math.ceil(data.total/50));
 const appliedCount=Object.entries(applied).filter(([k,v])=>v&&!['sort','direction'].includes(k)).length;
 const draftChanged=JSON.stringify(filters)!==JSON.stringify(applied);
 const columns:[string,string][]=tab==='cases'?[['源记录',''],['差异原因',''],['责任人',''],['状态',''],['版本','num'],['操作','act']]:tab==='transactions'?[['源记录',''],['单号',''],['交易时间',''],['客户',''],['商品',''],['数量','num'],['单价','num'],['国家/地区','']]:[['取消源记录',''],['销售源记录',''],['数量','num'],['精确金额','num'],['方式',''],['操作人','']];
 const caseCounts=overview?.cases;
 const caseShare=caseCounts&&caseCounts.total>0?[['pending',caseCounts.pending],['in_progress',caseCounts.in_progress],['closed',caseCounts.closed]].reduce<{key:string;x:number;w:number}[]>((acc,[key,value])=>{const x=acc.length?acc[acc.length-1].x+acc[acc.length-1].w:0;acc.push({key:String(key),x,w:Number(value)/caseCounts.total*100});return acc},[]):[];
 const count=(v:unknown)=>typeof v==='number'?v.toLocaleString('zh-CN'):String(v??'—');
 // Unknown codes stay visible with a Chinese fallback instead of guessing their meaning.
 const reasonName=caseReasonLabel;
 const scopeName=(scope:string)=>scopeLabels[scope]||scope;
 if(loading)return <div className="initial"><span className="brand-mark" aria-hidden="true">账</span><p>正在检查会话…</p></div>;
 const firstSetup=<section className="login-panel setup-panel">
  <h2>先为账户设置密码</h2>
  <p className="muted">选择要使用的账户，设置密码后完成安全验证。这里仅显示尚未设置密码的账户。</p>
  <div className="setup-accounts">{setupAccounts.map(account=><button className="setup-account" key={account.id} disabled={busy} onClick={()=>action(async()=>accept(await api('/auth/setup/start',{id:account.id})))}>
   <span className="setup-role">{labels[account.role]}</span>
   <span className="setup-id"><strong>{account.display_name||account.id}</strong><small title={countryLabel(account)}>{account.id} · {countrySummary(account)} · 尚未设置密码</small></span>
   <span className="setup-go">开始配置</span>
  </button>)}</div>
  <p className="scope-note">直接选择即可开始，无需初始化链接，也没有15分钟截止时间。</p>
  <div className="setup-footer"><span className="muted">已有密码，或需要继续配置？</span><button className="text-button" disabled={busy} onClick={()=>{setExistingLogin(true);setError('')}}>登录已有账户</button></div>
 </section>;
 const security=<section className="security-panel">
  <h2>{session?.state==='new'?'为账户设置密码':'完成安全配置'}</h2>
  <p className="muted">当前账户 <strong>{session?accountName(session):''}</strong>{session?.display_name&&session.display_name!==session.id&&<> <span className="mono">{session.id}</span></>}（{labels[session?.role||'']}{session?' · '+countrySummary(session):''}）。完成以下三步后即可使用。</p>
  <ol className="steps"><li className={session?.state!=='new'?'done':''}><span>强密码</span></li><li className={session?.has_passkey||session?.has_totp?'done':''}><span>验证Passkey或认证器</span></li><li className={complete?'done':''}><span>保存恢复码</span></li></ol>
  {session?.state==='new'?<>
   <p className="muted">设置16至128个字符的密码，也可一键生成。请先保存密码，下一步绑定自己的Passkey或认证器。</p>
   <form className="stack-form" onSubmit={e=>{e.preventDefault();action(async()=>{if(password!==passwordAgain)throw new Error('两次输入的密码不一致');accept(await api('/auth/setup/password',{password}))})}}>
    <label>新密码<input autoComplete="new-password" type={showPassword?'text':'password'} value={password} onChange={e=>setPassword(e.target.value)} required/></label>
    <label>确认新密码<input autoComplete="new-password" type={showPassword?'text':'password'} value={passwordAgain} onChange={e=>setPasswordAgain(e.target.value)} required/></label>
    <label className="checkbox"><input type="checkbox" checked={showPassword} onChange={e=>setShowPassword(e.target.checked)}/>显示密码</label>
    <div className="button-row"><button type="button" className="secondary" disabled={busy} onClick={generated}>安全生成密码</button><button disabled={busy||!password||!passwordAgain}>设置密码并继续</button></div>
   </form>
   <button className="text-button" disabled={busy} onClick={logout}>返回选择其他账户</button>
   <p className="scope-note">初始化入口没有时限。若配置会话失效，刷新后重新选择账户；已设密码则使用密码登录续办。</p>
  </>:<>
   <div className="button-row"><button disabled={busy} onClick={()=>action(()=>passkey(true))}>绑定Passkey</button><button className="secondary" disabled={busy||session?.has_totp} onClick={()=>action(enroll)}>绑定认证器TOTP</button></div>
   <p className="muted">使用你自己的设备完成验证。已有Passkey不能验证时，请使用已绑定的认证器或恢复流程。</p>
   {enrollment&&<div className="factor-material">
    <p>用认证器扫码，然后输入当前验证码。绑定资料仅在当前页面显示，请勿截入证据。</p>
    <img src={enrollment.qr} alt="认证器绑定二维码，仅供本人配置"/>
    <details><summary>手动输入密钥</summary><code>{enrollment.secret}</code></details>
    <form className="stack-form" onSubmit={e=>{e.preventDefault();action(async()=>accept(await api('/auth/totp/confirm',{code:otp})))}}><label>认证器验证码<input value={otp} onChange={e=>setOTP(e.target.value)} inputMode="numeric" autoComplete="one-time-code" required/></label><button disabled={busy}>验证绑定</button></form>
   </div>}
   {(codes.length>0||session?.state==='factor')&&<div className="recovery-material"><h3>保存恢复码</h3>{codes.length>0?<>
    <p>每个恢复码只能使用一次。保存在项目外的私人位置，完成后再确认。</p>
    <details><summary>查看并保存10个恢复码</summary><pre>{codes.join('\n')}</pre><button className="secondary" onClick={()=>{const blob=new Blob([codes.join('\n')],{type:'text/plain'});const url=URL.createObjectURL(blob);const a=document.createElement('a');a.href=url;a.download='private-recovery-codes.txt';a.click();URL.revokeObjectURL(url)}}>下载到私人位置</button></details>
    <label className="checkbox"><input type="checkbox" checked={saved} onChange={e=>setSaved(e.target.checked)}/>我已安全保存恢复码</label>
    <button disabled={busy||!saved} onClick={()=>action(async()=>{accept(await api('/auth/setup/complete',{saved}));setCodes([])})}>完成配置并进入</button>
   </>:<>
    <p>恢复码只展示一次。已绑定方式重新验证后，可补发新一组并作废旧组。</p>
    <button className="secondary" disabled={busy} onClick={()=>action(async()=>accept(await api('/auth/recovery/reissue',{})))}>重新生成恢复码</button>
   </>}</div>}
  </>}
 </section>;
 const loginPanel=<section className="login-panel">
  <h2>{recover?'恢复访问':'欢迎回来'}</h2>
  <p className="muted">{recover?'密码和一次性恢复码只用于受限重新绑定。':'使用Passkey，或密码加认证器验证码登录。'}</p>
  <form className="stack-form" onSubmit={e=>{e.preventDefault();action(async()=>accept(await api(recover?'/auth/recover':'/auth/login',recover?{id,password,code:recoveryCode}:{id,password})))}}>
   <label>账户<input autoComplete="username" value={id} onChange={e=>setID(e.target.value)} placeholder="finance / warehouse / admin" required/></label>
   <label>密码<input autoComplete="current-password" type="password" value={password} onChange={e=>setPassword(e.target.value)} required/></label>
   {recover&&<label>一次性恢复码<input type="password" value={recoveryCode} onChange={e=>setRecoveryCode(e.target.value)} required/></label>}
   <button disabled={busy}>{recover?'开始受限恢复':'密码验证并继续'}</button>
  </form>
  {!recover&&<><div className="or-rule"><span>或</span></div><button className="secondary full-width" disabled={busy} onClick={()=>action(()=>passkey(false))}>选择Passkey登录</button><p className="field-hint">无需填写账户或密码。选择已保存的Passkey，验证后自动登录对应账户。</p><details><summary>旧Passkey未出现在列表中？</summary><p className="field-hint">部分旧凭据需要指定账户。填写上方账户后使用兼容入口，无需重置已有凭据。</p><button className="text-button" disabled={busy||!id} onClick={()=>action(()=>passkey(false,id))}>使用指定账户的旧Passkey</button></details></>}
  <button className="text-button" onClick={()=>{setRecover(!recover);setPassword('');setRecoveryCode('')}}>{recover?'返回登录':'认证设备不可用，使用恢复码'}</button>
  {setupResource.loading&&<p className="muted" role="status">正在检查首次配置账户…</p>}
  {setupResource.error&&<p role="alert" className="inline-alert">首次配置账户加载失败：{setupResource.error} <button className="text-button" onClick={()=>setSetupRevision(n=>n+1)}>重试</button></p>}
  {setupAccounts.length>0&&<div className="setup-footer"><span className="muted">还有{setupAccounts.length}个账户尚未设置密码</span><button className="text-button" disabled={busy} onClick={()=>{setRecover(false);setExistingLogin(false);setPassword('');setError('')}}>为新账户设置密码</button></div>}
 </section>;
 const factorPanel=session&&<section className="login-panel">
  <h2>完成因素验证</h2>
  <p className="muted">仅密码验证不开放业务。</p>
  {session.has_totp&&<form className="stack-form" onSubmit={e=>{e.preventDefault();action(async()=>accept(await api('/auth/totp/login',{code:otp})))}}><label>认证器验证码<input inputMode="numeric" autoComplete="one-time-code" value={otp} onChange={e=>setOTP(e.target.value)} required/></label><button disabled={busy}>验证并继续</button></form>}
  {session.has_passkey&&<><button className="secondary full-width" disabled={busy} onClick={()=>action(()=>passkey(false))}>选择Passkey登录</button><p className="field-hint">验证后登录所选Passkey对应的账户。</p><details><summary>旧Passkey未出现在列表中？</summary><button className="text-button" disabled={busy} onClick={()=>action(()=>passkey(false,session.id))}>使用此账户的旧Passkey验证</button></details></>}
  {!session.has_totp&&!session.has_passkey&&<p>恢复状态需要原有效恢复会话。仅密码不能重新取得绑定权限；请按验收说明处理会话遗失。</p>}
 </section>;
 const securityTab=<div className="security-grid">
  <div className="panel"><p className="scope-note">变更认证方式需要5分钟内的完整认证；超过时请重新登录。</p>{security}</div>
  <section className="panel"><h2>更改密码</h2><form className="stack-form" onSubmit={e=>{e.preventDefault();action(async()=>accept(await api('/auth/credentials/change',{password})))}}><label>新的强密码<input type="password" autoComplete="new-password" value={password} onChange={e=>setPassword(e.target.value)} required/></label><button disabled={busy}>验证权限并更改</button></form><button className="text-button" onClick={()=>action(async()=>accept(await api('/auth/recovery/reissue',{})))}>重新生成恢复码</button></section>
 </div>;
 const jobsTab=<section className="panel jobs-panel">
  <h2>准备与对账任务</h2>
  <p className="muted">新任务按真实数据计数更新进度。已有业务运行按数据和规则版本复用，保留人工处理历史。</p>
  <div className="job-create"><label>数据范围<select value={scope} onChange={e=>setScope(e.target.value)}><option value="benchmark">首表固定一万行</option><option value="year1">首年度全部</option><option value="all">两个年度全部</option></select></label><button disabled={busy} onClick={()=>action(createJob)}>创建任务</button></div>
  {job&&<div className={'job-status '+(job.status||'')}>
   <h3 role="status"><span className="job-dot" aria-hidden="true"/>{jobLabels[job.status]||job.status} · {jobLabels[job.stage]||job.stage}</h3>
   <p className="muted">{['queued','running'].includes(job.status)?'可继续检索已发布数据。准备完成后会切换至新运行；请保持服务运行。':'仅成功发布的运行可进入业务查询。'}</p>
   <dl className="job-counts">{Object.entries(jobProgress).filter(([key,value])=>jobCountLabels[key]&&typeof value==='number').map(([key,value])=><div key={key}><dt>{jobCountLabels[key]}</dt><dd>{count(value)}</dd></div>)}</dl>
   <details><summary>任务标识（排查问题时使用）</summary><code>{job.id}</code></details>
   {job.error&&<p role="alert" className="inline-alert">{job.error}</p>}
  </div>}
 </section>;
 const dataTab=<>
  <div className="run-bar">
   <label className="run-select"><span>当前数据范围</span><select aria-label="当前数据运行" value={runID} onChange={e=>{setRunID(e.target.value);setPage(1);closeCase()}}>{runs.map(r=><option key={r.run_id} value={r.run_id}>{scopeName(r.scope)}（清洗 {r.cleaning_version}，规则 {r.rule_version}）</option>)}</select></label>
   <span className={'view-scope '+(finance?'global':'assigned')} title={countryLabel(session!)}>{finance?'负责范围：'+countrySummary(session!,3):'仅显示分派给你的差异及相关证据 · '+countrySummary(session!,3)}</span>
  </div>
  {runsResource.error&&<div role="alert" className="notice error">运行列表加载失败：{runsResource.error}<button onClick={()=>setRunsRevision(n=>n+1)}>重试</button></div>}
  {runID&&<section className="overview" aria-label="当前运行差异统计">
   <div className="overview-head"><span className="muted" role="status">{overviewResource.loading?'正在加载当前运行统计，列表可独立查询。':overviewResource.error?'统计暂不可用；下方列表独立加载。':'统计为最近一次加载结果；筛选仅作用于下方明细。'}</span><button className="text-button" disabled={overviewResource.loading} onClick={()=>setOverviewRevision(n=>n+1)}>刷新统计</button></div>
   {overviewResource.error&&<div role="alert" className="notice error">统计加载失败：{overviewResource.error}</div>}
   {overview&&<>
    <dl className="metrics">{[['total','差异总数',overview.cases.total],['pending','待处理',overview.cases.pending],['in_progress','处理中',overview.cases.in_progress],['closed','已关闭',overview.cases.closed]].map(([key,k,v])=><div key={String(key)} className={'metric '+key}><dt>{k}</dt><dd>{count(v)}</dd></div>)}</dl>
    {caseShare.length>0&&<svg className="share-bar" viewBox="0 0 100 6" preserveAspectRatio="none" role="img" aria-label={`待处理${caseCounts.pending}，处理中${caseCounts.in_progress}，已关闭${caseCounts.closed}，共${caseCounts.total}`}>{caseShare.map(s=><rect key={s.key} className={s.key} x={s.x} y={0} width={s.w} height={6}/>)}</svg>}
   </>}
   {overview&&<p className="muted">待解决且尚未配对 {count(overview.currently_unallocated??0)} 条 · 待补充 {count(overview.cases.waiting??0)} 条 · 补录销售 {count(overview.supplemental_sales??0)} 条 · 核实重复排除 {count(overview.remediation?.verified_duplicate??0)} 条 · 人工批量复核配对 {count(overview.reviewed_allocations??0)} 条；初始自动配对口径另列。</p>}
   <p className="caveat">{overview?.caveat||'匹配只表示规则推断。资料不足时保留原因，不制造关联。'}</p>
   {finance&&overview?.initial_reconciliation&&<details className="conservation"><summary>查看来源、行数守恒与初始配对口径</summary><p>原始 {overview.initial_reconciliation.input.raw} ＝ 接纳 {overview.initial_reconciliation.input.accepted} ＋ 排除 {overview.initial_reconciliation.input.excluded}；初始自动配对 {overview.initial_reconciliation.matched}，疑似 {overview.initial_reconciliation.suspected}，当前规则下无合格原单 {overview.initial_reconciliation.no_original}。人工确认保留初始分类。</p></details>}
  </section>}
  {tab==='cases'&&runID&&<ReviewPanel key={owner+'|'+runID} api={api} runID={runID} finance={!!finance} filters={applied} users={users} usersLoading={usersResource.loading} usersError={usersResource.error} onRetryUsers={()=>setUsersRevision(n=>n+1)} onOpen={openCase} onChanged={()=>{setRevision(n=>n+1);setOverviewRevision(n=>n+1)}}/>}
  <section className="panel table-panel" aria-busy={dataResource.loading||runsResource.loading}>
   <form className="filters" onSubmit={e=>{e.preventDefault();setApplied({...filters});setPage(1);setQueryRevision(n=>n+1)}}>
    <div className="filter-grid">
     {[['customer','客户编号'],['stock','商品编码'],['invoice','交易单号']].map(([k,v])=><label key={k}>{v}<input value={filters[k as keyof typeof filters]} onChange={e=>setFilters({...filters,[k]:e.target.value})}/></label>)}
     <label>国家/地区<select value={filters.country} onChange={e=>setFilters({...filters,country:e.target.value})} disabled={countriesResource.loading||!!countriesResource.error}><option value="">全部国家/地区（本人范围内）</option>{filters.country&&!countriesResource.value?.includes(filters.country)&&<option value={filters.country}>{filters.country}</option>}{(countriesResource.value||[]).map(country=><option key={country} value={country}>{country}</option>)}</select>{countriesResource.loading&&<small role="status">正在读取国家/地区…</small>}</label>
     <label>状态<select value={filters.status} onChange={e=>setFilters({...filters,status:e.target.value})}><option value="">全部</option>{['pending','in_progress','closed'].map(x=><option key={x} value={x}>{labels[x]}</option>)}</select></label>
     <label>补充进度<select value={filters.waiting} onChange={e=>setFilters({...filters,waiting:e.target.value})}><option value="">全部进度</option><option value="1">待补充</option><option value="0">其他进度</option></select></label>
     <label className="wide">差异原因<select value={filters.reason} onChange={e=>setFilters({...filters,reason:e.target.value})}><option value="">全部原因</option>{Object.entries(reasons).map(([k,[v]])=><option key={k} value={k}>{v}</option>)}</select></label>
     <label>起始日期<input type="date" value={filters.date_from} onChange={e=>setFilters({...filters,date_from:e.target.value})}/></label>
     <label>结束日期 不含<input type="date" value={filters.date_to} onChange={e=>setFilters({...filters,date_to:e.target.value})}/></label>
     {tab==='transactions'&&<>
      <label>排序字段<select value={filters.sort} onChange={e=>setFilters({...filters,sort:e.target.value})}>{[['source','来源顺序'],['date','交易时间'],['invoice','交易单号'],['customer','客户编号'],['stock','商品编码'],['country','国家/地区']].map(([k,v])=><option key={k} value={k}>{v}</option>)}</select></label>
      <label>排序方向<select value={filters.direction} onChange={e=>setFilters({...filters,direction:e.target.value})}><option value="asc">升序</option><option value="desc">降序</option></select></label>
     </>}
    </div>
    {countriesResource.error&&<p role="alert" className="inline-alert">国家/地区选项加载失败：{countriesResource.error} <button type="button" className="text-button" onClick={()=>setCountriesRevision(n=>n+1)}>重试</button></p>}
    <div className="filter-actions">
     <span className={'filter-state'+(draftChanged?' changed':'')} role="status">{draftChanged?'条件已修改，点击查询后生效':appliedCount?`已应用 ${appliedCount} 个筛选条件`:'未设置筛选条件'}</span>
     <button type="button" className="secondary" disabled={busy} onClick={()=>setFilters(emptyFilters)}>清空条件</button>
     <button disabled={busy||!runID}>查询</button>
     {finance&&runID&&<a className="button-link secondary export" href={'/api/exports?'+query()}>导出筛选交易CSV</a>}
    </div>
   </form>
   {runsResource.loading&&!runID?<div className="empty" role="status"><h3>正在读取已发布运行…</h3></div>
   :runsResource.error&&!runID?<div className="empty"><h3>运行列表尚未加载</h3><p>请使用上方重试按钮重新查询。</p></div>
   :!runID?<div className="empty"><h3>还没有已发布运行</h3><p>{finance?'在数据任务中准备官方数据后开始对账。':'请等待财务派单。'}</p></div>
   :dataResource.loading?<div className="empty" role="status" aria-live="polite"><span className="spinner" aria-hidden="true"/><h3>正在查询当前页…</h3><p>仍可切换页面或修改筛选条件。</p></div>
   :dataResource.error?<div className="empty" role="alert"><h3>查询未完成</h3><p>{dataResource.error}</p><button className="secondary" onClick={()=>setQueryRevision(n=>n+1)}>重试当前查询</button></div>
   :data.items.length===0?<div className="empty"><h3>当前范围没有记录</h3><p>可调整筛选条件，或等待新的差异派单。</p></div>
   :<div className="table-scroll"><table className={'data-table '+tab}>
    <thead><tr>{columns.map(([x,c])=><th key={x} className={c}>{x}</th>)}</tr></thead>
    <tbody>{data.items.map((x:any)=><tr key={x.id||x.cancel_id} className={tab==='cases'?'row-'+x.status:''}>{tab==='cases'?<>
     <td><span className="mono">{x.cancel_id}</span><small>{x.sheet}{x.source_row?` · 原行 ${x.source_row}`:''}</small></td>
     <td className="reason-cell"><span title={reasons[x.reason]?.[1]}>{reasonName(x.reason)}</span></td>
     <td>{x.assignee||<span className="unassigned">待派单</span>}</td>
     <td><span className={'status '+x.status}>{x.outcome?(outcomeLabels[x.outcome]||labels[x.status]):x.waiting?'待补充':labels[x.status]}</span></td>
     <td className="num">{x.version}</td>
     <td className="act"><button className="row-action" disabled={busy} onClick={()=>openCase(x)}>查看与处理</button></td>
    </>:tab==='transactions'?<>
     <td className="mono">{x.id}</td>
     <td className="mono">{x.invoice}</td>
     <td className="nowrap">{x.time?.replace('T',' ')}</td>
     <td>{x.customer||<span className="unassigned">未提供</span>}</td>
     <td><span className="mono">{x.stock}</span>{x.description&&<small className="clip" title={x.description}>{x.description}</small>}</td>
     <td className={'num'+(x.quantity<0?' negative':'')}>{x.quantity}</td>
     <td className="num">{x.price}</td>
     <td>{x.country||<span className="unassigned">未提供</span>}</td>
    </>:<>
     <td><span className="mono">{x.cancel_id}</span>{x.cancel_sheet&&<small>{x.cancel_sheet} · 原行 {x.cancel_source_row}</small>}</td>
     <td><span className="mono">{x.sale_id}</span>{x.sale_sheet&&<small>{x.sale_sheet} · 原行 {x.sale_source_row}</small>}</td>
     <td className="num">{x.quantity}</td>
     <td className="num">{x.amount}</td>
     <td><span className={'method '+(x.method==='manual'?'manual':'auto')}>{x.method==='manual'?'人工确认':'规则自动'}</span></td>
     <td>{x.actor||'自动规则'}{x.batch_id&&<small>批量复核 · {recommendationLabel(x.recommendation_kind)}<br/><span className="mono">{x.batch_id}</span></small>}</td>
    </>}</tr>)}</tbody>
   </table></div>}
   <footer className="pagination">
    <span className="page-total">{dataResource.value?`共 ${data.total.toLocaleString('zh-CN')} 条 · 每页50条`:dataResource.loading?'正在查询…':'每页50条'}</span>
    <div className="page-steps"><button className="secondary" disabled={busy||dataResource.loading||!dataResource.value||page<=1} onClick={()=>setPage(page-1)}>上一页</button><span>第 {page} 页{dataResource.value?` / 共 ${pageCount.toLocaleString('zh-CN')} 页`:''}</span><button className="secondary" disabled={busy||dataResource.loading||!dataResource.value||page*50>=data.total} onClick={()=>setPage(page+1)}>下一页</button></div>
    <form className="page-jump" key={page} onSubmit={e=>{e.preventDefault();const input=e.currentTarget.elements.namedItem('targetPage') as HTMLInputElement;const target=Number(input.value);if(Number.isSafeInteger(target)&&target>=1&&target<=Math.max(1,Math.ceil(data.total/50)))setPage(target)}}><label>跳至页<input name="targetPage" aria-label="跳至页" type="number" min="1" max={Math.max(1,Math.ceil(data.total/50))} defaultValue={page} required/></label><button className="secondary" disabled={busy||dataResource.loading||!dataResource.value}>跳转</button></form>
   </footer>
  </section>
 </>;
 const drawerHead=(title:React.ReactNode,meta?:React.ReactNode,status?:string)=><header className="drawer-head"><div><h2>{title}</h2>{meta&&<p className="drawer-meta">{meta}</p>}</div><div className="drawer-head-side">{status&&<span className={'status '+status}>{labels[status]}</span>}<button className="icon-button" onClick={closeCase} aria-label="关闭详情">×</button></div></header>;
 const isAssignee=!!selected&&selected.case.assignee===session?.id;
 const caseBusy=busy||detailResource.loading||!!detailResource.error;
 const choice=candidateChoice?.id===activeCase?.id?candidateChoice:null;
 const notice=caseNotice?.id===activeCase?.id?caseNotice:null;
 const drawerFeedback=<div className="drawer-feedback" aria-label="差异操作提示">
  {notice?.error&&<div role="alert" className="notice error">{notice.error}<button onClick={()=>setCaseNotice(null)} aria-label="关闭侧栏错误">×</button></div>}
  {notice?.message&&<div role="status" className="notice">{notice.message}<button onClick={()=>setCaseNotice(null)} aria-label="关闭侧栏提示">×</button></div>}
  {choice&&!selected?.confirmation&&<div role="alert" className="candidate-warning"><p>确认销售 <strong className="mono">{choice.row.id}</strong>？确认后此取消行不能改选其他候选，系统会重新校验身份、时间、余量和版本。</p><div className="button-row"><button disabled={caseBusy} onClick={()=>{setCandidateChoice(null);caseAction(()=>mutate('confirm-match',{version:choice.version,sale_row_id:choice.row.id},choice.row))}}>确认此候选</button><button className="secondary" disabled={busy} onClick={()=>setCandidateChoice(null)}>取消选择</button></div></div>}
  {detailResource.error&&<p role="alert" className="inline-alert">证据刷新失败：{detailResource.error} <button className="text-button" onClick={()=>setDetailRevision(n=>n+1)}>重试证据查询</button></p>}
  {auditResource.error&&<p role="alert" className="inline-alert">操作历史加载失败：{auditResource.error} <button className="text-button" onClick={()=>setDetailRevision(n=>n+1)}>重试</button></p>}
  {finance&&usersResource.error&&<p role="alert" className="inline-alert">责任人列表加载失败：{usersResource.error} <button className="text-button" onClick={()=>setUsersRevision(n=>n+1)}>重试</button></p>}
  {(busy||detailResource.loading)&&<p className="busy" role="status"><span className="spinner" aria-hidden="true"/>{busy?'正在保存操作，请稍候…':'正在刷新差异证据…'}</p>}
 </div>;
 const drawer=complete&&activeCase&&(!selected?<div className="drawer-backdrop"><section className="drawer" aria-label="差异证据与处理">{drawerHead('差异证据')}{drawerFeedback}<div className="drawer-body">{!detailResource.loading&&!detailResource.error&&<p className="muted">证据尚未加载。</p>}</div></section></div>
 :<div className="drawer-backdrop"><section className="drawer" aria-label="差异证据与处理">
  {drawerHead(<>差异 <span className="mono">{selected.case.cancel_id}</span></>,<>{selected.case.sheet} · 原Excel行 {selected.case.source_row} · 版本 {selected.case.version}</>,selected.case.status)}
  {drawerFeedback}
  <div className="drawer-body">
   <dl className="detail-facts">
    <div className="fact-reason"><dt>差异原因</dt><dd>{reasonName(selected.case.reason)}{reasons[selected.case.reason]&&<small>{reasons[selected.case.reason][1]}</small>}</dd></div>
    <div><dt>状态</dt><dd>{labels[selected.case.status]}</dd></div>
    <div><dt>责任人</dt><dd>{selected.case.assignee||'待派单'}</dd></div>
    <div><dt>取消数量</dt><dd className="num">{Math.abs(selected.cancel.quantity)}</dd></div>
    <div><dt>精确单价</dt><dd className="num">{selected.cancel.price}</dd></div>
   </dl>
   <p className="caveat">{selected.caveat}</p>
   {(finance||(isAssignee&&selected.case.status==='pending'))&&<div className="drawer-actions">
    {finance&&<><AssigneePicker key={selected.case.id} users={users} value={assignee} onChange={setAssignee} country={selected.cancel.country} disabled={caseBusy||selected.case.status==='closed'} loading={usersResource.loading} error={usersResource.error} onRetry={()=>setUsersRevision(n=>n+1)}/><button disabled={caseBusy||selected.case.status==='closed'||usersResource.loading||!!usersResource.error||!users.some(u=>u.id===assignee&&canAssign(u,selected.cancel.country))} onClick={()=>caseAction(()=>mutate('assign',{assignee}))}>{selected.case.assignee?'再次派单':'派单'}</button></>}
    {isAssignee&&selected.case.status==='pending'&&<button disabled={caseBusy} onClick={()=>caseAction(()=>mutate('start',{version:selected.case.version}))}>开始处理</button>}
   </div>}
   <RemediationPanel key={selected.case.id+':'+selected.case.version} data={selected} canEdit={isAssignee&&selected.case.status==='in_progress'&&!selected.confirmation} busy={caseBusy} countries={countriesResource.value||[]} onSubmit={body=>caseAction(()=>mutate('remediations',body))} onOpen={c=>openCase(c as Case)}/>
   <h3>决策时证据</h3>
   <p className="muted">只保存支撑原决策的有限证据，不等于全部当前候选。</p>
   {selected.decision_evidence.length?<div className="evidence-list">{selected.decision_evidence.map(x=><div className="evidence-line" key={x.id}><span>销售 <span className="mono">{x.id}</span></span><span className="num-pair"><small>当时余量</small>{x.remaining_at_decision}</span><span className="num-pair"><small>现在余量</small>{x.current_remaining}</span><span className="evidence-reason">{x.reason&&(evidenceReasons[x.reason]||<span className="raw-code">{x.reason}</span>)}</span></div>)}</div>:<p className="muted empty-inline">本差异没有合格的决策时候选证据。</p>}
   {selected.confirmation&&<section className="confirmed-allocation" aria-label="已确认配对"><h3>已确认销售 <span className="mono">{selected.confirmation.allocation.sale_id}</span></h3><p>已分配 <strong>{selected.confirmation.allocation.quantity}</strong> 件 · 精确金额 <span className="mono">{selected.confirmation.allocation.amount}</span> · 操作人 {selected.confirmation.allocation.actor}</p>{selected.confirmation.sale.duplicate_group&&<p>已确认重复组：容量 {selected.confirmation.sale.duplicate_group.original}，已退 {selected.confirmation.sale.duplicate_group.allocated}，有效余量 {selected.confirmation.sale.duplicate_group.remaining}；保留 {selected.confirmation.sale.duplicate_group.members.length} 条来源。</p>}{selected.confirmation.allocation.batch_id&&<p className="muted">批量复核 · {recommendationLabel(selected.confirmation.allocation.recommendation_kind||'')} · {selected.confirmation.allocation.policy_version}<br/><span className="mono">{selected.confirmation.allocation.batch_id}</span></p>}<small>此取消行已完成配对，其余候选不可再选。{selected.case.status==='in_progress'?'仍可编辑下方核查结论，完成后关闭差异。':'已确认结果会保留在此处。'}</small></section>}
   <h3>销售候选对比 <span className="count-badge">当前合格 {selected.candidates.total} 条</span></h3>
   {selected.candidates.total===0&&<p className="muted empty-inline">当前规则下没有合格候选。</p>}
   <CandidateComparison rows={selected.candidates.items} confirmation={selected.confirmation} canConfirm={isAssignee&&selected.case.status==='in_progress'} disabled={caseBusy} onChoose={row=>{setCaseNotice(null);setCandidateChoice({id:selected.case.id,row,version:selected.case.version})}}/>
   {selected.candidates.total>50&&<div className="button-row candidate-pages"><button className="secondary" disabled={caseBusy||selected.candidates.page<=1} onClick={()=>openCase(selected.case,selected.candidates.page-1)}>上一页候选</button><span>第 {selected.candidates.page} 页 / 共 {Math.ceil(selected.candidates.total/50)} 页</span><button className="secondary" disabled={caseBusy||selected.candidates.page*50>=selected.candidates.total} onClick={()=>openCase(selected.case,selected.candidates.page+1)}>下一页候选</button></div>}
   {isAssignee&&selected.case.status==='in_progress'&&!!selected.confirmation&&<form className="closure" onSubmit={e=>{e.preventDefault();caseAction(()=>mutate('close',{version:selected.case.version,conclusion}))}}><label>核查结论<textarea value={conclusion} onChange={e=>setConclusion(e.target.value)} placeholder="填写已确认配对的核查依据与结论。"/></label><small>确认候选后仍可编辑结论；点击下方按钮将结案，结案后按规则不能修改。</small><div className="button-row"><button disabled={caseBusy}>保存结论并关闭</button></div></form>}
   {selected.case.status==='closed'&&<div className="closed-conclusion"><h3>{selected.case.outcome?(outcomeLabels[selected.case.outcome]||'办理结论'):'历史结案'}</h3><p>{selected.case.conclusion}</p><p className="muted">此差异已关闭，不能修改或重新开启。</p></div>}
   <h3>操作历史</h3>
   {auditResource.loading&&<p role="status" className="muted">正在加载操作历史…</p>}
   {!auditResource.loading&&!auditResource.error&&history.length===0&&<p className="muted empty-inline">暂无操作记录。</p>}
   <ol className="audit-list">{history.map(x=><li key={x.id}><strong>{auditActions[x.action]||<span className="raw-code">{x.action}</span>}</strong><small>{x.actor} · {x.occurred_at}<br/>{labels[x.before.status]} v{x.before.version} → {labels[x.after.status]} v{x.after.version}</small>{x.after_evidence?.manual_confirmation&&<p>销售 {x.after_evidence.manual_confirmation.sale_id} · 分配 {x.after_evidence.manual_confirmation.quantity}</p>}</li>)}</ol>
  </div>
 </section></div>);
 return <div className={complete?'app-layout':'auth-layout'}>
  {complete?<aside className="sidebar">
   <div className="brand"><span className="brand-mark" aria-hidden="true">账</span><div>退货对账<small>差异处理工作台</small></div></div>
   <nav aria-label="工作空间"><p className="nav-label">工作空间</p>{navItems.map(([k,v])=><button key={k} className={'nav-item '+(tab===k?'active':'')} aria-current={tab===k?'page':undefined} onClick={()=>{setTab(k);setPage(1);closeCase()}}>{v}</button>)}</nav>
   <div className="sidebar-foot">UCI Online Retail II<br/>规则推断 · 课程模拟</div>
  </aside>:<aside className="auth-intro">
   <div className="brand"><span className="brand-mark" aria-hidden="true">账</span><div>退货对账<small>与差异处理系统</small></div></div>
   <div className="auth-pitch"><h1>让每一项差异<br/>有据可查</h1><p>核对来源，保留歧义，<br/>把核查过程记录到结案。</p></div>
   <p className="source-note">公开交易来自UCI Online Retail II<br/>匹配为规则推断，人工处理为课程模拟</p>
  </aside>}
  <main>
   <header className="topbar">
    <div className="title-block"><h1>{complete?tabTitles[tab]:(choosingSetup||session?.state==='new'?'首次配置':'安全登录')}</h1>{complete&&<p className="page-hint">{tabHints[tab]}</p>}</div>
    <div className="account">{session&&<><span className="account-id"><span className="avatar" aria-hidden="true">{(session.display_name||session.id).slice(0,1).toUpperCase()}</span><span title={countryLabel(session)}>{accountName(session)}<small>{session.id} · {labels[session.role]} · {countrySummary(session)}</small></span></span><button className="secondary compact" disabled={busy} onClick={logout}>退出</button></>}</div>
   </header>
   {!activeCase&&<>{error&&<div role="alert" className="notice error">{error}<button onClick={()=>setError('')} aria-label="关闭错误">×</button></div>}{message&&<div role="status" className="notice">{message}</div>}{busy&&<p className="busy" role="status">正在处理，请稍候…</p>}</>}
   {!complete?<div className="auth-content">{choosingSetup?firstSetup:!session?loginPanel:session.scope==='login_factor'||session.scope==='login_passkey'?factorPanel:security}</div>
   :<div className="workspace">{tab==='accounts'||tab==='requests'?<AccountPanel key={owner} api={api} session={session!} onChanged={()=>{setUsersRevision(v=>v+1);setRevision(v=>v+1)}}/>:tab==='security'?securityTab:tab==='jobs'?jobsTab:dataTab}</div>}
  </main>
  {drawer}
 </div>
}
createRoot(document.getElementById('root')!).render(<App/>);

import React,{useEffect,useRef,useState} from 'react';
import {caseReasonLabel,recommendationUserLabel,recommendationExplain} from './labels';
import {type Account} from './accounts';
import AssigneePicker,{canAssign} from './AssigneePicker';

type API=(path:string,body?:unknown,signal?:AbortSignal)=>Promise<any>;
type Option={id:string;kind:string;sale_id:string;invoice:string;time:string;gap_seconds:number;remaining:number;warning?:string;basket_id?:string;basket_cases?:string[];duplicate?:{capacity:number;allocated:number;remaining:number;canonical_id:string;members:{sale_id:string;sheet:string;source_row:number}[]}};
type Item={case:{id:string;run_id:string;cancel_id:string;reason:string;status:string;version:number;assignee:string;conclusion:string;source_row:number;sheet:string};cancel:{invoice:string;customer:string;stock:string;country:string;quantity:number;time:string;price:string};options:Option[];choice_id:string;has_allocation:boolean;notice?:string};
type Reason={code:string;message:string;count:number};
const actions:Record<string,string>={approve:'复核通过并结案',assign:'批量派单',start:'批量开始',confirm:'批量确认配对',close:'批量结案'};
// What each action does and which items it applies to, matching the server precheck.
const actionHelp:Record<string,string>={
 approve:'对未派单或本人负责的项目，一次完成认领、开始、按所选方案确认配对并结案。',
 assign:'把未派单的待处理项目派给所选责任人。',
 start:'把本人负责的待处理项目改为处理中。',
 confirm:'对本人处理中的项目按所选方案确认配对，不结案。',
 close:'对本人已配对的处理中项目保存结论并结案；未解决项目需打开详情补充资料。',
};
const states:Record<string,string>={pending:'待处理',in_progress:'处理中',closed:'已关闭',ready:'待提交',succeeded:'成功',skipped:'已跳过',failed:'失败'};
const groupings:[string,string][]=[['invoice','退货单'],['customer','客户与国家/地区'],['reason','差异原因'],['stock','商品']];
type Props={api:API;runID:string;finance:boolean;filters:Record<string,string>;users:Account[];usersLoading?:boolean;usersError?:string;onRetryUsers:()=>void;onChanged:()=>void;onOpen:(c:Item['case'])=>void};

function ConstraintBadge({option}:{option:Option|null}){
 if(!option)return <span className="muted">等待明确方案</span>;
 return <div className="constraint-badges">
  {option.duplicate&&<span className="badge badge-duplicate" title="这是重复销售的来源行数；确认后仅按一笔销售共享余量">重复来源 {option.duplicate.members.length} 行</span>}
  {option.basket_id&&<span className="badge badge-basket" title="整单所有待核查明细须一起选择相同整单方案">整单 {option.basket_cases?.length??0} 条退货</span>}
  {!option.duplicate&&!option.basket_id&&<span className="muted">按销售余量校验</span>}
 </div>;
}

function PageCheckbox({state,disabled,onChange}:{state:'none'|'some'|'all';disabled:boolean;onChange:(checked:boolean)=>void}){
 const ref=useRef<HTMLInputElement>(null);
 useEffect(()=>{if(ref.current)ref.current.indeterminate=state==='some'},[state]);
 return <input ref={ref} type="checkbox" aria-label="选择本页全部可处理项目" checked={state==='all'} disabled={disabled} onChange={e=>onChange(e.target.checked)}/>;
}

function relatedLabel(it:any){return it.group_size>1?`与另外 ${it.group_size-1} 条同组处理`:'单独处理'}

function PreviewModal({batch,batchPage,busy,error,opener,onClose,onCommit,onRetry,onShow}:{batch:any;batchPage:number;busy:boolean;error:string;opener:React.RefObject<HTMLElement|null>;onClose:()=>void;onCommit:()=>void;onRetry:()=>void;onShow:(page:number,view:string)=>void}){
 const dialog=useRef<HTMLDialogElement>(null),[closing,setClosing]=useState(false);
 const preview=batch.status==='preview',unfinished=batch.ready+batch.skipped+batch.failed,view:string=batch.view||'';
 const pages=Math.max(1,Math.ceil((batch.view_total??batch.total)/50));
 const tabs:[string,string,string][]=(preview?[['ready','可提交','outcome-ready'],['skipped','将跳过','outcome-skipped'],['failed','失败','outcome-failed']]:[['succeeded','成功','outcome-ready'],['ready','待完成','outcome-pending'],['skipped','已跳过','outcome-skipped'],['failed','失败','outcome-failed']]);
 const shown=tabs.filter(([key])=>batch[key]>0||key===view);
 useEffect(()=>{const el=dialog.current!,overflow=document.body.style.overflow;el.showModal();document.body.style.overflow='hidden';
  // close() restores focus to whatever was focused before opening (possibly a
  // control disabled meanwhile), so return focus to the opener after it.
  return()=>{el.close();document.body.style.overflow=overflow;opener.current?.focus()}},[]);
 useEffect(()=>{const el=dialog.current;if(el&&!el.contains(document.activeElement))el.focus()},[busy,batch.id,batch.status]);
 // Play the exit animation before unmounting; the timer covers a skipped animationend.
 const finished=useRef(false);
 function finish(){if(!finished.current){finished.current=true;onClose()}}
 function requestClose(){if(busy||closing)return;setClosing(true);window.setTimeout(finish,260)}
 function onKeyDown(e:React.KeyboardEvent<HTMLDialogElement>){
  // Chrome closes a modal dialog natively on Escape even when cancel is
  // prevented, which skips the exit animation; handle Escape here first.
  if(e.key==='Escape'){e.preventDefault();requestClose();return}
  if(e.key!=='Tab')return;
  const controls=Array.from(e.currentTarget.querySelectorAll<HTMLElement>('button:not([disabled]),a[href],input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex="0"]'));
  const first=controls[0],last=controls.at(-1),active=document.activeElement;
  if(!first){e.preventDefault();e.currentTarget.focus()}
  else if(e.shiftKey&&(active===first||active===e.currentTarget)){e.preventDefault();last?.focus()}
  else if(!e.shiftKey&&(active===last||active===e.currentTarget)){e.preventDefault();first.focus()}
 }
 const problem=view==='skipped'||view==='failed';
 return <dialog ref={dialog} tabIndex={-1} className={'preview-modal'+(closing?' is-closing':'')} aria-labelledby="batch-preview-title" aria-describedby="batch-preview-summary" onKeyDown={onKeyDown} onAnimationEnd={e=>{if(closing&&e.target===e.currentTarget)finish()}} onCancel={e=>{e.preventDefault();requestClose()}} onClose={finish} onClick={e=>{if(e.target===e.currentTarget)requestClose()}}>
  <div className="modal-content">
   <div className="modal-header">
    <div>
     <h3 id="batch-preview-title">{preview?'批量预览':'批量处理结果'} · {actions[batch.action]}</h3>
     <p id="batch-preview-summary" className="muted">共 {batch.total} 条，{batch.groups??'若干'} 个处理组。同组项目共享整单或销售余量，组内任一项冲突则整组跳过。{batch.readonly>0&&` 另有 ${batch.readonly} 条已关闭，只读，未列入本批。`}</p>
    </div>
    <button className="icon-button" disabled={busy} aria-label="关闭批量详情" onClick={requestClose}>×</button>
   </div>
   <div className="modal-body" aria-busy={busy}>
    {error&&<div role="alert" className="notice error">{error}</div>}
    <div className="outcome-tabs" role="tablist" aria-label="按结果查看">
     {shown.map(([key,label,tone])=><button key={key} role="tab" aria-selected={view===key} className={'outcome-card '+tone} disabled={busy} onClick={()=>{if(view!==key)onShow(1,key)}}><strong>{batch[key]}</strong><span>{label}</span></button>)}
    </div>
    {problem&&batch.reasons?.length>0&&<ul className="reason-summary" aria-label="跳过与失败原因">{batch.reasons.map((r:Reason)=><li key={r.message}><span>{r.message}</span><strong>{r.count} 条</strong></li>)}</ul>}
    {preview&&view==='ready'&&<p className="muted modal-note">建议在提交前不占余量。提交时会重新检查权限、版本和余量，结果可能与预览不同。</p>}
    <div className={'table-scroll'+(busy?' is-loading':'')} role="tabpanel">
     <table className="data-table"><thead><tr><th>退货</th><th>{problem?'原因':'去向与依据'}</th><th>关联处理</th></tr></thead>
      <tbody>{batch.items.map((it:any)=><tr key={it.case_id}>
       <td className="mono">{it.cancel_id}<small>{it.invoice} · {it.stock}</small></td>
       <td>{problem?<span className="row-reason">{it.message||states[it.status]}</span>:it.option?<>{recommendationUserLabel(it.option.kind)} · {it.option.invoice} · {it.option.sale_id}{it.option.duplicate&&<small>{it.option.duplicate.members.length} 条销售源行八字段相同，组容量 {it.option.duplicate.capacity}，组余量 {it.option.duplicate.remaining}</small>}{it.option.warning&&<small>{it.option.warning}</small>}</>:<span className="muted">工作流操作，不新增配对</span>}</td>
       <td>{relatedLabel(it)}</td>
      </tr>)}</tbody>
     </table>
     {!batch.items.length&&<p className="empty-inline">这一类没有项目。</p>}
    </div>
    {pages>1&&<div className="pagination"><span>第 {batchPage} / {pages} 页</span><div className="button-row"><button className="secondary" disabled={busy||batchPage<=1} onClick={()=>onShow(batchPage-1,view)}>上一页</button><button className="secondary" disabled={busy||batchPage>=pages} onClick={()=>onShow(batchPage+1,view)}>下一页</button></div></div>}
   </div>
   <div className="modal-footer">
    <button className="secondary" disabled={busy} onClick={requestClose}>{preview?'调整选择':'返回工作台'}</button>
    {preview?<button disabled={busy||batch.ready===0} onClick={onCommit}>{busy?'正在提交…':`确认提交 ${batch.ready} 条`}</button>:unfinished>0&&<button disabled={busy} onClick={onRetry}>{busy?'正在重新预览…':`仅重新预览未完成 ${unfinished} 条`}</button>}
   </div>
  </div>
 </dialog>
}

export default function ReviewPanel({api,runID,finance,filters,users,usersLoading,usersError,onRetryUsers,onChanged,onOpen}:Props){
 const [enabled,setEnabled]=useState(false),[mode,setMode]=useState('recommendations'),[setID,setSetID]=useState(''),[counts,setCounts]=useState<Record<string,number>>({}),[page,setPage]=useState(1),[data,setData]=useState<{items:Item[];total:number;readonly?:number}>({items:[],total:0});
 const [groupBy,setGroupBy]=useState('invoice'),[groupKey,setGroupKey]=useState<string|null>(null),[groups,setGroups]=useState<any[]>([]),[groupPage,setGroupPage]=useState(1),[groupTotal,setGroupTotal]=useState(0);
 const [all,setAll]=useState(false),[selected,setSelected]=useState<Set<string>>(new Set()),[excluded,setExcluded]=useState<Set<string>>(new Set()),[choices,setChoices]=useState<Record<string,string>>({});
 const [operation,setOperation]=useState('approve'),[assignee,setAssignee]=useState(''),[conclusion,setConclusion]=useState(''),[batch,setBatch]=useState<any>(null),[batchPage,setBatchPage]=useState(1),[busy,setBusy]=useState(false),[loading,setLoading]=useState(false),[error,setError]=useState('');
 useEffect(()=>{setAssignee(current=>users.some(u=>u.id===current)?current:'')},[users]);
 const [showModal,setShowModal]=useState(false);
 const modalOpener=useRef<HTMLElement|null>(null);
 function closeModal(){setShowModal(false)}
 const mounted=useRef(true),queryGeneration=useRef(0);const filterKey=JSON.stringify(filters);const recommended=mode==='recommendations';
 useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;queryGeneration.current++}},[]);
 function clearSelection(){setAll(false);setSelected(new Set());setExcluded(new Set());setChoices({});setBatch(null);setPage(1);setShowModal(false)}
 useEffect(()=>{clearSelection();setGroupKey(null)},[filterKey,mode,groupBy]);
 useEffect(()=>{setAll(false);setSelected(new Set());setExcluded(new Set());setChoices({});setBatch(null);setPage(1);setShowModal(false)},[groupKey]);
 async function work(fn:()=>Promise<void>){setBusy(true);setError('');try{await fn()}catch(e){if(mounted.current)setError(e instanceof Error?e.message:'操作未完成')}finally{if(mounted.current)setBusy(false)}}
 async function generate(signal?:AbortSignal){const v=await api('/review-sets',{run_id:runID},signal);if(mounted.current&&!signal?.aborted){setSetID(v.id);setCounts(v.counts);setSelected(new Set());setExcluded(new Set());setAll(false);setChoices({});setPage(1)}return v.id as string}
 useEffect(()=>{if(!enabled)return;const controller=new AbortController();setBusy(true);setError('');(async()=>{try{await generate(controller.signal);if(controller.signal.aborted)return;const last=await api('/case-batches/latest?run_id='+encodeURIComponent(runID),undefined,controller.signal);if(!controller.signal.aborted&&last.id){setBatch(last);setBatchPage(1)}}catch(e){if(!controller.signal.aborted)setError(e instanceof Error?e.message:'加载未完成')}finally{if(!controller.signal.aborted)setBusy(false)}})();return()=>controller.abort()},[enabled,runID]);
 function params(p:number,withGroup=true){const q=new URLSearchParams({run_id:runID,page:String(p),page_size:'50',recommended_only:String(recommended)});Object.entries(filters).forEach(([k,v])=>{if(v)q.set(k,v)});if(withGroup&&groupKey!==null){q.set('group_by',groupBy);q.set('group_key',groupKey)}return q.toString()}
 // Previous rows stay on screen (dimmed and inert) until the next page arrives,
 // so paging and filtering do not collapse the table.
 useEffect(()=>{if(!enabled||!setID)return;const controller=new AbortController();const generation=++queryGeneration.current;setLoading(true);api('/review-sets/'+setID+'?'+params(page),undefined,controller.signal).then(v=>{if(!controller.signal.aborted&&generation===queryGeneration.current)setData(v)}).catch(e=>{if(!controller.signal.aborted)setError(e.message)}).finally(()=>{if(!controller.signal.aborted&&generation===queryGeneration.current)setLoading(false)});return()=>controller.abort()},[enabled,setID,page,filterKey,mode,groupBy,groupKey]);
 useEffect(()=>{if(!enabled||!setID||mode!=='groups')return;const controller=new AbortController();api('/review-sets/'+setID+'/groups?'+params(groupPage,false)+'&group_by='+groupBy,undefined,controller.signal).then(v=>{if(!controller.signal.aborted){setGroups(v.items);setGroupTotal(v.total)}}).catch(e=>{if(!controller.signal.aborted)setError(e.message)});return()=>controller.abort()},[enabled,setID,mode,groupBy,groupPage,filterKey]);
 function isChecked(id:string){return all?!excluded.has(id):selected.has(id)}
 function toggle(id:string,checked:boolean){setBatch(null);setShowModal(false);if(all){setExcluded(previous=>{const next=new Set(previous);if(checked)next.delete(id);else next.add(id);return next})}else setSelected(previous=>{const next=new Set(previous);if(checked)next.add(id);else next.delete(id);return next})}
 function togglePage(checked:boolean){const ids=data.items.map(it=>it.case.id);setBatch(null);setShowModal(false);if(all)setExcluded(prev=>{const next=new Set(prev);ids.forEach(id=>checked?next.delete(id):next.add(id));return next});else setSelected(prev=>{const next=new Set(prev);ids.forEach(id=>checked?next.add(id):next.delete(id));return next})}
 const n=all?Math.max(0,data.total-excluded.size):selected.size;
 const pageChecked=data.items.filter(it=>isChecked(it.case.id)).length;
 const pageState=!data.items.length||!pageChecked?'none':pageChecked===data.items.length?'all':'some';
 async function preview(retry=false){const activeSet=retry?await generate():setID;const v=await api('/case-batches/preview',retry?{set_id:activeSet,retry_batch_id:batch.id}:{set_id:activeSet,action:operation,filter:{...filters,run_id:runID},recommended_only:recommended,all,ids:[...selected],exclude_ids:[...excluded],choices,assignee,conclusion,group_by:groupKey!==null?groupBy:'',group_key:groupKey??''});if(mounted.current){setBatch(v);setBatchPage(1);setShowModal(true)}}
 async function commit(){const v=await api('/case-batches/'+batch.id+'/commit',{});if(mounted.current){setBatch(v);setBatchPage(1);setShowModal(true);onChanged();await generate()}}
 async function showBatchPage(p:number,view:string){const v=await api('/case-batches/'+batch.id+'?page='+p+(view?'&status='+encodeURIComponent(view):''));if(mounted.current){setBatch(v);setBatchPage(p)}}

 const closedCount=counts.closed||0;
 const clearCount=(counts.unique||0)+(counts.duplicate||0);
 const pendingCount=(counts.basket||0)+(counts.nearest||0)+(counts.tied||0);
 const reviewCount=Math.max(0,(counts.total||0)-closedCount-(counts.recommended||0)-(counts.tied||0));
 const currentStep=batch?3:n?3:2;
 const needsConclusion=['approve','close'].includes(operation);
 const blockedReason=!n?'先在第 2 步勾选项目。':operation==='assign'&&(usersLoading||!!usersError||!users.some(u=>u.id===assignee&&canAssign(u,filters.country||undefined)))?'请先选择负责当前国家/地区范围的派单人员。':needsConclusion&&!conclusion.trim()?'填写共同核查结论后才能预览。':'';
 const groupPages=Math.max(1,Math.ceil(groupTotal/50));

 return <section className="panel review-panel" aria-label="推荐复核与批量处理">
  <div className="review-heading"><div><h2>推荐复核与批量处理</h2><p className="muted">系统整理配对建议，复核后才正式生效。支持跨页选择与分组处理。</p></div><button className="secondary" onClick={()=>setEnabled(!enabled)} aria-expanded={enabled}>{enabled?'收起批量工作台':'打开批量工作台'}</button></div>
  {enabled&&<div className="review-body">
   {error&&!showModal&&<div role="alert" className="notice error">{error}</div>}

   <div className="review-step">
    <div className="review-step-header">
     <span className="step-number">1</span>
     <h3>确定处理范围</h3>
    </div>
    <div className="review-controls">
     <div className="segmented" role="group" aria-label="工作台视图">
      <button aria-pressed={recommended} disabled={busy} onClick={()=>setMode('recommendations')}>按推荐复核</button>
      <button aria-pressed={!recommended} disabled={busy} onClick={()=>setMode('groups')}>按分组处理</button>
     </div>
     <button className="secondary" disabled={busy} onClick={()=>work(async()=>{setBatch(null);setShowModal(false);await generate()})}>重新生成建议</button>
    </div>
    <p className="muted review-mode-note">{recommended?'只列出有配对建议的项目。':'列出全部可处理项目，可按退货单、客户、原因或商品成组选择。'}切换视图、分组或上方筛选会清空当前选择。</p>
    {mode==='groups'&&<div className="review-grouping">
     <label>分组依据<select value={groupBy} disabled={busy} onChange={e=>{setGroupBy(e.target.value);setGroupPage(1)}}>{groupings.map(([v,l])=><option value={v} key={v}>{l}</option>)}</select></label>
     <label>当前组<select value={groupKey??''} disabled={busy} onChange={e=>setGroupKey(e.target.value||null)}><option value="">当前筛选的全部组</option>{groups.map(g=><option key={g.key} value={g.key}>{groupBy==='reason'?caseReasonLabel(g.key):g.label} · {g.count} 条，{g.recommended} 条有建议</option>)}</select></label>
     <div className="group-pager" aria-label="组列表翻页">
      <span>共 {groupTotal} 组{groupPages>1&&` · 第 ${groupPage}/${groupPages} 页`}</span>
      {groupPages>1&&<div className="group-pager-buttons"><button className="secondary compact" disabled={groupPage<=1||busy} aria-label="上一页组" onClick={()=>setGroupPage(groupPage-1)}>‹</button><button className="secondary compact" disabled={groupPage>=groupPages||busy} aria-label="下一页组" onClick={()=>setGroupPage(groupPage+1)}>›</button></div>}
     </div>
    </div>}
    <div className="recommendation-distribution">
     <p className="distribution-label">建议快照 · 可处理 {Math.max(0,(counts.total??0)-closedCount)} 条（点击查看说明）</p>
     <div className="distribution-cards">
      <details className="distribution-card distribution-confirmed">
       <summary><span className="distribution-icon" aria-hidden="true">✓</span><strong>{clearCount} 条</strong><span>明确建议，待复核</span></summary>
       <p>当前唯一 {counts.unique||0} 条 · 完全重复归组 {counts.duplicate||0} 条。人工通过后才正式配对。</p>
       <p>{recommendationExplain('unique')}</p>
       <p>{recommendationExplain('duplicate')}</p>
      </details>
      <details className="distribution-card distribution-pending">
       <summary><span className="distribution-icon" aria-hidden="true">⚠</span><strong>{pendingCount} 条</strong><span>推断或并列，待核实</span></summary>
       <p>整单 {counts.basket||0} 条 · 按时间 {counts.nearest||0} 条 · 最近并列 {counts.tied||0} 条。</p>
       <p>{recommendationExplain('basket')}</p>
       <p>{recommendationExplain('nearest')}</p>
       <p>{recommendationExplain('tied')}</p>
      </details>
      <details className="distribution-card distribution-review">
       <summary><span className="distribution-icon" aria-hidden="true">○</span><strong>{reviewCount} 条</strong><span>保留核查</span></summary>
       <p>没有合格销售建议或已有配对，需要打开详情逐条核查；可批量派单、开始；未解决项目需打开详情补充资料。</p>
      </details>
     </div>
     <small className="muted">按退货行去重，统计本运行中当前账户可见的整个快照，不随筛选变化。{closedCount>0&&`已关闭 ${closedCount} 条只读，不进入工作台。`}未复核的建议不占余量；重新生成会读取最新状态。</small>
    </div>
   </div>

   <div className="review-step" data-status={currentStep===2?'active':undefined} aria-current={currentStep===2?'step':undefined}>
    <div className="review-step-header">
     <span className="step-number">2</span>
     <h3>勾选项目并确认方案</h3>
    </div>
    <div className="review-selection">
     <strong aria-live="polite">已选择 {n} 条{all?'（当前筛选全部，含其他页）':''}</strong>
     <button className="secondary" disabled={busy||loading||!data.total||(all&&!excluded.size)} onClick={()=>{setAll(true);setExcluded(new Set());setSelected(new Set());setBatch(null);setShowModal(false)}}>选择当前筛选全部 {data.total} 条</button>
     <button className="text-button" disabled={busy||!n} onClick={()=>{setAll(false);setSelected(new Set());setExcluded(new Set());setBatch(null);setShowModal(false)}}>清除选择</button>
     {loading&&<span className="inline-status" role="status"><span className="spinner"/>正在整理…</span>}
    </div>
    <p className="muted review-note">徽章只说明当前行的方案约束；跨页整单是否完整、共享余量是否足够，以预览中的处理组和原因为准。{(data.readonly??0)>0&&` 当前筛选另有 ${data.readonly} 条已关闭，只读，不在此列出，可在差异列表查看。`}</p>
    {!setID||(loading&&!data.items.length)?<p className="empty-inline" role="status">正在整理当前页…</p>:<div className={'table-scroll'+(loading?' is-loading':'')} inert={loading||undefined} aria-busy={loading}><table className="data-table review-table"><thead><tr><th><PageCheckbox state={pageState} disabled={busy||loading||!data.items.length} onChange={togglePage}/></th><th>退货与来源</th><th>责任人与状态</th><th>处理建议</th><th>关联约束</th><th>依据与数量</th><th>证据</th></tr></thead><tbody>{data.items.map(it=>{const chosen=choices[it.case.id]??it.choice_id;const option=it.options.find(o=>o.id===chosen)||null;const checked=isChecked(it.case.id);return <tr key={it.case.id} className={checked?'is-selected':undefined}><td><input type="checkbox" aria-label={'选择退货 '+it.case.cancel_id} checked={checked} disabled={busy} onChange={e=>toggle(it.case.id,e.target.checked)}/></td><td><span className="mono">{it.cancel.invoice} · {it.case.cancel_id}</span><small>{it.cancel.customer||'缺客户'} · {it.cancel.country}<br/>{it.cancel.stock} · 退 {Math.abs(it.cancel.quantity)} · 单价 {it.cancel.price}</small></td><td>{it.case.assignee||'待派单'}<small>{states[it.case.status]} · 版本 {it.case.version}</small></td><td>{it.options.length?<label className="review-option-label"><span className="sr-only">退货 {it.case.cancel_id} 的推荐方案</span><select disabled={busy||it.has_allocation} value={chosen} onChange={e=>{setBatch(null);setShowModal(false);setChoices({...choices,[it.case.id]:e.target.value})}}><option value="">请选择配对方案</option>{it.options.map(o=><option key={o.id} value={o.id}>{recommendationUserLabel(o.kind)} · {o.invoice} · {o.sale_id}</option>)}</select></label>:<span className="muted">{it.has_allocation?'已配对':'保留核查'}</span>}{it.notice&&<small>{it.notice}</small>}</td><td className="constraint-cell"><ConstraintBadge option={option}/></td><td>{option&&<><span>销售 {option.time.replace('T',' ')}<br/>距退货 {(option.gap_seconds/86400).toFixed(2)} 天 · 可退 {option.remaining}</span>{option.warning&&<small className="review-warning">{option.warning}</small>}{option.duplicate&&<details><summary>{option.duplicate.members.length} 条源行完全相同 · 组容量 {option.duplicate.capacity}</summary><p>组已退 {option.duplicate.allocated}，组余量 {option.duplicate.remaining}。确认后采用代表销售 {option.duplicate.canonical_id}。</p>{option.duplicate.members.map(m=><small key={m.sale_id}>{m.sheet} · 行 {m.source_row} · {m.sale_id}</small>)}</details>}{option.basket_cases&&<small>整单 {option.basket_cases.length} 条待核查明细须一起选择同一整单方案。</small>}</>}</td><td><button className="row-action" disabled={busy} onClick={()=>onOpen(it.case)}>查看详情</button></td></tr>})}</tbody></table>{!data.total&&!loading&&<p className="empty-inline">当前筛选没有可处理项目。</p>}</div>}
    <div className="pagination"><span>共 {data.total} 条 · 每页 50 条</span><div className="button-row"><button className="secondary" disabled={busy||loading||page<=1} onClick={()=>setPage(page-1)}>上一页</button><span>第 {page} / {Math.max(1,Math.ceil(data.total/50))} 页</span><button className="secondary" disabled={busy||loading||page*50>=data.total} onClick={()=>setPage(page+1)}>下一页</button></div></div>
   </div>

   <div className="review-step" data-status={currentStep===3?'active':undefined} aria-current={currentStep===3?'step':undefined}>
    <div className="review-step-header">
     <span className="step-number">3</span>
     <h3>选择操作并预览</h3>
    </div>
    <div className="review-bulk-actions">
     <label>批量操作<select value={operation} disabled={busy} onChange={e=>{setOperation(e.target.value);setBatch(null);setShowModal(false)}}>{Object.entries(actions).filter(([k])=>finance||k!=='assign').map(([k,v])=><option key={k} value={k}>{v}</option>)}</select></label>
     {operation==='assign'&&<div className="review-assignee"><span>派给</span><AssigneePicker users={users} value={assignee} disabled={busy} loading={usersLoading} error={usersError} onRetry={onRetryUsers} country={filters.country||undefined} batch onChange={id=>{setAssignee(id);setBatch(null);setShowModal(false)}}/></div>}
     <p className="action-help">{actionHelp[operation]}不符合条件的项目会在预览中单独列为“将跳过”并说明原因。</p>
     {needsConclusion&&<label className="review-conclusion">共同核查结论<textarea value={conclusion} disabled={busy} onChange={e=>{setConclusion(e.target.value);setBatch(null);setShowModal(false)}} placeholder="说明复核依据；每条都会保存本结论，结案后不能修改。"/></label>}
     <div className="preview-action">
      <button disabled={busy||loading||!!blockedReason} aria-describedby={blockedReason?'preview-blocked':undefined} onClick={e=>{modalOpener.current=e.currentTarget;void work(()=>preview())}}>预览所选 {n} 条</button>
      {blockedReason&&<small id="preview-blocked" className="muted">{blockedReason}</small>}
     </div>
    </div>
    {batch&&<div className="preview-hint"><p>{batch.status==='preview'?`预览已保存：可提交 ${batch.ready} 条`:`上次处理：成功 ${batch.succeeded} 条`}{batch.skipped>0&&`，跳过 ${batch.skipped} 条`}{batch.failed>0&&`，失败 ${batch.failed} 条`}</p><button className="secondary" disabled={busy} onClick={e=>{modalOpener.current=e.currentTarget;setShowModal(true)}}>查看批量详情</button></div>}
   </div>

   {busy&&!showModal&&<p className="busy" role="status"><span className="spinner"/>正在保存或生成结果，请稍候…</p>}
   {batch&&showModal&&<PreviewModal batch={batch} batchPage={batchPage} busy={busy} error={error} opener={modalOpener} onClose={closeModal} onCommit={()=>work(commit)} onRetry={()=>work(()=>preview(true))} onShow={(p,view)=>work(()=>showBatchPage(p,view))}/>}
  </div>}
 </section>
}

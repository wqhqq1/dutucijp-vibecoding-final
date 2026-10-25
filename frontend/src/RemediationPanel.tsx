import {useRef,useState} from 'react';
import {caseReasonLabel} from './labels';

type Row={id:string;invoice:string;customer:string;country:string;stock:string;price:string;time:string;quantity:number;description?:string;kind?:string;sheet:string;source_row:number};
type Case={id:string;cancel_id:string;status:string;version:number;reason:string;assignee:string;generation?:number;outcome?:string;waiting?:boolean;missing_materials?:string;next_step?:string};
type Evidence={case:Case;cancel:Row;original_cancel?:Row;effective_cancel?:Row;duplicate_rows?:Row[];related_cases?:Case[];remediations?:{id:string;action:string;evidence:string;actor:string;occurred_at:string;before:Row;after:Row}[];current_assessment?:{reason:string;candidate_count_lower_bound:number}};
const fields:[string,string][]=[['customer','客户编号'],['country','国家/地区'],['stock','商品编号'],['price','精确单价'],['time','退货时间'],['quantity','退货数量']];
const actionLabels:Record<string,string>={correct_return:'核实修正退货',supplement_sale:'补录销售原单',verify_duplicate:'核实疑似重复',wait_materials:'保存待补充信息',recheck:'按当前资料重新核查'};
export const outcomeLabels:Record<string,string>={reclassified:'已流转',matched:'已配对办结',verified_duplicate:'核实重复，业务排除',legacy_closed:'历史结案'};
const rowValues=(r:Row):Record<string,string>=>({customer:r.customer,country:r.country,stock:r.stock,price:r.price,time:r.time,quantity:String(Math.abs(r.quantity))});
const canonicalTime=(value:string)=>(value.length===16?value+':00':value).replace(/(\.\d*?[1-9])0+$/,'$1').replace(/\.0+$/,'');
const pickerTime=(value:string)=>canonicalTime(canonicalTime(value).replace(/(\.\d{3})\d+$/,'$1'));
function SourceTimeInput({label,value,disabled,onChange}:{label:string;value:string;disabled:boolean;onChange:(value:string)=>void}){
 return <input aria-label={label} type="datetime-local" step="0.001" required disabled={disabled} value={pickerTime(value)} onChange={e=>onChange(canonicalTime(e.target.value)===pickerTime(value)?value:canonicalTime(e.target.value))}/>;
}

export default function RemediationPanel({data,canEdit,busy,countries,onSubmit,onOpen}:{data:Evidence;canEdit:boolean;busy:boolean;countries:string[];onSubmit:(body:unknown)=>Promise<void>;onOpen:(c:Case)=>void}){
 const row=data.effective_cancel||data.cancel;
 const [action,setAction]=useState(data.case.reason.includes('duplicate')?'verify_duplicate':'correct_return');
 const [values,setValues]=useState(rowValues(row)),[evidence,setEvidence]=useState('');
 const [missing,setMissing]=useState(data.case.missing_materials||''),[next,setNext]=useState(data.case.next_step||'');
 const [sale,setSale]=useState({invoice:'',description:'',quantity:'',time:'',price:row.price,customer:row.customer,country:row.country,stock:row.stock,document_ref:'',line_ref:''});
 const [kind,setKind]=useState(data.case.reason==='suspected_duplicate_cancel'?'cancel':'sale'),[selected,setSelected]=useState<string[]>([]),[canonical,setCanonical]=useState(''),[disposition,setDisposition]=useState('');
 const request=useRef<{key:string;id:string}|null>(null);
 const duplicateRows=(data.duplicate_rows||[]).filter(r=>r.kind===kind);
 const base=rowValues(row),changes=Object.fromEntries(Object.entries(values).filter(([key,value])=>key==='time'?canonicalTime(value)!==canonicalTime(base[key]):value!==base[key]));
 const hasIdentity=!!row.customer&&!!row.country;
 const valid=evidence.trim()&&(action!=='correct_return'||Object.keys(changes).length>0)&&(action!=='wait_materials'||(missing.trim()&&next.trim()))&&(action!=='verify_duplicate'||(selected.length>0&&disposition&&(disposition!=='duplicate'||(selected.length>=2&&canonical))));
 async function submit(){
  const payload={version:data.case.version,action,evidence,...(action==='correct_return'?{changes}:{}),...(action==='supplement_sale'?{sale}:{}),...(action==='verify_duplicate'?{row_ids:selected,disposition,canonical_id:canonical}:{}),...(action==='wait_materials'?{missing_materials:missing,next_step:next}:{})};
  const key=JSON.stringify(payload);if(request.current?.key!==key)request.current={key,id:crypto.randomUUID()};
  await onSubmit({...payload,request_id:request.current!.id});
 }
 return <section className="remediation-panel" aria-label="差异补充与重算">
  <h3>补充资料与处理结果</h3>
  <p className="muted">第 {data.case.generation||1} 轮{data.case.outcome?' · '+(outcomeLabels[data.case.outcome]||data.case.outcome):''}{data.case.waiting?' · 待补充':''}。补充后重新校验身份、时间、重复与余量。</p>
  {data.case.waiting&&<div className="notice"><strong>待补充</strong><p>缺少：{data.case.missing_materials}</p><p>下一步：{data.case.next_step}</p></div>}
  {data.original_cancel&&<details className="remediation-values"><summary>原始值与当前有效值</summary><table className="data-table"><thead><tr><th>字段</th><th>原始值</th><th>有效值</th></tr></thead><tbody>{fields.map(([key,label])=><tr key={key}><th>{label}</th><td>{rowValues(data.original_cancel!)[key]||'未提供'}</td><td>{rowValues(row)[key]||'未提供'}</td></tr>)}</tbody></table></details>}
  {data.current_assessment?.reason&&<p className="muted">最近重算：{data.current_assessment.reason==='unique_qualified_candidate'?'唯一合格候选':data.current_assessment.reason==='verified_duplicate'?'已核实重复':caseReasonLabel(data.current_assessment.reason)} · 合格候选 {data.current_assessment.candidate_count_lower_bound} 条</p>}
  {canEdit&&<form onSubmit={e=>{e.preventDefault();void submit()}}>
   <label>处理方法<select value={action} disabled={busy} onChange={e=>setAction(e.target.value)}>{Object.entries(actionLabels).map(([key,label])=><option key={key} value={key} disabled={(key==='supplement_sale'&&!hasIdentity)||(key==='verify_duplicate'&&!data.duplicate_rows?.length)}>{label}</option>)}</select></label>
   {action==='correct_return'&&<><p className="muted">仅提交有依据的改动；退货数量填写正整数，时间采用来源记录的时间。</p><div className="remediation-grid">{fields.map(([key,label])=><label key={key}>{label}{key==='country'?<select value={values.country} disabled={busy} onChange={e=>setValues({...values,country:e.target.value})}><option value="">未提供</option>{Array.from(new Set([...countries,...(row.country?[row.country]:[])])).map(c=><option key={c} value={c}>{c}</option>)}</select>:key==='time'?<SourceTimeInput label={label} value={values.time} disabled={busy} onChange={time=>setValues({...values,time:pickerTime(time)===pickerTime(row.time)?row.time:time})}/>:<input value={values[key]} disabled={busy} required={!['customer'].includes(key)} maxLength={256} type={key==='quantity'?'number':'text'} min={key==='quantity'?1:undefined} step={key==='quantity'?1:undefined} onChange={e=>setValues({...values,[key]:e.target.value})}/>}</label>)}</div></>}
   {action==='supplement_sale'&&<><p className="muted">录入实际销售凭证，身份与单价沿用已核实退货。已有同凭证明细不可重复补录。</p><div className="remediation-grid">{([['invoice','销售单号'],['document_ref','凭证编号'],['line_ref','明细号'],['quantity','销售数量'],['time','销售时间'],['description','商品描述（可选）']] as const).map(([key,label])=><label key={key}>{label}{key==='time'?<SourceTimeInput label={label} value={sale.time} disabled={busy} onChange={time=>setSale({...sale,time})}/>:<input value={sale[key]} required={key!=='description'} maxLength={key==='description'?4000:256} disabled={busy} type={key==='quantity'?'number':'text'} min={key==='quantity'?1:undefined} step={key==='quantity'?1:undefined} onChange={e=>setSale({...sale,[key]:e.target.value})}/>}</label>)}</div><p className="muted">{row.customer} · {row.country} · {row.stock} · 单价 {row.price}</p></>}
   {action==='verify_duplicate'&&<><label>核实对象<select value={kind} disabled={busy} onChange={e=>{setKind(e.target.value);setSelected([]);setCanonical('')}}><option value="cancel">取消记录</option><option value="sale">销售记录</option></select></label><p className="muted">勾选同一组字段相同的疑似重复记录。独立业务须提供区分依据；销售重复采用最早源行为代表，只计一次容量。</p><div className="remediation-values"><table className="data-table"><thead><tr><th>选择</th><th>记录与来源</th><th>单号 / 数量 / 时间</th></tr></thead><tbody>{duplicateRows.map(r=><tr key={r.id}><td><input type="checkbox" disabled={busy} checked={selected.includes(r.id)} aria-label={'核实记录 '+r.id} onChange={e=>{setSelected(e.target.checked?[...selected,r.id]:selected.filter(id=>id!==r.id));setCanonical('')}}/></td><td>{r.id}<small>{r.sheet}{r.source_row>0?' · 行 '+r.source_row:''}</small></td><td>{r.invoice} · {Math.abs(r.quantity)}<small>{r.time}</small></td></tr>)}</tbody></table>{!duplicateRows.length&&<p>当前没有该类疑似重复记录。</p>}</div><label>核实结果<select required value={disposition} disabled={busy} onChange={e=>setDisposition(e.target.value)}><option value="">请选择核实结果</option><option value="duplicate">确认重复，只计一次</option><option value="independent">确认独立业务，分别保留</option></select></label>{disposition==='duplicate'&&<label>保留记录<select required value={canonical} disabled={busy} onChange={e=>setCanonical(e.target.value)}><option value="">请选择保留记录</option>{duplicateRows.filter(r=>selected.includes(r.id)).map(r=><option key={r.id} value={r.id}>{r.id} · {r.invoice}</option>)}</select></label>}</>}
   {action==='wait_materials'&&<><label>缺少的材料<textarea required maxLength={4000} disabled={busy} value={missing} onChange={e=>setMissing(e.target.value)}/></label><label>下一步与跟进方法<textarea required maxLength={4000} disabled={busy} value={next} onChange={e=>setNext(e.target.value)}/></label></>}
   <label>核实依据<textarea required maxLength={8000} disabled={busy} value={evidence} onChange={e=>setEvidence(e.target.value)} placeholder="填写凭证来源、核查事实及本次处理依据。"/></label>
   <button disabled={busy||!valid}>{action==='wait_materials'?'保存待补充信息':'提交补充并重算'}</button>
  </form>}
  {!!data.related_cases?.length&&<div className="problem-chain"><h4>问题流转</h4>{data.related_cases.map(c=><button type="button" className="text-button" key={c.id} disabled={c.id===data.case.id||busy} onClick={()=>onOpen(c)}>第 {c.generation||1} 轮 · {caseReasonLabel(c.reason)} · {c.outcome?(outcomeLabels[c.outcome]||c.outcome):c.waiting?'待补充':c.status==='pending'?'待处理':'处理中'}{c.status!=='closed'&&!c.assignee?' · 待派单':''}</button>)}</div>}
  {!!data.remediations?.length&&<details><summary>本问题的补充记录（{data.remediations.length}）</summary><ol className="audit-list">{data.remediations.map(r=><li key={r.id}><strong>{actionLabels[r.action]||r.action}</strong><small>{r.actor} · {r.occurred_at}</small><p>{r.evidence}</p></li>)}</ol></details>}
 </section>;
}

import React,{useEffect,useId,useRef,useState} from 'react';
import {accountLabel,accountName,allCountries,countryLabel,type Account} from './accounts';
import {roleLabel} from './labels';

type Props={users:Account[];value:string;onChange:(id:string)=>void;country?:string;disabled?:boolean;loading?:boolean;error?:string;onRetry?:()=>void;batch?:boolean};
export function canAssign(account:Account,country?:string){return country===undefined||allCountries(account)||!!account.countries?.includes(country)}

function Selector({users,value,country,loading,error,onRetry,batch,onChange,onClose,opener}:Props&{onClose:()=>void;opener:React.RefObject<HTMLButtonElement|null>}){
 const dialog=useRef<HTMLDialogElement>(null),search=useRef<HTMLInputElement>(null),title=useId();
 const [query,setQuery]=useState(''),[role,setRole]=useState(''),[page,setPage]=useState(1),[choice,setChoice]=useState(value);
 const filtered=users.filter(u=>(!role||u.role===role)&&[accountName(u),u.id,roleLabel(u.role),countryLabel(u)].some(text=>text.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())));
 const pages=Math.max(1,Math.ceil(filtered.length/20)),current=Math.min(page,pages),rows=filtered.slice((current-1)*20,current*20);
 const selected=users.find(u=>u.id===choice),ready=!!selected&&canAssign(selected,country)&&!loading&&!error;
 useEffect(()=>{const el=dialog.current!,overflow=document.body.style.overflow;el.showModal();document.body.style.overflow='hidden';search.current?.focus();return()=>{el.close();document.body.style.overflow=overflow;opener.current?.focus()}},[]);
 return <dialog ref={dialog} className="assignee-dialog" aria-labelledby={title} onKeyDown={e=>{if(e.key==='Escape'){e.preventDefault();onClose()}}} onCancel={e=>{e.preventDefault();onClose()}} onClose={onClose} onClick={e=>{if(e.target===e.currentTarget)onClose()}}>
  <div className="assignee-dialog-content">
   <header className="modal-header"><div><h3 id={title}>选择派单人员</h3><p className="muted">{country!==undefined?(country?`当前国家：${country}。不负责该国的账户不可选。`:'当前记录未提供国家，仅可选择全部国家范围的账户。'):batch?'可选择当前权限内的责任人；跨国家批次会在预览中逐项核验范围。':'选择责任人后回填，再点击派单保存。'}</p></div><button type="button" className="icon-button" aria-label="关闭人员选择" onClick={onClose}>×</button></header>
   <div className="assignee-dialog-body">
    <div className="assignee-search"><label>搜索人员<input ref={search} type="search" value={query} onChange={e=>{setQuery(e.target.value);setPage(1)}} placeholder="显示名称、登录账户名或国家"/></label><label>角色<select value={role} onChange={e=>{setRole(e.target.value);setPage(1)}}><option value="">全部角色</option>{['warehouse','finance','admin'].map(r=><option key={r} value={r}>{roleLabel(r)}</option>)}</select></label></div>
    {loading&&<p role="status" className="muted">正在读取责任人…</p>}
    {error&&<p role="alert" className="inline-alert">责任人加载失败：{error} {onRetry&&<button type="button" className="text-button" onClick={onRetry}>重试</button>}</p>}
    <p className="muted picker-result-count" role="status">共 {filtered.length} 个匹配账户{choice&&selected?`，已选：${accountName(selected)}`:''}</p>
    <div className="assignee-table-scroll" tabIndex={0} role="region" aria-label="派单人员表格"><table className="data-table"><thead><tr><th>选择</th><th>名称 · 登录账户名</th><th>角色</th><th>负责国家</th><th>可选状态</th></tr></thead><tbody>{rows.map(u=>{const allowed=canAssign(u,country),disabled=!allowed||loading||!!error;return <tr key={u.id} className={(choice===u.id?'is-selected ':'')+(!allowed?'assignee-unavailable':'')} onClick={()=>{if(!disabled)setChoice(u.id)}}><td><input type="radio" name={title} aria-label={'选择 '+accountLabel(u)} checked={choice===u.id} disabled={disabled} onChange={()=>setChoice(u.id)}/></td><td><strong>{accountName(u)}</strong><small className="mono">{u.id}</small></td><td>{roleLabel(u.role)}</td><td className="assignee-countries">{countryLabel(u)}</td><td>{allowed?'可选择':'不负责该国家'}</td></tr>})}</tbody></table></div>
    {!filtered.length&&!loading&&!error&&<p className="empty-inline">{users.length?'没有匹配的人员，请调整搜索或角色。':'当前没有可选责任人。'}</p>}
    {pages>1&&<div className="pagination"><span>第 {current} / {pages} 页 · 每页20人</span><div className="button-row"><button type="button" className="secondary" disabled={current===1} onClick={()=>setPage(current-1)}>上一页</button><button type="button" className="secondary" disabled={current===pages} onClick={()=>setPage(current+1)}>下一页</button></div></div>}
   </div>
   <footer className="modal-footer"><button type="button" className="secondary" onClick={onClose}>取消</button><button type="button" disabled={!ready} onClick={()=>{if(ready){onChange(choice);onClose()}}}>确认选择</button></footer>
  </div>
 </dialog>;
}

export default function AssigneePicker(props:Props){
 const [open,setOpen]=useState(false),opener=useRef<HTMLButtonElement>(null),selected=props.users.find(u=>u.id===props.value);
 return <div className="assignee-picker"><button ref={opener} type="button" className="secondary assignee-trigger" aria-label="选择责任人" aria-haspopup="dialog" disabled={props.disabled} onClick={()=>setOpen(true)}><span>{selected?accountName(selected):'选择派单人员'}{selected&&<small>{selected.id} · {countryLabel(selected)}</small>}</span><span aria-hidden="true">⌕</span></button>{open&&<Selector {...props} opener={opener} onClose={()=>setOpen(false)}/>}</div>;
}

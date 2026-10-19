import React,{useEffect,useId,useRef,useState} from 'react';
import {accountName,countryLabel,type Account} from './accounts';

export type DeletionImpact={account:Account;open_cases:number;closed_cases:number;pending_requests:number;passkey_count?:number;deleted_at?:string;revoked_credential_ids?:string[]};
type API=(path:string,body?:unknown,signal?:AbortSignal,method?:string)=>Promise<any>;
export default function DeleteAccountDialog({account,api,busy,error,opener,onClose,onConfirm}:{account:Account;api:API;busy:boolean;error?:string;opener:React.RefObject<HTMLElement|null>;onClose:()=>void;onConfirm:()=>void}){
 const dialog=useRef<HTMLDialogElement>(null),title=useId(),[confirmation,setConfirmation]=useState(''),[impact,setImpact]=useState<DeletionImpact|null>(null),[loading,setLoading]=useState(true),[loadError,setLoadError]=useState(''),[revision,setRevision]=useState(0);
 useEffect(()=>{const el=dialog.current!,overflow=document.body.style.overflow;el.showModal();document.body.style.overflow='hidden';return()=>{el.close();document.body.style.overflow=overflow;opener.current?.focus()}},[]);
 useEffect(()=>{const controller=new AbortController();setLoading(true);setLoadError('');api('/admin/accounts/'+encodeURIComponent(account.id)+'/deletion-preview',undefined,controller.signal).then(v=>{if(!controller.signal.aborted)setImpact(v)}).catch(e=>{if(!controller.signal.aborted)setLoadError(e instanceof Error?e.message:'无法读取删除影响')}).finally(()=>{if(!controller.signal.aborted)setLoading(false)});return()=>controller.abort()},[account.id,revision]);
 const ready=!!impact&&!loading&&!loadError&&confirmation.trim()===account.id&&!busy;
 return <dialog ref={dialog} className="account-delete-dialog" aria-labelledby={title} onKeyDown={e=>{if(e.key==='Escape'){e.preventDefault();if(!busy)onClose()}}} onCancel={e=>{e.preventDefault();if(!busy)onClose()}} onClose={()=>{if(!busy)onClose()}}>
  <header className="modal-header"><div><h3 id={title}>删除账户</h3><p>{accountName(account)} · <span className="mono">{account.id}</span></p><small>{countryLabel(account)}</small></div><button type="button" className="icon-button" aria-label="关闭删除确认" disabled={busy} onClick={onClose}>×</button></header>
  <div className="delete-dialog-body">
   {loading&&<p role="status">正在核对该账户的工作…</p>}
   {loadError&&<p role="alert" className="inline-alert">{loadError} <button type="button" className="text-button" onClick={()=>setRevision(v=>v+1)}>重试</button></p>}
   {impact&&<dl className="deletion-impact"><div><dt>未办结差异，退回未派单</dt><dd>{impact.open_cases}</dd></div><div><dt>已办结差异，原样保留</dt><dd>{impact.closed_cases}</dd></div>{impact.pending_requests>0&&<div><dt>待审批申请，自动驳回</dt><dd>{impact.pending_requests}</dd></div>}</dl>}
   <p>删除后立即退出该账户并停用密码、Passkey、认证器和恢复码。已保存的配对与审计保留，未办结工作可由新责任人继续处理。</p>
   <p className="muted">登录账户名保留用于历史，不再用于新账户。提交时会重新核对工作数量，以实际处理结果为准。</p>
   <p className="muted">删除后将尝试通知当前浏览器移除或隐藏已作废的Passkey，不保证清理其他设备；不支持时可手动移除。</p>
   <label>输入登录账户名确认删除<input value={confirmation} autoComplete="off" spellCheck={false} disabled={busy} placeholder={account.id} onChange={e=>setConfirmation(e.target.value)}/></label>
   {error&&<p role="alert" className="notice error">{error}</p>}
  </div>
  <footer className="modal-footer"><button type="button" className="secondary" disabled={busy} onClick={onClose}>取消</button><button type="button" className="danger-button" disabled={!ready} onClick={()=>{if(ready)onConfirm()}}>{busy?'正在删除…':'确认删除账户'}</button></footer>
 </dialog>;
}

import React,{useEffect,useRef,useState} from 'react';

type Policy={base_origin:string;origins:string[];version:number};
type API=(path:string,body?:unknown,signal?:AbortSignal,method?:string)=>Promise<any>;
export default function PasskeyDomainsPanel({api,onChanged}:{api:API;onChanged:()=>void}){
 const [policy,setPolicy]=useState<Policy|null>(null),[origins,setOrigins]=useState<string[]>([]),[entry,setEntry]=useState(''),[revision,setRevision]=useState(0),[loading,setLoading]=useState(true),[busy,setBusy]=useState(false),[error,setError]=useState(''),[message,setMessage]=useState('');
 const locked=useRef(false),mounted=useRef(true);
 useEffect(()=>()=>{mounted.current=false},[]);
 useEffect(()=>{const controller=new AbortController();setLoading(true);setError('');api('/admin/passkey-domains',undefined,controller.signal).then((p:Policy)=>{if(!controller.signal.aborted){setPolicy(p);setOrigins(p.origins)}}).catch(e=>{if(!controller.signal.aborted)setError(e instanceof Error?e.message:'许可入口设置读取失败')}).finally(()=>{if(!controller.signal.aborted)setLoading(false)});return()=>controller.abort()},[revision]);
 const current=location.origin,dirty=!!policy&&JSON.stringify(origins)!==JSON.stringify(policy.origins);
 function add(e:React.FormEvent){e.preventDefault();const raw=entry.trim();if(!raw)return;try{const u=new URL(raw.includes('://')?raw:'https://'+raw);if(u.username||u.password||u.search||u.hash||u.pathname!=='/'||!['https:','http:'].includes(u.protocol))throw new Error();if(origins.includes(u.origin)||u.origin===policy?.base_origin){setError('该入口已经在批准列表中。');return}setOrigins(list=>[...list,u.origin]);setEntry('');setError('');setMessage('')}catch{setError('请填写域名或完整入口，例如 https://recon.example.com:8443，不包含路径。')}}
 async function save(){if(locked.current||!policy||!dirty)return;locked.current=true;setBusy(true);setError('');setMessage('');try{const p:Policy=await api('/admin/passkey-domains',{origins,version:policy.version},undefined,'PUT');if(mounted.current){setPolicy(p);setOrigins(p.origins);setMessage('批准入口已保存并立即生效。旧本机入口及所有已登记凭据保留，正在进行的Passkey验证需重新发起。');onChanged()}}catch(e){if(mounted.current)setError(e instanceof Error?e.message:'许可入口设置保存失败')}finally{locked.current=false;if(mounted.current)setBusy(false)}}
 return <section className="panel passkey-domains" aria-labelledby="passkey-domains-title">
  <div className="section-head"><h2 id="passkey-domains-title">许可入口</h2><button type="button" className="secondary compact" disabled={loading||busy} onClick={()=>{setMessage('');setRevision(v=>v+1)}}>重新读取</button></div>
  <p>新旧许可入口可同时使用。Windows 客户端在同一后端的 localhost、IP 和域名之间共用原生 Passkey；网页凭据继续遵循各自的域名规则。</p>
  <p className="muted">可批准完整 HTTP 或 HTTPS 入口，包括本机、局域网 IP 和域名。局域网服务须配置监听；HTTPS 须使用有效证书。这里只批准入口，不会自动配置网络。最多20个额外入口，不支持通配符。</p>
  {loading&&<p role="status">正在读取批准入口…</p>}
  {error&&<p role="alert" className="notice error">{error}</p>}
  {message&&<p role="status" className="notice">{message}</p>}
  {policy&&<>
   <div className="table-wrap"><table><thead><tr><th>批准入口</th><th>网页 Passkey 身份</th><th>操作</th></tr></thead><tbody>
    {[policy.base_origin,...origins].map((origin,i)=><tr key={origin}><td className="mono">{origin}{origin===current&&<small className="muted"> · 当前入口</small>}</td><td className="mono">{new URL(origin).hostname.includes(":")||/^\d[\d.]*$/.test(new URL(origin).hostname)?"原生客户端使用通用身份":new URL(origin).hostname}</td><td>{i===0?<span className="muted">旧本机入口，固定保留</span>:origin===current?<span className="muted">当前入口，需切换后移除</span>:<button type="button" className="row-action delete-account-action" disabled={busy||loading} aria-label={'移除入口 '+origin} onClick={()=>{setOrigins(list=>list.filter(v=>v!==origin));setMessage('')}}>移除</button>}</td></tr>)}
   </tbody></table></div>
   <form className="domain-entry" onSubmit={add}><label htmlFor="passkey-origin">新增批准入口<input id="passkey-origin" value={entry} placeholder="https://recon.example.com" autoComplete="off" disabled={busy||loading} onChange={e=>setEntry(e.target.value)}/></label><button type="submit" className="secondary" disabled={!entry.trim()||origins.length>=20||busy||loading}>加入列表</button></form>
   <p className="muted">移除入口会停止该入口访问，保留其凭据和业务历史；重新批准相同入口后可继续使用。保存会使未完成的Passkey验证失效。</p>
   <div className="domain-save"><button type="button" disabled={!dirty||busy||loading} onClick={()=>void save()}>{busy?'正在保存…':'保存许可入口'}</button><span className="muted">{dirty?'有未保存的修改':'已保存'}</span></div>
  </>}
 </section>;
}

import React,{useEffect,useRef,useState} from 'react';

type Policy={base_origin:string;origins:string[];version:number};
type API=(path:string,body?:unknown,signal?:AbortSignal,method?:string)=>Promise<any>;
export default function PasskeyDomainsPanel({api,onChanged}:{api:API;onChanged:()=>void}){
 const [policy,setPolicy]=useState<Policy|null>(null),[origins,setOrigins]=useState<string[]>([]),[entry,setEntry]=useState(''),[revision,setRevision]=useState(0),[loading,setLoading]=useState(true),[busy,setBusy]=useState(false),[error,setError]=useState(''),[message,setMessage]=useState('');
 const locked=useRef(false),mounted=useRef(true);
 useEffect(()=>()=>{mounted.current=false},[]);
 useEffect(()=>{const controller=new AbortController();setLoading(true);setError('');api('/admin/passkey-domains',undefined,controller.signal).then((p:Policy)=>{if(!controller.signal.aborted){setPolicy(p);setOrigins(p.origins)}}).catch(e=>{if(!controller.signal.aborted)setError(e instanceof Error?e.message:'域名设置读取失败')}).finally(()=>{if(!controller.signal.aborted)setLoading(false)});return()=>controller.abort()},[revision]);
 const current=location.origin,dirty=!!policy&&JSON.stringify(origins)!==JSON.stringify(policy.origins);
 function add(e:React.FormEvent){e.preventDefault();const raw=entry.trim();if(!raw)return;try{const u=new URL(raw.includes('://')?raw:'https://'+raw);if(u.username||u.password||u.search||u.hash||u.pathname!=='/'||!['https:','http:'].includes(u.protocol))throw new Error();if(origins.includes(u.origin)||u.origin===policy?.base_origin){setError('该入口已经在批准列表中。');return}setOrigins(list=>[...list,u.origin]);setEntry('');setError('');setMessage('')}catch{setError('请填写域名或完整入口，例如 https://recon.example.com:8443，不包含路径。')}}
 async function save(){if(locked.current||!policy||!dirty)return;locked.current=true;setBusy(true);setError('');setMessage('');try{const p:Policy=await api('/admin/passkey-domains',{origins,version:policy.version},undefined,'PUT');if(mounted.current){setPolicy(p);setOrigins(p.origins);setMessage('批准入口已保存并立即生效。旧本机入口及所有已登记凭据保留，正在进行的Passkey验证需重新发起。');onChanged()}}catch(e){if(mounted.current)setError(e instanceof Error?e.message:'域名设置保存失败')}finally{locked.current=false;if(mounted.current)setBusy(false)}}
 return <section className="panel passkey-domains" aria-labelledby="passkey-domains-title">
  <div className="section-head"><h2 id="passkey-domains-title">Passkey 批准域名</h2><button type="button" className="secondary compact" disabled={loading||busy} onClick={()=>{setMessage('');setRevision(v=>v+1)}}>重新读取</button></div>
  <p>新旧入口可同时使用。每个域名使用自己的Passkey，旧凭据继续在原域名登录；在新域名先用现有密码和认证器或恢复流程登录，再绑定新Passkey。</p>
  <p className="muted">外网入口须使用有效HTTPS证书，并由你配置DNS和反向代理到本机服务。这里只批准入口，不会自动发布网站。localhost 可使用HTTP。最多20个额外入口，不支持通配符。</p>
  {loading&&<p role="status">正在读取批准入口…</p>}
  {error&&<p role="alert" className="notice error">{error}</p>}
  {message&&<p role="status" className="notice">{message}</p>}
  {policy&&<>
   <div className="table-wrap"><table><thead><tr><th>批准入口</th><th>Passkey RP ID</th><th>操作</th></tr></thead><tbody>
    {[policy.base_origin,...origins].map((origin,i)=><tr key={origin}><td className="mono">{origin}{origin===current&&<small className="muted"> · 当前入口</small>}</td><td className="mono">{new URL(origin).hostname}</td><td>{i===0?<span className="muted">旧本机入口，固定保留</span>:origin===current?<span className="muted">当前入口，需切换后移除</span>:<button type="button" className="row-action delete-account-action" disabled={busy||loading} aria-label={'移除入口 '+origin} onClick={()=>{setOrigins(list=>list.filter(v=>v!==origin));setMessage('')}}>移除</button>}</td></tr>)}
   </tbody></table></div>
   <form className="domain-entry" onSubmit={add}><label htmlFor="passkey-origin">新增批准入口<input id="passkey-origin" value={entry} placeholder="https://recon.example.com" autoComplete="off" disabled={busy||loading} onChange={e=>setEntry(e.target.value)}/></label><button type="submit" className="secondary" disabled={!entry.trim()||origins.length>=20||busy||loading}>加入列表</button></form>
   <p className="muted">移除入口会停止该入口访问，保留其凭据和业务历史；重新批准相同入口后可继续使用。保存会使未完成的Passkey验证失效。</p>
   <div className="domain-save"><button type="button" disabled={!dirty||busy||loading} onClick={()=>void save()}>{busy?'正在保存…':'保存批准域名'}</button><span className="muted">{dirty?'有未保存的修改':'已保存'}</span></div>
  </>}
 </section>;
}

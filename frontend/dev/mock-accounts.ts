// Development-only account management fixtures for `--mode mock`. Mirrors the
// FR-19 HTTP contract (backend/internal/web/accounts.go, internal/auth/accounts.go)
// closely enough to exercise the UI; it is never imported by src/ and never
// reaches dist. All accounts and requests here are invented sample data. Mock
// approval only adds an in-memory record: no password, Passkey or TOTP exists.

export type Profile={id:string;role:string;display_name:string;country_scope:'all'|'selected';countries:string[];permissions_version:number};
type Account=Profile&{state:string};
type Request={id:string;requester:string;account_id:string;display_name:string;country_scope:string;countries:string[];reason:string;status:string;created_at:string;reviewed_by:string;reviewed_at:string;rejection_reason:string;account_state?:string};
type Audit={id:number;actor:string;action:string;target:string;request_id:string;before:unknown;after:unknown;occurred_at:string};
type Reply=(status:number,body:unknown)=>void;

// Country names as they appear in the public Online Retail II source.
export const allCountryNames=['Australia','Austria','Bahrain','Belgium','Brazil','Canada','Channel Islands','Cyprus','Czech Republic','Denmark','EIRE','European Community','Finland','France','Germany','Greece','Iceland','Israel','Italy','Japan','Lebanon','Lithuania','Malta','Netherlands','Norway','Poland','Portugal','RSA','Saudi Arabia','Singapore','Spain','Sweden','Switzerland','USA','United Arab Emirates','United Kingdom','Unspecified'];

const ago=(hours:number)=>new Date(Date.now()-hours*3600000).toISOString();
const sel=(...countries:string[])=>({country_scope:'selected' as const,countries:[...countries].sort()});
const all={country_scope:'all' as const,countries:[] as string[]};
function seedAccounts():Account[]{return [
 {id:'admin',role:'admin',display_name:'系统管理员',...all,permissions_version:1,state:'complete'},
 {id:'finance',role:'finance',display_name:'财务总部',...all,permissions_version:1,state:'complete'},
 {id:'finance-uk',role:'finance',display_name:'英国与法国财务',...sel('United Kingdom','France'),permissions_version:2,state:'complete'},
 {id:'finance-de',role:'finance',display_name:'德国财务',...sel('Germany'),permissions_version:1,state:'recovering'},
 {id:'warehouse',role:'warehouse',display_name:'英国仓库',...sel('United Kingdom'),permissions_version:1,state:'complete'},
 {id:'warehouse-eu',role:'warehouse',display_name:'欧洲大陆联合退货仓',...sel('Germany','Netherlands','Belgium','Spain','Portugal','Italy','Switzerland','Austria','Denmark','Sweden','Norway','Finland'),permissions_version:1,state:'factor'},
 {id:'warehouse-fr-lyon-north-distribution-centre-2026',role:'warehouse',display_name:'法国里昂北部配送中心退货仓（第二仓·临时扩容）',...sel('France'),permissions_version:1,state:'password'},
 {id:'warehouse-sg',role:'warehouse',display_name:'warehouse-sg',...sel('Singapore'),permissions_version:1,state:'new'},
]}
function seedRequests():Request[]{const base={reviewed_by:'',reviewed_at:'',rejection_reason:''};return [
 {...base,id:'mock-req-5',requester:'finance-uk',account_id:'warehouse-uk-manchester',display_name:'曼彻斯特退货仓',...sel('United Kingdom'),reason:'旺季英国退货量增加，需要第二个仓库账户分担北部门店的退货核查。',status:'pending',created_at:ago(2)},
 {...base,id:'mock-req-4',requester:'finance',account_id:'warehouse-nordic',display_name:'北欧联合仓',...sel('Denmark','Sweden','Norway','Finland','Iceland'),reason:'',status:'pending',created_at:ago(20)},
 {...base,id:'mock-req-3',requester:'finance-uk',account_id:'warehouse-fr-paris',display_name:'巴黎退货仓',...sel('France'),reason:'法国差异目前全部派给英国仓库，跨国核查需要额外沟通。\n申请一个法国本地仓库账户，专门处理France范围内的取消记录。',status:'pending',created_at:ago(30)},
 {...base,id:'mock-req-2',requester:'finance',account_id:'warehouse-sg',display_name:'warehouse-sg',...sel('Singapore'),reason:'新加坡样例数据核查。',status:'approved',created_at:ago(100),reviewed_by:'admin',reviewed_at:ago(96)},
 {...base,id:'mock-req-1',requester:'finance-uk',account_id:'warehouse-uk-temp',display_name:'英国临时仓',...sel('United Kingdom'),reason:'临时使用。',status:'rejected',created_at:ago(200),reviewed_by:'admin',reviewed_at:ago(190),rejection_reason:'临时账户请改用现有英国仓库，避免同一国家/地区重复派单。'},
]}
function seedAudit(list:Account[],reqs:Request[]):Audit[]{
 const out:Audit[]=[];let id=0;const push=(hours:number,actor:string,action:string,target:string,after:unknown,before:unknown=null,request='')=>out.push({id:++id,actor,action,target,request_id:request,before,after,occurred_at:ago(hours)});
 const find=(x:string)=>list.find(a=>a.id===x)!;
 push(310,'admin','create','finance-uk',{...find('finance-uk'),...sel('United Kingdom'),state:'new'});
 push(300,'admin','create','warehouse',{...find('warehouse'),state:'new'});
 push(250,'admin','update','finance-uk',find('finance-uk'),{...find('finance-uk'),...sel('United Kingdom')});
 push(240,'admin','create','warehouse-eu',{...find('warehouse-eu'),state:'new'});
 push(200,'finance-uk','request','warehouse-uk-temp',reqs[4],null,reqs[4].id);
 push(190,'admin','rejected','warehouse-uk-temp',reqs[4],{...reqs[4],status:'pending'},reqs[4].id);
 push(170,'admin','create','warehouse-fr-lyon-north-distribution-centre-2026',{...find('warehouse-fr-lyon-north-distribution-centre-2026'),state:'new'});
 push(120,'admin','create','finance-de',{...find('finance-de'),state:'new'});
 push(100,'finance','request','warehouse-sg',reqs[3],null,reqs[3].id);
 push(96,'admin','approved','warehouse-sg',reqs[3],{...reqs[3],status:'pending'},reqs[3].id);
 push(30,'finance-uk','request','warehouse-fr-paris',reqs[2],null,reqs[2].id);
 push(20,'finance','request','warehouse-nordic',reqs[1],null,reqs[1].id);
 push(2,'finance-uk','request','warehouse-uk-manchester',reqs[0],null,reqs[0].id);
 return out.reverse();
}
let passkeyDomains={base_origin:'http://localhost:5173',origins:[] as string[],version:1};
let accounts=seedAccounts(),requests=seedRequests(),audit=seedAudit(accounts,requests),requestSeq=10;
export function resetAccounts(){passkeyDomains={base_origin:'http://localhost:5173',origins:[],version:1};accounts=seedAccounts();requests=seedRequests();audit=seedAudit(accounts,requests);requestSeq=10}

const profile=({state:_state,...p}:Account):Profile=>({...p,countries:[...p.countries]});
const view=(a:Account)=>({...profile(a),state:a.state});
export const isAll=(p:{role:string;country_scope:string})=>p.role==='admin'||p.country_scope==='all';
export const allows=(p:Profile,country:string)=>isAll(p)||p.countries.includes(country);
const contains=(owner:Profile,other:{country_scope:string;countries:string[];role:string})=>isAll(owner)||(!isAll(other)&&other.countries.every(c=>owner.countries.includes(c)));
export function accountByID(id:string){return accounts.find(a=>a.id===id)}
export function accountProfile(id:string){const a=accountByID(id);return a&&a.state!=='deleted'?profile(a):undefined}
export function setupAccounts(){return accounts.filter(a=>a.state==='new').map(profile)}
export function visibleUsers(p:Profile){return accounts.filter(u=>u.state!=='deleted'&&(isAll(p)||isAll(u)||u.countries.some(c=>p.countries.includes(c)))).map(profile)}
export function countriesFor(p:Profile){return allCountryNames.filter(c=>allows(p,c))}

const invalid=(reply:Reply,message:string)=>reply(422,{code:'invalid_input',message});
const conflict=(reply:Reply,message:string)=>reply(409,{code:'conflict',message});
const forbidden=(reply:Reply)=>reply(403,{code:'forbidden',message:'当前账户无权执行此操作'});
function validate(input:any,reply:Reply):Profile|null{
 const p:Profile={id:String(input.id||''),role:String(input.role||''),display_name:String(input.display_name||'').trim(),country_scope:input.country_scope,countries:[...new Set<string>((input.countries||[]).map(String))].sort(),permissions_version:1};
 if(!/^[a-z0-9][a-z0-9_.-]{2,63}$/.test(p.id)){invalid(reply,'账户名须为3至64位小写字母、数字、点、下划线或连字符');return null}
 if(p.role!=='finance'&&p.role!=='warehouse'){invalid(reply,'只能创建财务或仓库账户');return null}
 if(!p.display_name||[...p.display_name].length>64){invalid(reply,'显示名称须为1至64个字符且不含控制字符');return null}
 if(!p.country_scope)p.country_scope=p.countries.length?'selected':'all';
 if(p.country_scope==='all'){if(p.countries.length){invalid(reply,'全部国家/地区范围不能同时指定国家/地区');return null}return p}
 if(p.country_scope!=='selected'||!p.countries.length){invalid(reply,'请选择至少一个负责国家/地区，或选择全部国家/地区');return null}
 if(p.countries.some(c=>!allCountryNames.includes(c))){invalid(reply,'请选择已发布数据中的国家/地区；尚未导入数据时可先使用全部国家/地区');return null}
 return p;
}
function available(id:string,except='',reply:Reply){if(accounts.some(a=>a.id===id)||requests.some(r=>r.account_id===id&&r.status==='pending'&&r.id!==except)){conflict(reply,'该账户名已存在或已有待审批申请');return false}return true}
const record=(actor:string,action:string,target:string,before:unknown,after:unknown,request='')=>audit.unshift({id:(audit[0]?.id||0)+1,actor,action,target,request_id:request,before,after,occurred_at:new Date().toISOString()});

// Returns false when the path is not an account route. openCases lists the
// country of every unfinished case per assignee, for the scope-shrink check.
export function handleAccounts(api:string,method:string,body:any,session:Profile&{state:string;scope:string},reply:Reply,openCases:(id:string)=>string[],deletionCases:(id:string,remove:boolean)=>{open_cases:number;closed_cases:number}):boolean{
 const business=session.scope==='business'&&session.state==='complete';
 if(api==='/countries'){reply(200,countriesFor(session));return true}
 if(api==='/admin/passkey-domains'||api==='/admin/accounts'||api==='/admin/account-audit'||api.startsWith('/admin/accounts/')){
  if(!business||session.role!=='admin'){forbidden(reply);return true}
  if(api==='/admin/passkey-domains'){
   if(method==='GET'){reply(200,passkeyDomains);return true}
   if(method!=='PUT'){reply(405,{message:'不支持的操作'});return true}
   if(body.version!==passkeyDomains.version){conflict(reply,'域名设置已变化，请刷新后重试');return true}
   try{const origins=[...new Set<string>((body.origins||[]).map((raw:string)=>{const u=new URL(raw.includes('://')?raw:'https://'+raw);if(u.username||u.password||u.pathname!=='/'||u.search||u.hash||u.protocol!=='https:'&&!(u.protocol==='http:'&&u.hostname==='localhost')||u.hostname.includes('*'))throw new Error();return u.origin}))];if(origins.length>20)throw new Error();const before=passkeyDomains;passkeyDomains={...before,origins:origins.filter(v=>v!==before.base_origin).sort(),version:before.version+1};record(session.id,'passkey_domains','Passkey域名',before,passkeyDomains);reply(200,passkeyDomains)}catch{invalid(reply,'请填写有效HTTPS入口，不含路径或通配符')};return true
  }
  if(api==='/admin/account-audit'){reply(200,audit.slice(0,200));return true}
  if(api==='/admin/accounts'&&method==='GET'){reply(200,accounts.filter(a=>a.state!=='deleted').sort((a,b)=>a.id.localeCompare(b.id)).map(view));return true}
  if(api==='/admin/accounts'&&method==='POST'){const p=validate(body,reply);if(!p||!available(p.id,'',reply))return true;const a={...p,state:'new'};accounts.push(a);record(session.id,'create',p.id,null,view(a));reply(200,view(a));return true}
  const m=/^\/admin\/accounts\/([^/]+)$/.exec(api);
  const remove=/^\/admin\/accounts\/([^/]+)(\/deletion-preview)?$/.exec(api);
  if(remove&&(method==='DELETE'||remove[2])){
   const a=accountByID(decodeURIComponent(remove[1]));if(!a||a.state==='deleted'){reply(404,{code:'not_found',message:'账户不存在或已删除'});return true}
   if(a.role==='admin'){forbidden(reply);return true}
   const pending=requests.filter(r=>r.requester===a.id&&r.status==='pending');
   const impact={account:profile(a),...deletionCases(a.id,false),pending_requests:pending.length};
   if(method==='GET'&&remove[2]){reply(200,impact);return true}
   if(method!=='DELETE'||remove[2]){reply(405,{message:'不支持的操作'});return true}
   deletionCases(a.id,true);a.state='deleted';a.permissions_version++;
   for(const r of pending){const before={...r};Object.assign(r,{status:'rejected',reviewed_by:session.id,reviewed_at:new Date().toISOString(),rejection_reason:'申请人账户已删除，申请自动驳回。'});record(session.id,'rejected',r.account_id,before,{...r},r.id)}
   const result={...impact,deleted_at:new Date().toISOString()};record(session.id,'delete',a.id,impact,result);reply(200,result);return true
  }
  if(m&&method==='PATCH'){
   const old=accountByID(decodeURIComponent(m[1]));if(!old||old.state==='deleted'){reply(404,{code:'not_found',message:'账户不存在'});return true}
   if(old.role==='admin'){forbidden(reply);return true}
   if(body.id&&body.id!==old.id||body.role&&body.role!==old.role){invalid(reply,'不能修改登录账户名或角色');return true}
   const p=validate({...body,id:old.id,role:old.role},reply);if(!p)return true;
   const changed=old.country_scope!==p.country_scope||old.countries.join()!==p.countries.join();
   if(changed&&!isAll(p)&&openCases(old.id).some(c=>!p.countries.includes(c))){conflict(reply,'仍有新国家/地区范围之外的未结案派单，请先完成这些工作');return true}
   const before=view(old);Object.assign(old,{display_name:p.display_name,country_scope:p.country_scope,countries:p.countries,permissions_version:old.permissions_version+(changed?1:0)});
   record(session.id,'update',old.id,before,view(old));reply(200,view(old));return true
  }
  reply(405,{code:'method_not_allowed',message:'不支持的操作'});return true;
 }
 if(api==='/warehouse-requests'){
  if(!business||(session.role!=='admin'&&session.role!=='finance')){forbidden(reply);return true}
  if(method==='GET'){reply(200,requests.filter(r=>session.role==='admin'||r.requester===session.id).map(r=>({...r,account_state:r.status==='approved'?accountByID(r.account_id)?.state:undefined})).sort((a,b)=>b.created_at.localeCompare(a.created_at)));return true}
  if(session.role!=='finance'){forbidden(reply);return true}
  if(body.role&&body.role!=='warehouse'){invalid(reply,'只能申请仓库账户');return true}
  const p=validate({...body,role:'warehouse'},reply);if(!p)return true;
  if(!contains(session,p)){forbidden(reply);return true}
  const reason=String(body.reason||'').trim();if([...reason].length>1000){invalid(reply,'申请理由不能超过1000字符');return true}
  if(!available(p.id,'',reply))return true;
  const r:Request={id:'mock-req-'+(++requestSeq),requester:session.id,account_id:p.id,display_name:p.display_name,country_scope:p.country_scope,countries:p.countries,reason,status:'pending',created_at:new Date().toISOString(),reviewed_by:'',reviewed_at:'',rejection_reason:''};
  requests.unshift(r);record(session.id,'request',p.id,null,r,r.id);reply(200,r);return true;
 }
 const review=/^\/warehouse-requests\/([^/]+)\/(approve|reject)$/.exec(api);
 if(review){
  if(!business||session.role!=='admin'){forbidden(reply);return true}
  const r=requests.find(x=>x.id===decodeURIComponent(review[1]));if(!r){reply(404,{code:'not_found',message:'申请不存在'});return true}
  if(r.status!=='pending'){conflict(reply,'申请已经审批，不能重复操作');return true}
  const before={...r};
  if(review[2]==='approve'){
   const p=validate({id:r.account_id,role:'warehouse',display_name:r.display_name,country_scope:r.country_scope,countries:r.countries},reply);if(!p)return true;
   const owner=accountProfile(r.requester);if(!owner||owner.role!=='finance'||!contains(owner,p)){conflict(reply,'申请人当前国家/地区权限已不再覆盖申请范围，请驳回后重新申请');return true}
   if(!available(p.id,r.id,reply))return true;
   accounts.push({...p,state:'new'});r.status='approved';
  }else{
   const reason=String(body.reason||'').trim();if(!reason||[...reason].length>1000){invalid(reply,'驳回原因须为1至1000字符');return true}
   r.status='rejected';r.rejection_reason=reason;
  }
  r.reviewed_by=session.id;r.reviewed_at=new Date().toISOString();record(session.id,r.status,r.account_id,before,{...r},r.id);reply(200,r);return true;
 }
 return false;
}

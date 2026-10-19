type SignalProvider={signalUnknownCredential?:(options:{rpId:string;credentialId:string})=>Promise<void>};

// A successful signal is only a request to the current credential provider.
// It must never turn a committed account deletion into a reported failure.
export async function signalRemovedPasskeys(ids:string[],provider:SignalProvider|undefined=typeof PublicKeyCredential==='undefined'?undefined:PublicKeyCredential,timeoutMs=2000,rpId=typeof location==='undefined'?'localhost':location.hostname):Promise<string>{
 const keys=[...new Set(ids)];
 if(!keys.length)return '该账户没有已登记的Passkey。';
 if(typeof provider?.signalUnknownCredential!=='function')return '当前浏览器不支持Passkey失效通知，可在设备密码管理器中手动移除。';
 let accepted=0,failed=0,timer:ReturnType<typeof setTimeout>|undefined;
 const requests=keys.map(credentialId=>Promise.resolve().then(()=>provider.signalUnknownCredential!({rpId,credentialId})).then(()=>{accepted++},()=>{failed++}));
 try{await Promise.race([Promise.all(requests),new Promise<void>(resolve=>{timer=setTimeout(resolve,timeoutMs)})])}finally{if(timer!==undefined)clearTimeout(timer)}
 const pending=keys.length-accepted-failed;
 if(!accepted)return pending?'账户已删除，Passkey通知暂未响应，请在设备密码管理器中手动清理。':'账户已删除，设备未接受Passkey失效通知，请手动清理。';
 return `已向当前浏览器发送 ${accepted} 条Passkey失效通知，由设备决定隐藏或移除。`+(failed||pending?'部分通知未完成，可手动清理。':'其他设备可能仍需手动清理。');
}

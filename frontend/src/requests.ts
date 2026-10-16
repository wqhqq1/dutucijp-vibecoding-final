export type ResourceResult<T> = {loading:boolean;value?:T;error?:string};

// Each independently displayed resource owns its request. Cleanup suppresses late
// responses even when a transport has already completed or ignores cancellation.
export function startResourceRequest<T>(
  load:(signal:AbortSignal)=>Promise<T>,
  publish:(result:ResourceResult<T>)=>void,
):()=>void {
  const controller=new AbortController();
  publish({loading:true});
  void (async()=>{
    try {
      const value=await load(controller.signal);
      if(!controller.signal.aborted)publish({loading:false,value});
    } catch(error) {
      if(!controller.signal.aborted)publish({loading:false,error:error instanceof Error?error.message:'请求未完成'});
    }
  })();
  return ()=>controller.abort();
}

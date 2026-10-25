import React,{useState} from 'react';
import {allCountries,type Account} from './accounts';

// Account charts are plain SVG with geometry in attributes, so they work under
// the backend CSP (style-src 'self') without inline styles. Every figure a mark
// shows is also printed beside it or reachable from a keyboard-focusable control.

export type Segment={key:string;label:string;value:number;tone:string;hint?:string};

// Part-to-whole bar. The legend doubles as a filter when onSelect is given.
export function StackedBar({label,segments,selected,onSelect}:{label:string;segments:Segment[];selected?:string;onSelect?:(key:string)=>void}){
 const total=segments.reduce((sum,s)=>sum+s.value,0);
 let x=0;
 const description=segments.map(s=>`${s.label}${s.value}`).join('，');
 return <div className="viz-stack">
  {total>0?<svg className="stack-bar" viewBox="0 0 100 10" preserveAspectRatio="none" role="img" aria-label={`${label}：${description}，共${total}`}>
   {segments.filter(s=>s.value>0).map(s=>{const width=s.value/total*100,rect=<rect key={s.key} className={'tone-'+s.tone+(selected&&selected!==s.key?' is-dimmed':'')} x={x} y={0} width={width} height={10}><title>{`${s.label} ${s.value}`}</title></rect>;x+=width;return rect})}
  </svg>:<div className="stack-bar is-empty" aria-hidden="true"/>}
  <ul className="viz-legend" aria-label={label+'图例'}>
   {segments.map(s=><li key={s.key}>{onSelect?<button type="button" className="legend-item" aria-pressed={selected===s.key} title={s.hint} onClick={()=>onSelect(selected===s.key?'':s.key)}><span className={'swatch tone-'+s.tone} aria-hidden="true"/>{s.label}<strong>{s.value}</strong></button>
    :<span className="legend-item" title={s.hint}><span className={'swatch tone-'+s.tone} aria-hidden="true"/>{s.label}<strong>{s.value}</strong></span>}</li>)}
  </ul>
 </div>;
}

type Coverage={country:string;finance:number;warehouse:number};

// Which countries have finance and warehouse accounts limited to them. Accounts
// with all-country scope cover every row equally, so they are counted once in
// the note instead of lengthening every bar.
export function coverage(countries:string[],accounts:Account[]){
 const staff=accounts.filter(a=>a.role==='finance'||a.role==='warehouse');
 const global={finance:staff.filter(a=>a.role==='finance'&&allCountries(a)).length,warehouse:staff.filter(a=>a.role==='warehouse'&&allCountries(a)).length};
 const known=new Set(countries);
 // Keep countries an account still holds even if no published row lists them now.
 for(const a of staff)if(!allCountries(a))for(const c of a.countries||[])known.add(c);
 const rows:Coverage[]=[...known].map(country=>{const limited=staff.filter(a=>!allCountries(a)&&a.countries?.includes(country));return {country,finance:limited.filter(a=>a.role==='finance').length,warehouse:limited.filter(a=>a.role==='warehouse').length}})
  .sort((a,b)=>(b.finance+b.warehouse)-(a.finance+a.warehouse)||a.country.localeCompare(b.country));
 const gaps=global.warehouse?[]:rows.filter(r=>r.warehouse===0).map(r=>r.country);
 return {rows,global,gaps};
}

export function CountryCoverage({countries,accounts,selected,onSelect}:{countries:string[];accounts:Account[];selected:string;onSelect:(country:string)=>void}){
 const [expanded,setExpanded]=useState(false),[focus,setFocus]=useState<Coverage|null>(null);
 const {rows,global,gaps}=coverage(countries,accounts);
 const max=Math.max(1,...rows.map(r=>Math.max(r.finance,r.warehouse)));
 const shown=expanded?rows:rows.slice(0,8);
 const readout=focus?`${focus.country}：限定财务 ${focus.finance} 个，限定仓库 ${focus.warehouse} 个`:`点击国家/地区可筛选下方名录。全部国家/地区账户：财务 ${global.finance} 个，仓库 ${global.warehouse} 个，覆盖所有国家/地区，未计入条形。`;
 if(!rows.length)return <p className="muted empty-inline">尚无已发布国家/地区，账户只能使用全部国家/地区范围。</p>;
 return <div className="coverage">
  <div className="coverage-head"><ul className="viz-legend" aria-label="国家/地区覆盖图例"><li><span className="legend-item"><span className="swatch tone-finance" aria-hidden="true"/>限定财务</span></li><li><span className="legend-item"><span className="swatch tone-warehouse" aria-hidden="true"/>限定仓库</span></li></ul></div>
  <p className="viz-readout" aria-live="polite">{readout}</p>
  <ol className="coverage-rows">{shown.map(r=><li key={r.country}><button type="button" className={'coverage-row'+(gaps.includes(r.country)?' is-gap':'')} aria-pressed={selected===r.country} aria-label={`${r.country}，限定财务${r.finance}个，限定仓库${r.warehouse}个${gaps.includes(r.country)?'，暂无仓库覆盖':''}`} onClick={()=>onSelect(selected===r.country?'':r.country)} onMouseEnter={()=>setFocus(r)} onMouseLeave={()=>setFocus(null)} onFocus={()=>setFocus(r)} onBlur={()=>setFocus(null)}>
   <span className="coverage-name" title={r.country}>{r.country}</span>
   <svg className="coverage-bars" viewBox="0 0 100 12" preserveAspectRatio="none" aria-hidden="true"><rect className="coverage-track" x={0} y={0} width={100} height={5}/><rect className="coverage-track" x={0} y={7} width={100} height={5}/>{r.finance>0&&<rect className="tone-finance" x={0} y={0} width={r.finance/max*100} height={5}/>}{r.warehouse>0&&<rect className="tone-warehouse" x={0} y={7} width={r.warehouse/max*100} height={5}/>}</svg>
   <span className="coverage-count">{r.finance}<span aria-hidden="true"> / </span>{r.warehouse}</span>
  </button></li>)}</ol>
  {rows.length>8&&<button type="button" className="text-button" onClick={()=>setExpanded(!expanded)}>{expanded?'只显示前 8 个国家/地区':`显示全部 ${rows.length} 个国家/地区`}</button>}
  {gaps.length>0&&<p className="coverage-gaps"><strong>{gaps.length} 个国家/地区暂无仓库账户：</strong>{gaps.slice(0,6).join('、')}{gaps.length>6?` 等`:''}。可派给负责该国的财务或管理员，也可先创建仓库账户。</p>}
  {!gaps.length&&<p className="coverage-ok">每个国家/地区都至少有一个仓库账户可派单{global.warehouse?'（含全部国家/地区仓库账户）':''}。</p>}
 </div>;
}

function dayKey(date:Date){return `${date.getFullYear()}-${String(date.getMonth()+1).padStart(2,'0')}-${String(date.getDate()).padStart(2,'0')}`}

// Management operations per day over the last fortnight, one series.
export function ActivityColumns({events,days=14}:{events:{occurred_at:string}[];days?:number}){
 const [focus,setFocus]=useState<number|null>(null);
 const today=new Date(),buckets=Array.from({length:days},(_,i)=>{const d=new Date(today.getFullYear(),today.getMonth(),today.getDate()-(days-1-i));return {key:dayKey(d),label:`${d.getMonth()+1}-${d.getDate()}`,count:0}});
 const index=new Map(buckets.map((b,i)=>[b.key,i]));
 for(const e of events){const d=new Date(e.occurred_at);if(!Number.isNaN(d.getTime())){const i=index.get(dayKey(d));if(i!==undefined)buckets[i].count++}}
 const total=buckets.reduce((s,b)=>s+b.count,0),peak=Math.max(...buckets.map(b=>b.count));
 const top=Math.max(1,peak),W=420,H=124,left=26,right=6,base=100,plot=86,slot=(W-left-right)/days,bar=Math.min(16,slot-6);
 const readout=focus!==null?`${buckets[focus].label}：${buckets[focus].count} 次管理操作`:total?`最近 ${days} 天共 ${total} 次管理操作，单日最多 ${peak} 次。`:`最近 ${days} 天没有管理操作。`;
 return <figure className="activity">
  <p className="viz-readout" aria-live="polite">{readout}</p>
  <svg className="activity-chart" viewBox={`0 0 ${W} ${H}`} role="group" aria-label={`最近${days}天每日管理操作次数`}>
   <line className="viz-grid" x1={left} x2={W-right} y1={base-plot} y2={base-plot}/>
   <text className="viz-tick" x={left-6} y={base-plot+4} textAnchor="end">{top}</text>
   <text className="viz-tick" x={left-6} y={base+4} textAnchor="end">0</text>
   {buckets.map((b,i)=>{const h=b.count/top*plot,x=left+i*slot+(slot-bar)/2,y=base-h,r=Math.min(3,h);
    return <g key={b.key} className={'activity-col'+(focus===i?' is-focused':'')} tabIndex={0} aria-label={`${b.label}，${b.count}次`} onMouseEnter={()=>setFocus(i)} onMouseLeave={()=>setFocus(null)} onFocus={()=>setFocus(i)} onBlur={()=>setFocus(null)}>
     <rect className="activity-hit" x={left+i*slot} y={base-plot} width={slot} height={plot+22}/>
     {h>0&&<path className="activity-bar" d={`M${x},${base} V${y+r} Q${x},${y} ${x+r},${y} H${x+bar-r} Q${x+bar},${y} ${x+bar},${y+r} V${base} Z`}/>}
     {(i%2===(days-1)%2)&&<text className="viz-tick" x={x+bar/2} y={base+16} textAnchor="middle">{b.label}</text>}
    </g>})}
   <line className="viz-axis" x1={left} x2={W-right} y1={base} y2={base}/>
  </svg>
  <details className="viz-table"><summary>查看每日数据表</summary><table><thead><tr><th>日期</th><th className="num">操作次数</th></tr></thead><tbody>{buckets.map(b=><tr key={b.key}><td>{b.key}</td><td className="num">{b.count}</td></tr>)}</tbody></table></details>
 </figure>;
}

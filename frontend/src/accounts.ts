export type Account={id:string;role:string;display_name?:string;country_scope?:string;countries?:string[];permissions_version?:number;state?:string};
export function allCountries(account:Account){return account.role==='admin'||account.country_scope!=='selected'}
export function countryLabel(account?:Account|null){if(!account)return '';return allCountries(account)?'全部国家':account.countries?.join('、')||'未指定国家'}
// Compact form for one-line places (top bar, select options): long country
// lists collapse to a count; the full list stays available through countryLabel.
export function countrySummary(account?:Account|null,max=2){
 if(!account)return '';
 if(allCountries(account))return '全部国家';
 const list=account.countries||[];
 if(!list.length)return '未指定国家';
 return list.length<=max?list.join('、'):`${list.slice(0,max).join('、')} 等${list.length}国`;
}
export function accountName(account:Account){return account.display_name||account.id}
export function accountLabel(account:Account){return `${accountName(account)} · ${account.id} · ${countrySummary(account)}`}

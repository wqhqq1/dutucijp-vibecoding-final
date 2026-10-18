// Shared display labels. Keep API values and saved business codes unchanged.
export const caseReasons:Record<string,[string,string]>={
 missing_identity:['缺客户或国家','取消记录缺少客户编号或国家，按规则不自动配对。'],
 suspected_duplicate_cancel:['取消行疑似重复','这条取消在同一工作表内疑似重复，需人工核查。'],
 ambiguous_candidates:['多个合格候选','两条以上销售同时满足条件，不按先后顺序猜测。'],
 suspected_duplicate_sale:['候选销售疑似重复','唯一合格的销售行在同一工作表内疑似重复。'],
 no_identity_match:['无同身份销售','当前范围内没有客户、商品、单价和国家都相同的销售。'],
 no_strictly_earlier_sale:['无更早销售','同身份的销售都不早于这次取消。'],
 insufficient_remaining_quantity:['可退余量不足','更早的同身份销售，剩余可退数量都不够。'],
};
const recommendationLabels:Record<string,string>={unique:'当前唯一',duplicate:'完全重复归组',basket:'整单关联',nearest:'最近一笔'};
const roleLabels:Record<string,string>={finance:'财务',warehouse:'仓库',admin:'管理员'};

// New user-friendly labels for batch review
const recommendationUserLabels:Record<string,string>={
 unique:'唯一匹配',
 duplicate:'完全重复归组',
 basket:'整单关联',
 nearest:'按时间推荐',
 tied:'最近并列',
};

const recommendationExplanations:Record<string,string>={
 unique:'当前退货行在相同客户、商品、单价和国家条件下,只能找到唯一一笔符合条件的销售。',
 duplicate:'多笔销售的原始记录8个字段完全一致,系统归为一组,只计一次销售数量。确认后所有退货共享这个组的余量。',
 basket:'同一张退货单的多个商品,系统推断出共同的原销售单。您需要为整单的所有明细选择同一个方案,否则无法提交。',
 nearest:'按销售时间倒序排列,取最近的销售作为推荐。这是规则推断,不代表真实原单证明。',
 tied:'多笔销售时间完全相同,系统无法自动判断,请根据商品描述或其他证据选择。',
};

export function caseReasonLabel(code:string){return caseReasons[code]?.[0]||`未知分类（${code}）`}
export function recommendationLabel(code:string){const label=recommendationLabels[code];return typeof label==='string'?label:`未知推荐类型（${code}）`}
export function roleLabel(code:string){const label=roleLabels[code];return typeof label==='string'?label:`未知角色（${code}）`}

export function recommendationUserLabel(code:string){const label=recommendationUserLabels[code];return typeof label==='string'?label:recommendationLabel(code)}
export function recommendationExplain(code:string){const explanation=recommendationExplanations[code];return typeof explanation==='string'?explanation:''}

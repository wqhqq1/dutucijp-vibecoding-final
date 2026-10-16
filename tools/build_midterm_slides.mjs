// Author with the bundled public Artifact Tool API. Teacher reference stays private.
import fs from 'node:fs/promises';
import path from 'node:path';
import { createRequire } from 'node:module';
import { pathToFileURL, fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';

const project = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const deps = 'C:/Users/wqhqq/.cache/codex-runtimes/codex-primary-runtime/dependencies';
const skill = 'C:/Users/wqhqq/.codex/plugins/cache/openai-primary-runtime/presentations/26.1013.11914/skills/presentations';
process.env.RUNTIME_NODE_MODULES = path.join(deps,'node/node_modules');
const req = createRequire(import.meta.url);
const {FileBlob, PresentationFile} = await import(pathToFileURL(req.resolve('@oai/artifact-tool', {paths:[path.join(deps,'node/node_modules')]})).href);
const {finalizePresentation, applyPresentationChartFont} = await import(pathToFileURL(path.join(skill,'container_tools/artifact_tool_utils.mjs')).href);
const build = path.join(project,'.working/midterm-20261016');
await fs.mkdir(build,{recursive:true});
const reference = path.join(project,'.working/midterm-20261015/teacher-reference.pptx');
const p = await PresentationFile.importPptx(await FileBlob.load(reference));
if(p.slides.items.length!==10) throw new Error('Teacher reference must have ten slides');
const shotRoot = '每日工作记录/2026-10-15/截图/FR12-16浏览器验收/';
const evidence = {
 overview:shotRoot+'01-财务差异总览.jpg', duplicate:shotRoot+'04-重复派单拒绝.jpg',
 closed:shotRoot+'14-结论关闭与不可修改.jpg', wait:shotRoot+'08-交易查询等待状态.jpg',
 test:shotRoot+'22-本人WindowsPowerShell测试通过.png',
 login:'每日工作记录/2026-10-15/截图/2026-10-15-06-修复版安全登录页.png',
 git:'每日工作记录/2026-10-14/截图/2026-10-14-02-真实提交记录-GitHub页面.jpg',
 performance:'每日工作记录/2026-10-16/原始输出/正式NFR01-03/summary.json',
 http:'每日工作记录/2026-10-16/原始输出/正式NFR04/summary.json',
 regression:'每日工作记录/2026-10-16/原始输出/辅导收工回归/summary.json',
};
for(const v of Object.values(evidence)) await fs.access(path.join(project,v));
const imageBytes=new Map();
for(const v of Object.values(evidence).filter(v=>/\.(png|jpg)$/.test(v))) imageBytes.set(v,new Uint8Array(await fs.readFile(path.join(project,v))));
const perf = JSON.parse(await fs.readFile(path.join(project,evidence.performance),'utf8'));
const nfr = Object.fromEntries(['NFR01','NFR02','NFR03'].map(n=>[n,perf.results.filter(x=>x.nfr===n)]));
for(const a of Object.values(nfr)) if(a.length!==3||a.some(x=>!x.baseline_passed)) throw new Error('Performance evidence mismatch');
const regression=JSON.parse((await fs.readFile(path.join(project,evidence.regression),'utf8')).replace(/^\uFEFF/,''));
if(regression.go_top_level_passed!==76||+regression.python_tests_ran!==15||+regression.frontend_passed!==6)throw new Error('Regression evidence mismatch');

const titles=['退货对账与差异处理系统','一、业务场景与用户','二、选题规模对照表','三、规格骨架','四、技术选型与决定记录','五、核心链路演示与数据准备','六、工程化控制物证','七、制度物证','八、失败与修复记录','九、剩余工作、风险与计划'];
const footers=[
 '截至2026-10-16，可审阅初稿。证据：97b790a、当日正式量测与既有操作记录',
 '证据：要做什么.md，2026-10-15 Chrome实拍01。公开交易真实，人工处理为课程模拟',
 '证据：三份规格、选题自查表.md、2026-10-16正式NFR与回归原始输出',
 '证据：FR-10、TC-20，2026-10-15 Chrome实拍04及隔离并发测试',
 '证据：决定记录.md、97b790a、10-15/16正式量测。取三次最大值，未清OS文件缓存',
 '证据：官方all守恒输出、year1配对输出、2026-10-15 Chrome实拍14与审计',
 '证据：79ff9b2、746a7e1恢复记录、当日97项回归。图为10-14历史提交页面',
 '证据：run-app/SKILL.md、只读静态审查记录、Hook策略自测。未执行项明确待验',
 '证据：10-15 PS5亲跑截图22、查询等待实拍08、安全登录实拍06与修复提交',
 '证据：本人验收步骤.md、课程完成核对表.md、执行计划.md。下列日期均为后续计划',
];
const sourceNote = (files)=>files.map(x=>path.join(project,x)).join('\n');
function style(shape,size=24,font='宋体',bold=false){
 shape.text.style={fontSize:size,typeface:font,bold,color:'#000000',alignment:'left',verticalAlignment:'top',autoFit:'none',wrap:'square',insets:0};
 shape.text.insets=0;
}
function text(slide,name,value,x,y,w,h,size=24,font='宋体',bold=false){
 const sh=slide.shapes.add({geometry:'textbox',name,position:{left:x,top:y,width:w,height:h},text:value});
 style(sh,size,font,bold);return sh;
}
function body(i,lines,y=143,gap=43,size=24){
 if(i!==9&&lines.length!==3)throw new Error('Expected three body lines');
 if(i===9&&lines.length!==4)throw new Error('Expected four plan lines');
 const s=p.slides.items[i]; const old=s.shapes.items.find(x=>x.name==='TextBox 2');old.text='';old.position={left:68,top:143,width:1,height:1};
 lines.forEach((line,j)=>text(s,`正文${j+1}`,line,68,y+j*gap,1134,44,size));
}
function image(i,filename,x,y,w,h,crop){p.slides.items[i].images.add({blob:imageBytes.get(filename),contentType:filename.endsWith('.png')?'image/png':'image/jpeg',alt:`真实证据：${filename}${crop?'（仅在幻灯片内裁切显示，原图保留）':''}`,fit:'contain',position:{left:x,top:y,width:w,height:h},...(crop?{crop}:{})});}
const notes=[];
function note(i,seconds,script,files,limits=''){
 notes[i]={slide:i+1,title:titles[i],seconds,script,sources:files,limits};
 p.slides.items[i].speakerNotes.text=`计划讲述${seconds}秒。尚未由本人计时演练。\n${script}\n\n证据路径：\n${sourceNote(files)}\n\n适用边界：${limits||'以实际记录为准。'}\n材料状态截至2026-10-16，尚未冻结。`;
}
for(const[i,s]of p.slides.items.entries()){
 const title=s.shapes.items.find(x=>x.name==='TextBox 1');title.text=titles[i];style(title,i===0?40:34.6667,'黑体',true);
 title.position={left:68,top:i===0?192:60.47,width:1134,height:72};
 const foot=s.shapes.items.find(x=>x.name==='TextBox 3');foot.text=footers[i];style(foot,16,'宋体');foot.position={left:68,top:642,width:1134,height:28};
}
// Cover: exactly four authored lines, each at most twenty characters.
const cover=['退货对账与差异处理系统','机国际2502','20251101030 王麒皓','零售交易Ⅱ（UCI）·挑战档'];
if(cover.some(x=>[...x].length>20))throw new Error('Cover text too long');
const coverBody=p.slides.items[0].shapes.items.find(x=>x.name==='TextBox 2');coverBody.text='';coverBody.position={left:68,top:300,width:1,height:1};
cover.slice(1).forEach((v,i)=>text(p.slides.items[0],`封面${i+2}`,v,68,310+i*56,1134,45,26.6667));
note(0,10,'这是退货对账与差异处理系统中期可审阅初稿。当前全量支持与四项性能底线已实测通过，课程材料及本人验收仍有待办。',['选题自查表.md',evidence.performance,evidence.http]);

body(1,['财务核查取消交易，仓库处理本人差异，管理员查看全局。','目标是减少逐行筛选与备注，把责任人、状态和结论连起来。','候选来自保守规则推断，原数据没有真实原单关联号。']);
image(1,evidence.overview,68,281,1134,326,{left:0,top:0,right:0,bottom:.44});
note(1,25,'公开数据来自UCI Online Retail II。系统面对财务、仓库、管理员三类角色。财务派单，责任人核查，管理员查看全局。屏幕中的4,991条是year1初始差异，并非4,991个人工已经处理完的工单。',['要做什么.md',evidence.overview],'没有访谈证据可证明某企业原来使用何种手工系统，因此“减少逐行筛选与备注”是课程设计目标。交易真实，匹配为规则推断，角色及人工处理是课程模拟。截图为10-15版本，不证明10-16首屏速度。');

const scaleRows=[['指标','课程下限','当前实际'],['需求','≥12条','18条FR'],['不做的范围','≥2条','4条NR'],['异常情况','≥3条','8项'],['独立模块','≥3个','5个，真机未全验'],['非CRUD规则','≥3条','8条R'],['口径与清洗','≥3条且配用例','6条CR，已测'],['非功能指标','≥1条','4项底线均通过']];
const scaleBody=p.slides.items[2].shapes.items.find(x=>x.name==='TextBox 2');scaleBody.text='';scaleBody.position={left:68,top:150,width:1,height:1};
const table=p.slides.items[2].tables.add({rows:8,columns:3,left:68,top:158,width:1134,height:432,columnWidths:[360,330,444],values:scaleRows});
table.styleOptions={headerRow:true,bandedRows:false};table.borders.assign({style:'solid',fill:'#A5A5A5',width:1});
for(let r=0;r<8;r++){table.rows[r].height=54;for(let c=0;c<3;c++){
 const cell=table.getCell(r,c);cell.fill=r===0?'#F0F0F0':'#FFFFFF';cell.text.style={fontSize:24,typeface:'宋体',color:'#000000',bold:r===0};
}}
note(2,25,'七项规模要求分别对应规格中的编号。十八条需求是定义数，不能等同十八条本人真机全部验收。五个模块已经实现且有自动覆盖，个人操作仍有缺口。四项非功能必须底线现在全部通过。',['选题自查表.md','要做什么.md','怎么做.md','怎么算做对.md',evidence.performance,evidence.http],'优选目标中首次全量45秒和重算5秒未达到。模块数与本人完成数分开，未把设计数量当成人工验收数量。');

body(3,['FR-01至FR-18覆盖五模块，48条TC区分自动与本人验收。','FR-10：重复派单须拒绝，并显示当前责任人。','TC-20：已派warehouse再派，拒绝且仍显示warehouse。']);
image(3,evidence.duplicate,68,281,1134,343,{left:.15,top:0,right:0,bottom:.5});
note(3,25,'五模块为数据准备、对账、工作流、认证审计、查询复现。以重复派单说明可测试的规格：财务已把同一差异派给warehouse，再派应拒绝并保留责任人，截图真实显示“已有责任人：warehouse”。画面是已关闭的0:180案例。待处理重复派单另有07及服务记录。隔离SQLite并发测试另验证争抢仅一次成功且状态、审计原子提交。',['要做什么.md','怎么做.md','怎么算做对.md',evidence.duplicate,'每日工作记录/2026-10-15/FR09-15工作流记录.md'],'48条TC是规格条目，不能与97个自动测试数量互换。截图验证重复派单，真实同时争抢使用隔离测试证据。');

body(4,['选择Go/Gin与SQLite，比较过FastAPI等方案，保留本地部署。','本轮采用索引分页和有界批量事务，代价是更复杂的导入控制。','四项底线通过：一万行1.84s，全量72.34s，重算10.96s，查询P95 11.72ms。'],143,43,24);
const chart=p.slides.items[4].charts.add('bar',{
 position:{left:118,top:290,width:1040,height:315},title:'首次全量流程（秒）',titleTextStyle:{fontSize:23,fill:'#000000'},
 categories:['10-15正式结果','10-16三次最大值','必须底线'],
 series:[{name:'首次全量流程（秒）',values:[198.841174,Math.max(...nfr.NFR02.map(x=>x.measured_seconds)),90],fill:'#255F6D',points:[{idx:0,fill:'#777777'},{idx:1,fill:'#255F6D'},{idx:2,fill:'#B6B6B6'}],valuesFormatCode:'0.00'}],
 barOptions:{direction:'bar',grouping:'clustered',gapWidth:65},hasLegend:false,
 xAxis:{min:0,max:220,majorUnit:50,textStyle:{fontSize:18,fill:'#222222'},majorGridlines:{style:'solid',fill:'#DDDDDD',width:1}},
 yAxis:{textStyle:{fontSize:21,fill:'#000000'},line:{fill:'#FFFFFF',width:0}},
 dataLabels:{showValue:true,position:'outEnd',numberFormatCode:'0.00',numberFormatSourceLinked:false,textStyle:{fontSize:21,fill:'#000000'}},
 chartFill:'#FFFFFF',plotAreaFill:'#FFFFFF'});
applyPresentationChartFont(chart,{sourceFont:'宋体'});
note(4,25,'技术选择保持Go/Gin/SQLite、React/TypeScript/Vite和Python准备。其他后端没有同条件完整对比，不能声称Go相对它们快几倍。图中旧版198.841174秒与新版三次最大72.3367323秒均为完整官方all首次流程，包含准备、导入、索引、计算、持久验算和关闭。其余两次71.9751851、71.9158387秒。重算三次最大10.9557399秒。一万行最大1.8390835秒。后台活动中10路共300次真实认证HTTP，0失败，P95 11.7245毫秒。',['决定记录.md',evidence.performance,evidence.http,'每日工作记录/2026-10-15/FR12-18执行记录.md'],'每次NFR01/02新准备目录和空库，NFR03新输出库重算。未主动清OS文件缓存，下载、编译、安装不计。全量45秒及重算5秒优选目标尚未达。并发测试使用隔离随机账户真实密码加TOTP协议流程，不是用户真机TOTP。后端2至3毫秒诊断不代表浏览器从登录到渲染。');

body(5,['官方两表1,067,371行，接纳1,038,818行，排除28,553行。','year1取消10,205条，自动5,214条，初始差异4,991条。','财务派单后由责任人开始处理，填写核查结论再关闭。']);
image(5,evidence.closed,125,281,1030,336,{left:.55,top:.41,right:.025,bottom:.32});
note(5,40,'第一版演示使用首工作表year1，最终all已经实际跑通。year1原始525461等于接纳521770加排除3691，接纳取消10205等于自动5214、疑似2699、无合格原单2292。全量接纳取消19164等于自动9714、疑似6275、无合格原单3175，初始差异9450。演示0:181没有当前合格候选，不能制造关联，说明资料不足后结案。开始、关闭以及原有责任人都可从审计追溯。',[evidence.closed,'每日工作记录/2026-10-15/FR18浏览器截图与PPT暂停.md','每日工作记录/2026-10-16/性能优化与验证.md'],'year1为第一个工作表，非严格自然年。自动覆盖约51.09%不是关联真值准确率。用户亲自完成Passkey，案例0:181业务点击由代理完成；用户原有0:180记录另列。人工确认只查看过真实候选，没有无依据选择。');

body(6,['可控：按FR编号保存修改与原始输出，97项自动回归通过。','可回退：实验79ff9b2未合入，746a7e1快照已恢复核验。','可审查：保存差异与失败证据，本人上下文管理动作仍待补。']);
image(6,evidence.git,278,281,724,339);
note(6,40,'真实实验分支experiment/fr18-disposable-20261015-210730保留79ff9b2提交，比较后没有业务价值所以未合入。746a7e1是已经bundle verify、clone、fsck和恢复复现通过的旧软件快照，不冒充今天最终冻结包。今天最终回归是76Go顶层、15Python、6前端，共97项，0失败0跳过。用户亲跑截图属于10-15的71Go加13Python，不能改称亲跑97项。',[evidence.git,'每日工作记录/2026-10-15/执行交接.md',evidence.regression],'图为10-14真实GitHub页面，仅显示历史提交。当前版本源代码97b790a，制作前HEAD为4a6ae5e。没有本人上下文占用百分比或实际费用证据，不引用模板中的58%、26%或假定费用。');

body(7,['技能：run-app文件已备，重启后的真实调用仍待本人验证。','子代理：两轮静态审查均零问题，中断的核心任务由主代理接管。','Hook：策略自测已过，可信配置下真实deny仍待本人触发。'],165,139,28);
note(7,40,'这一页明确制度动作的实际状态。项目技能文件和Hook策略已存在，但不能把文件存在或手动调用策略脚本当成真实宿主执行成功。独立静态审查与增量复审实际由指定的GPT-6.1-sol完成，两次零问题；核心实现子代理随后受服务额度影响中断，主代理接管并完成回归。工程动作仍需用户在可信设置下亲自完成，并保留真正的拒绝和目标未执行证据。',['.agents/skills/run-app/SKILL.md','.codex/hooks.json','每日工作记录/2026-10-16/性能优化与验证.md','课程完成核对表.md'],'未声称技能重启成功、Hook可信deny成功或核心代理完整交回。页面不使用应用API的403截图冒充Hook证据。');

const failBody=p.slides.items[8].shapes.items.find(x=>x.name==='TextBox 2');failBody.text='';failBody.position={left:68,top:150,width:1,height:1};
const cards=[
 {text:'PS5误报标准错误\n执行层：亲跑发现\n修：保留原流与退出码',image:evidence.test},
 {text:'查询页持续等待\n实现层：界面暴露\n修：索引与独立加载',image:evidence.wait},
 {text:'首页301循环\n实现层：HTTP复现\n修：显式返回嵌入首页',image:evidence.login},
];
for(const[j,c]of cards.entries()){
 if([...c.text.replaceAll('\n','')].length>40)throw new Error('Failure card exceeds forty characters');
 text(p.slides.items[8],`失败卡片${j+1}`,c.text,68+j*390,165,354,128,24);
 if(j===0)image(8,c.image,68,335,354,114,{left:0,top:.5,right:.65,bottom:.32});
 else if(j===1)image(8,c.image,458,335,354,196,{left:.16,top:.42,right:.58,bottom:.30});
 else image(8,c.image,848,315,354,290);
}
note(8,40,'三例都来自实际问题。第一例用户在WindowsPowerShell5.1运行test.ps1，unittest正常向标准错误输出却被PowerShell当NativeCommandError，修复为独立进程捕获两路输出并按退出码判断，图为用户修复后亲跑通过。第二例Chrome交易查询实际持续等待，10-16修正来源索引、连接顺序与前端独立加载，正式并发测试通过，但未采新版浏览器端总计时。第三例首页301循环经HTTP复现，显式返回嵌入首页后返回200，图为修复后实际登录页。',[evidence.test,evidence.wait,evidence.login,'每日工作记录/2026-10-15/FR18浏览器截图与PPT暂停.md','每日工作记录/2026-10-16/性能优化与验证.md'],'截图分别为修复后测试、修复前等待、修复后首页。没有把不同日期图片拼成同一次测试，也没有把诊断计时代替正式成绩。卡片层次为本项目对执行环境/实现的分析。');

body(9,['剩余：FR-14恢复真机反例、FR-18技能/Hook与报告，拟10-17至21收口。','风险一：首次全量45秒、重算5秒优选目标未达，四项必须底线已过。','风险二：本人完整预演与交付冻结未完成，计划10-21闭合，10-22留空。','请老师确认：没有原单关联号时，是否认可保守推断与资料不足结案。'],166,102,26.6667);
note(9,30,'后续计划不代表已经完成。本人还需受限恢复与反例、可信技能和Hook动作、完整正常及异常预演。真实设备三角色采用Passkey，TOTP按用户要求跳过真机验证，自动协议覆盖保留。人工确认如没有真实依据继续保留待处理，不为演示制造关联。材料还需3至5页报告、最新bundle及九类清单、压缩解包和20MiB限制检查、另一台机器验证、冻结散列和本人发送邮件。计划10-21前完成中期收口，10-22与11-17不执行修补或改包。',['本人验收步骤.md','课程完成核对表.md','执行计划.md',evidence.performance,evidence.http],'当前只存在10-14、10-15、10-16真实工作日，不能伪造八日证据。不能把性能底线通过写成课程整体完成或已发送邮件。45秒和5秒是优选目标，按用户要求已停止进一步优化。');

await fs.writeFile(path.join(build,'slide-content.json'),JSON.stringify({actualDate:'2026-10-16',sourceCode:'97b790a',sourceHead:'4a6ae5e',cover,scaleRows,cards:cards.map(x=>x.text),notes},null,2));
const candidate=path.join(build,'candidate.pptx');
await (await PresentationFile.exportPptx(p)).save(candidate);
const final=path.join(project,process.env.PPT_FINAL_NAME||'中期汇报.pptx');
// The active task workspace is the project parent. Keep the private receipt
// outside the root-level deliverable directory, as required by the finalizer.
const receiptDir=path.join(path.dirname(project),'.working/recon-midterm-20261016');
await fs.mkdir(receiptDir,{recursive:true});
const receiptPath=path.join(receiptDir,`${path.basename(final)}.${Date.now()}.validation.json`);
const result=await finalizePresentation({
 workspaceDir:path.dirname(project),candidatePath:candidate,finalPath:final,explicitTotalSlideCount:10,
 requiredNativeTableOwnerSlides:[3],requiredNativeChartOwnerSlides:[5],materializeLiteralChartWorkbooks:true,
 pythonExecutable:path.join(deps,'python/python.exe'),
 integrityValidatorPath:path.join(skill,'container_tools/inspect_presentation_package_integrity.py'),
 layoutValidatorPath:path.join(skill,'container_tools/inspect_presentation_layout_geometry.py'),
 layoutArgs:['--expected-slide-size-emu','12193200,6858000','--validate-bullet-geometry','--validate-heading-fit','--require-native-table-slide','3'],
 fontPolicy:{basis:'reference',families:['黑体','宋体'],referencePath:reference,referenceSha256:createHash('sha256').update(await fs.readFile(reference)).digest('hex')},
 verifyArtifactToolImport:true,receiptPath
});
await fs.writeFile(path.join(build,'finalizer-result.json'),JSON.stringify(result,null,2));
const fp=await PresentationFile.importPptx(await FileBlob.load(final));
const previews=path.join(build,`${path.parse(final).name}-previews`);await fs.mkdir(previews,{recursive:true});
for(const[i,s]of fp.slides.items.entries()){
 const img=await fp.export({slide:s,format:'png',scale:1});
 await fs.writeFile(path.join(previews,`slide-${String(i+1).padStart(2,'0')}.png`),new Uint8Array(await img.arrayBuffer()));
}
await fs.copyFile(receiptPath,path.join(build,`${path.basename(final)}.validation.json`));
console.log(JSON.stringify({final,slides:fp.slides.items.length,previews,receipt:receiptPath}));

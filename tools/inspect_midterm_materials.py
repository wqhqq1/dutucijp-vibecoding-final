"""Read-only checks of the authored ten-slide material, not a business test."""
from pathlib import Path
from zipfile import ZipFile
from xml.etree import ElementTree as ET
from hashlib import sha256
from datetime import datetime
from zoneinfo import ZoneInfo
from pypdf import PdfReader
import json

ROOT = Path(__file__).resolve().parents[1]
BUILD = ROOT / '.working' / 'midterm-20261016'
OUT = ROOT / '每日工作记录/2026-10-16/原始输出/FR18中期PPT'
OUT.mkdir(parents=True, exist_ok=True)
NS = {'p':'http://schemas.openxmlformats.org/presentationml/2006/main',
      'a':'http://schemas.openxmlformats.org/drawingml/2006/main',
      'c':'http://schemas.openxmlformats.org/drawingml/2006/chart'}
content = json.loads((BUILD/'slide-content.json').read_text(encoding='utf-8'))
assert sum(n['seconds'] for n in content['notes']) == 300
assert all(len(s) <= 20 for s in content['cover'])
assert all(len(s.replace('\n','')) <= 40 for s in content['cards'])
assert len(content['scaleRows']) == 8
missing = sorted({f for n in content['notes'] for f in n['sources'] if not (ROOT/f).is_file()})
assert not missing, missing
with ZipFile(ROOT/'中期汇报.pptx') as z:
    names = z.namelist()
    slides = [n for n in names if __import__('re').fullmatch(r'ppt/slides/slide\d+\.xml', n)]
    assert len(slides) == 10
    size = ET.fromstring(z.read('ppt/presentation.xml')).find('p:sldSz',NS)
    assert (size.get('cx'),size.get('cy')) == ('12193200','6858000')
    assert len(ET.fromstring(z.read('ppt/slides/slide3.xml')).findall('.//a:tbl',NS)) == 1
    assert len(ET.fromstring(z.read('ppt/slides/slide5.xml')).findall('.//c:chart',NS)) == 1
    workbooks = [n for n in names if n.startswith('ppt/embeddings/') and n.endswith('.xlsx')]
    assert len(workbooks) == 1
    embedded = {sha256(z.read(n)).hexdigest() for n in names if n.startswith('ppt/media/')}
    assert len(embedded) == 7
    allowed = set()
    for f in (ROOT/'每日工作记录/2026-10-15/截图').rglob('*'):
        if f.is_file() and f.suffix.lower() in ('.png','.jpg'):
            allowed.add(sha256(f.read_bytes()).hexdigest())
    for f in (ROOT/'每日工作记录/2026-10-14/截图').rglob('*'):
        if f.is_file() and f.suffix.lower() in ('.png','.jpg'):
            allowed.add(sha256(f.read_bytes()).hexdigest())
    assert embedded <= allowed, 'Media must be unchanged genuine source screenshots'
    notes = [n for n in names if __import__('re').fullmatch(r'ppt/notesSlides/notesSlide\d+\.xml',n)]
    assert len(notes) == 10
    slide_texts = '\n'.join(''.join(ET.fromstring(z.read(n)).itertext()) for n in slides)
    texts = '\n'.join(''.join(ET.fromstring(z.read(n)).itertext()) for n in slides + notes)
    forbidden = ['张三','20231234','a7b8c9d','43.5 MB','58%','26%']
    # Notes explicitly explain that the template's invented percentages were
    # not used. Reject example text on slides, and private links everywhere.
    assert not any(s in slide_texts for s in forbidden)
    assert not any(s in texts for s in ['#bootstrap=','otpauth://'])
    assert all(s in texts for s in ['72.34','10.96','11.72','97项','优选目标未达'])
pdf = PdfReader(ROOT/'中期汇报.pdf')
assert len(pdf.pages) == 10
receipt=json.loads((BUILD/'中期汇报.pptx.validation.json').read_text(encoding='utf-8'))
assert receipt['packageIntegrity']['status']=='pass'
assert receipt['presentationLayout']['finding_count']==0
artifacts=[]
for filename in ['中期汇报.pptx','中期汇报.pdf']:
    f=ROOT/filename
    artifacts.append({'path':filename,'bytes':f.stat().st_size,'sha256':sha256(f.read_bytes()).hexdigest()})
assert artifacts[0]['sha256']==receipt['finalSha256']
result={
    'actual_time':datetime.now(ZoneInfo('Asia/Shanghai')).isoformat(),
    'software_source_commit':'97b790a','base_head_before_materials':'4a6ae5e',
    'slides':10,'pdf_pages':10,'native_table_slide':3,'native_chart_slide':5,
    'embedded_chart_workbooks':len(workbooks),'genuine_unchanged_embedded_images':len(embedded),
    'notes_parts':len(notes),'planned_speech_seconds':300,
    'source_paths_exist':True,'template_sample_text_absent_on_slides':True,
    'pptx_finalizer_status':'pass','visual_review':{
        'not_automated':True,
        'see_manual_record':'每日工作记录/2026-10-16/FR18中期PPT与逐页检查.md',
        'native_powerpoint_application_run':False,
    },
    'artifacts':artifacts,'status':'pass',
    'limits':['可审阅初稿，未冻结','真机恢复、技能重启和可信Hook deny尚待本人',
              '97项回归为代理运行，截图22为本人亲跑旧版84项',
              'PDF为静态预览，可编辑内容及讲者备注在PPTX'],
}
(OUT/'材料检查.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(result,ensure_ascii=False,indent=2))

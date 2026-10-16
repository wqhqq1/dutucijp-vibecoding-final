"""Create a static ten-page PDF preview from inspected final PPTX renders.

The editable source is 中期汇报.pptx. This PDF intentionally contains slide images.
"""
from pathlib import Path
from reportlab.pdfgen.canvas import Canvas
from reportlab.lib.utils import ImageReader
from pypdf import PdfReader
import json

ROOT = Path(__file__).resolve().parents[1]
BUILD = ROOT / '.working' / 'midterm-20261016'
PAGES = BUILD / '中期汇报-previews'
PDF = ROOT / '中期汇报.pdf'

if PDF.exists():
    raise FileExistsError('Preserve existing review PDF; choose a new revision name.')
slides = [PAGES / f'slide-{i:02d}.png' for i in range(1, 11)]
assert all(p.is_file() for p in slides)
page_width, page_height = 12193200 / 12700, 6858000 / 12700
c = Canvas(str(PDF), pagesize=(page_width, page_height), pageCompression=1)
c.setTitle('退货对账与差异处理系统 中期汇报')
c.setAuthor('王麒皓 20251101030 机国际2502')
c.setSubject('截至2026-10-16的十页可审阅初稿，未冻结；静态预览，编辑请使用PPTX。')
for slide in slides:
    c.drawImage(ImageReader(str(slide)), 0, 0, width=page_width, height=page_height)
    c.showPage()
c.save()
reader = PdfReader(PDF)
assert len(reader.pages) == 10
assert all(abs(float(p.mediabox.width) - page_width) < .01 for p in reader.pages)
assert all(abs(float(p.mediabox.height) - page_height) < .01 for p in reader.pages)
(BUILD / 'pdf-check.json').write_text(json.dumps({
    'pages': len(reader.pages), 'width_pt': page_width, 'height_pt': page_height,
    'format': 'static image preview of final PPTX renders', 'editable_master': '中期汇报.pptx',
    'visual_check': 'pending until Poppler renders inspected',
}, ensure_ascii=False, indent=2), encoding='utf-8')
print(json.dumps({'pdf':str(PDF),'pages':len(reader.pages)}, ensure_ascii=False))

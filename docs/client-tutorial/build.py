#!/usr/bin/env python3
"""把 subscription-tutorial.md 生成为单文件 HTML（图片内嵌），可直接放到网站上或本地打开。

用法：python3 docs/client-tutorial/build.py [输出路径]
依赖：pip install markdown
"""
import base64
import re
import sys
from pathlib import Path

import markdown

DOCS = Path(__file__).resolve().parent
SRC = DOCS / "subscription-tutorial.md"

# 章节标题 -> 英文锚点（中文锚点在部分浏览器 / 嵌入环境中跳转不可靠）
ANCHORS = {
    "一、怎么选客户端": "choose",
    "二、客户端下载地址": "download",
    "三、客户端订阅": "clients",
    "四、按协议订阅（进阶）": "protocols",
    "五、常见问题": "faq",
}

CSS = """
:root{
  --bg:#f5f7fa; --surface:#ffffff; --surface-2:#f1f4f8; --text:#1f2a37; --muted:#5b6b7d;
  --line:#dfe5ec; --accent:#2b8ae6; --accent-soft:#e8f2fd; --warn-bg:#fff7e6; --warn-line:#f0c36d;
}
@media (prefers-color-scheme: dark){
  :root:not([data-theme="light"]){
    color-scheme:dark;
    --bg:#11161d; --surface:#18202a; --surface-2:#1f2935; --text:#e4eaf1; --muted:#9aa9b9;
    --line:#2c3846; --accent:#5aa8f2; --accent-soft:#1a2d42; --warn-bg:#2a2416; --warn-line:#8a6d2c;
  }
}
:root[data-theme="dark"]{
  color-scheme:dark;
  --bg:#11161d; --surface:#18202a; --surface-2:#1f2935; --text:#e4eaf1; --muted:#9aa9b9;
  --line:#2c3846; --accent:#5aa8f2; --accent-soft:#1a2d42; --warn-bg:#2a2416; --warn-line:#8a6d2c;
}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--text);
  font:15px/1.75 -apple-system,BlinkMacSystemFont,"PingFang SC","Hiragino Sans GB","Microsoft YaHei","Noto Sans CJK SC",sans-serif;
  padding-inline:16px;padding-block:32px 64px}
.page{max-width:820px;margin:0 auto;display:flex;flex-direction:column;gap:24px}
header.top{display:flex;flex-direction:column;gap:6px}
header.top h1{margin:0;font-size:28px;line-height:1.3;text-wrap:balance}
header.top p{margin:0;color:var(--muted)}
nav.toc{background:var(--surface);border:1px solid var(--line);border-radius:10px;padding:14px 18px}
nav.toc strong{display:block;font-size:13px;letter-spacing:.06em;color:var(--muted);margin-bottom:6px}
nav.toc ol{margin:0;padding:0;list-style:none;display:flex;flex-wrap:wrap;gap:8px}
nav.toc a{display:inline-block;padding:4px 12px;border-radius:999px;background:var(--accent-soft);color:var(--accent);text-decoration:none;font-size:14px}
nav.toc a:hover,nav.toc a:focus-visible{text-decoration:underline}
article{background:var(--surface);border:1px solid var(--line);border-radius:12px;padding:8px 24px 24px}
article h2{font-size:22px;margin:40px 0 12px;padding-top:16px;border-top:1px solid var(--line);scroll-margin-top:16px;text-wrap:balance}
article h2:first-of-type{border-top:0;margin-top:16px}
article h3{font-size:18px;margin:32px 0 8px;scroll-margin-top:16px}
article h4{font-size:15px;margin:20px 0 6px;color:var(--muted)}
article p,article li{max-width:68ch}
article a{color:var(--accent)}
article a:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
article img{display:block;max-width:100%;height:auto;margin:16px auto;border:1px solid var(--line);border-radius:10px}
article blockquote{margin:16px 0;padding:10px 16px;background:var(--warn-bg);border:1px solid var(--warn-line);border-radius:8px}
article blockquote p{margin:6px 0}
article code{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:.9em;background:var(--surface-2);padding:1px 5px;border-radius:4px}
.table-wrap{overflow-x:auto;margin:12px 0}
article table{border-collapse:collapse;width:100%;font-size:14px}
article th,article td{border:1px solid var(--line);padding:8px 10px;text-align:left;vertical-align:top}
article th{background:var(--surface-2);white-space:nowrap}
article hr{display:none}
footer{color:var(--muted);font-size:13px;text-align:center}
@media (max-width:560px){article{padding:4px 16px 20px}header.top h1{font-size:24px}}
"""


def embed_images(html: str) -> str:
    def repl(m):
        path = DOCS / m.group(1)
        data = base64.b64encode(path.read_bytes()).decode()
        return f'src="data:image/png;base64,{data}"'
    return re.sub(r'src="(images/[^"]+\.png)"', repl, html)


def build(full_document: bool = True) -> str:
    md = SRC.read_text(encoding="utf-8")
    # 去掉一级标题，改由页面头部显示
    md = re.sub(r"^# .*\n", "", md, count=1)
    md = md.replace("(#二客户端下载地址)", "(#download)")
    body = markdown.markdown(md, extensions=["tables", "sane_lists"])
    for title, anchor in ANCHORS.items():
        body = body.replace(f"<h2>{title}</h2>", f'<h2 id="{anchor}">{title}</h2>')
    body = re.sub(r"(<table>.*?</table>)", r'<div class="table-wrap">\1</div>', body, flags=re.S)
    body = re.sub(r'<a href="(https?://[^"]+)"', r'<a href="\1" target="_blank" rel="noopener"', body)
    body = embed_images(body)

    toc = "".join(f'<li><a href="#{a}">{t}</a></li>' for t, a in ANCHORS.items())
    content = f"""<title>客户端订阅教程</title>
<style>{CSS}</style>
<div class="page">
  <header class="top">
    <h1>客户端订阅使用教程</h1>
    <p>在面板获取订阅，导入 sing-box、v2rayN / v2rayNG、Clash、Surge、Shadowrocket、Loon、Quantumult X。</p>
  </header>
  <nav class="toc" aria-label="目录"><strong>目录</strong><ol>{toc}</ol></nav>
  <article>{body}</article>
  <footer>遇到问题请在面板提交工单，并附上客户端名称、版本和报错截图。</footer>
</div>
"""
    if not full_document:
        return content
    return ('<!DOCTYPE html>\n<html lang="zh-CN">\n<head>\n<meta charset="utf-8">\n'
            '<meta name="viewport" content="width=device-width, initial-scale=1">\n'
            + content.replace("<div class=\"page\">", "</head>\n<body>\n<div class=\"page\">", 1)
            + "</body>\n</html>\n")


if __name__ == "__main__":
    out = Path(sys.argv[1]) if len(sys.argv) > 1 else DOCS / "subscription-tutorial.html"
    fragment = "--fragment" in sys.argv
    if fragment:
        out = Path(sys.argv[1])
    out.write_text(build(full_document=not fragment), encoding="utf-8")
    print(f"written: {out} ({out.stat().st_size // 1024} KB)")

"""将程序实际输出的 ANSI 帧转成 HTML，便于审查中文排版和配色。"""
import argparse
import html
from pathlib import Path
import re
import subprocess
import unicodedata


def ansi_html(text):
    style = {"color": "#F8F8F2", "background-color": "#282a36"}
    output = []
    parts = re.split(r"(\x1b\[[0-9;:]*m)", text)
    for part in parts:
        if not part.startswith("\x1b["):
            css = ";".join(k + ":" + v for k, v in style.items())
            chunks = part.split("\n")
            for index, chunk in enumerate(chunks):
                if chunk:
                    content = "".join('<span class="wide">' + html.escape(c) + '</span>'
                                      if unicodedata.east_asian_width(c) in ("W", "F")
                                      else html.escape(c) for c in chunk)
                    output.append('<span class="run" style="' + css + '">' + content + '</span>')
                if index < len(chunks) - 1:
                    output.append('</div><div class="row">')
            continue
        values = [int(x) if x else 0 for x in part[2:-1].replace(":", ";").split(";")]
        i = 0
        while i < len(values):
            value = values[i]
            if value == 0:
                style = {"color": "#F8F8F2", "background-color": "#282a36"}
            elif value == 1:
                style["font-weight"] = "600"
            elif value == 22:
                style.pop("font-weight", None)
            elif value in (38, 48) and i + 4 < len(values) and values[i + 1] == 2:
                key = "color" if value == 38 else "background-color"
                style[key] = "#%02x%02x%02x" % tuple(values[i + 2:i + 5])
                i += 4
            elif value == 39:
                style["color"] = "#F8F8F2"
            elif value == 49:
                style["background-color"] = "#282a36"
            i += 1
    return '<div class="row">' + "".join(output) + "</div>"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", default="./bin/gdesk")
    parser.add_argument("--output", default="docs/preview.html")
    parser.add_argument("--width", type=int, default=112)
    parser.add_argument("--height", type=int, default=28)
    parser.add_argument("--view", choices=("gpu", "tasks"), default="gpu")
    args = parser.parse_args()
    command = [args.binary, "--demo", "--snapshot", "--width", str(args.width), "--height", str(args.height)]
    if args.view == "tasks":
        command.append("--gpus=false")
    text = subprocess.check_output(command, text=True)
    document = '''<!doctype html><html lang="zh-CN"><meta charset="UTF-8"><title>G-Desk · 终端预览</title>
<style>*{box-sizing:border-box}html,body{margin:0;padding:0;background:#282a36;color:#f8f8f2}pre{font-family:Menlo,"PingFang SC",monospace;font-size:16px;line-height:26px;letter-spacing:0;margin:0;padding:0;background:#282a36;font-variant-ligatures:none;width:max-content}.row{height:26px;white-space:pre}.run{display:inline-block;height:26px;vertical-align:top}.wide{display:inline-block;width:2ch;height:26px;vertical-align:top}</style><pre>''' + ansi_html(text.rstrip("\n")) + '</pre></html>'
    Path(args.output).write_text(document, encoding="utf-8")


if __name__ == "__main__":
    main()

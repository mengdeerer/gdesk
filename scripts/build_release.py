"""交叉构建 macOS / Linux 发布包；创建新目录，不覆盖既有构建。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile

TARGETS = (("darwin", "arm64"), ("darwin", "amd64"),
           ("linux", "arm64"), ("linux", "amd64"))


def dependency_notices(go, root):
    """发布二进制时附带依赖和 Go 运行时的原始许可证文本。"""
    decoder = json.JSONDecoder()
    modules = {}
    for system, arch in TARGETS:
        env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED="0")
        raw = subprocess.check_output([go, "list", "-deps", "-json", "./cmd/gdesk"],
                                      cwd=root, env=env, text=True)
        while raw.strip():
            package, end = decoder.raw_decode(raw.lstrip())
            raw = raw.lstrip()[end:]
            module = package.get("Module")
            if module and not module.get("Main"):
                modules[module["Path"]] = module
    notices = []
    for module in sorted(modules.values(), key=lambda m: m["Path"]):
        directory = Path(module["Dir"])
        licenses = sorted(p for p in directory.iterdir()
                          if p.is_file() and p.name.lower().startswith(("license", "copying")))
        if not licenses:
            raise RuntimeError(f"依赖缺少许可证文件: {module['Path']}")
        for path in licenses:
            notices.append(f"{module['Path']} {module['Version']} / {path.name}\n\n{path.read_text()}")
    goroot = Path(subprocess.check_output([go, "env", "GOROOT"], text=True).strip())
    notices.append("Go runtime\n\n" + (goroot / "LICENSE").read_text())
    return "\n\n" + ("\n\n" + "=" * 72 + "\n\n").join(notices)


def public_tar_info(info):
    """发布包不携带本机用户名、用户组或文件时间。"""
    info.uid = info.gid = 0
    info.uname = info.gname = ""
    info.mtime = 0
    info.pax_headers = {}
    return info


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--go", default="go")
    parser.add_argument("--output-dir", type=Path, help="指定尚不存在的输出目录，保留既有构建")
    args = parser.parse_args()
    if not re.fullmatch(r"v\d+\.\d+\.\d+(?:-[a-zA-Z0-9.-]+)?", args.version):
        parser.error("版本必须为 vX.Y.Z 或 vX.Y.Z-prerelease")
    root = Path(__file__).resolve().parents[1]
    output = args.output_dir.resolve() if args.output_dir is not None else root / "dist" / args.version
    output.mkdir(parents=True, exist_ok=False)
    notices = output / "THIRD_PARTY_NOTICES.txt"
    notices.write_text(dependency_notices(args.go, root), encoding="utf-8")
    sums = []
    for system, arch in TARGETS:
        name = f"gdesk_{args.version}_{system}_{arch}"
        folder = output / name
        folder.mkdir()
        env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED="0")
        print(f"构建 {system}/{arch}", flush=True)
        subprocess.run([
            args.go, "build", "-trimpath", "-ldflags",
            f"-s -w -X github.com/mengdeerer/gdesk/internal/app.Version={args.version}",
            "-o", str(folder / "gdesk"), "./cmd/gdesk",
        ], cwd=root, env=env, check=True)
        archive = output / f"{name}.tar.gz"
        with tarfile.open(archive, "w:gz") as tar:
            tar.add(folder / "gdesk", arcname="gdesk", filter=public_tar_info)
            tar.add(root / "README.md", arcname="README.md", filter=public_tar_info)
            tar.add(notices, arcname="THIRD_PARTY_NOTICES.txt", filter=public_tar_info)
        sums.append(f"{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}")
    (output / "checksums.txt").write_text("\n".join(sums) + "\n", encoding="utf-8")
    print(f"发布包与 SHA-256: {output}")


if __name__ == "__main__":
    main()

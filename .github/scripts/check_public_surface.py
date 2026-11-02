#!/usr/bin/env python3
"""Check that the public surface of this repository stays public.

Runs in CI for new commits and in the release jobs for packaged binaries. It
rejects private-document references, local environment narration, local
absolute paths, verification trailers, CJK outside allowlisted bilingual files,
and secret-like strings.

This script deliberately contains only generic patterns; personal identifiers
(local usernames, account ids, the production domain) belong in the private
workspace guard and must never be committed here.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
import tarfile
import zipfile
from pathlib import Path

# Generic private-document markers. UPPER-CASE .md names other than README and
# the license are private knowledge files; docs-local/ and its distinctive
# basenames are private by definition. These are document names, not personal
# data, so they are safe to match from a committed checker.
PRIVATE_DOC = re.compile(
    r"("
    r"\b(?<!LICENSE-)(?!README\b|THIRD_PARTY_NOTICES\b|LICENSE\b)[A-Z][A-Z0-9_-]{2,}\.md\b"
    r"|docs-local"
    r"|product-scope|documentation-map|workflow-windows-wsl|local-testing"
    r"|changelog\.md|lessons\.md"
    r"|\u00a7"
    r")"
)
# Private build-environment narration. Docker Desktop is intentionally NOT
# flagged: it is legitimate public deployment guidance.
ENV_NARRATION = re.compile(
    r"(WSL|goproxy\.cn|Git Bash|workstation|Arch Linux)",
    re.IGNORECASE,
)
VERIFICATION = re.compile(r"^\s*Verification:\s*", re.IGNORECASE | re.MULTILINE)
LOCAL_PATH = re.compile(r"([A-Za-z]:\\Users\\|/home/|/Users/|/mnt/)")
EMAIL = re.compile(r"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}")
CJK = re.compile(r"[\u4e00-\u9fff]")
# Secret shapes only, not the words "password"/"secret": those are ordinary
# domain vocabulary in this codebase.
SECRET = re.compile(
    r"(ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|"
    r"AKIA[0-9A-Z]{16}|xox[bp]-[A-Za-z0-9-]{10,}|"
    r"BEGIN [A-Z ]*PRIVATE KEY|sk-[A-Za-z0-9]{20,}|"
    r"\b\d{8,10}:[A-Za-z0-9_-]{30,}\b)",
    re.IGNORECASE,
)

TREE_ALLOW = {
    "README.md": {"cjk"},
    "compose.example.yaml": {"cjk"},
    "deploy/windwatch.service": {"cjk"},
    "internal/i18n/locales/": {"cjk"},
    "internal/web/templates/": {"cjk"},
    "*_test.go": {"cjk"},
    ".dockerignore": {"private-doc"},
    "go.sum": {"secret"},
    ".github/scripts/check_public_surface.py": {"all"},
}

# Binary asset scanning: a verification trailer must never be baked into a
# shipped binary, and no local user path may survive the build. CI runner
# paths are expected and allowed.
BINARY_PATTERNS = [
    (b"Verification:", "verification trailer"),
]
PRINTABLE = re.compile(rb"[\x20-\x7e]{6,}")
USER_PATH = re.compile(
    r"(?:C:\\Users\\(?!(?:runner|runneradmin|Public|Default|All Users)\\)[A-Za-z0-9_.-]+"
    r"|/home/(?!(?:runner)/)[A-Za-z0-9_.-]+"
    r"|/Users/(?!(?:runner)/)[A-Za-z0-9_.-]+"
    r"|/mnt/[a-z]/[A-Za-z0-9_.-]+)"
)


def allow_for(path: str) -> set[str]:
    if path in TREE_ALLOW:
        return TREE_ALLOW[path]
    for key, allow in TREE_ALLOW.items():
        if key.endswith("/") and path.startswith(key):
            return allow
    for key, allow in TREE_ALLOW.items():
        if key.startswith("*") and path.endswith(key[1:]):
            return allow
    return set()


def run_git(repo: Path, args: list[str]) -> str:
    result = subprocess.run(
        ["git", "-C", str(repo), *args],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=True,
    )
    return result.stdout.decode("utf-8", "replace")


def scan_text(kind: str, path: str, text: str, allow: set[str], errors: list[str]) -> None:
    if "all" in allow:
        return
    for number, line in enumerate(text.splitlines(), 1):
        checks = [
            ("private-doc", PRIVATE_DOC),
            ("env-narration", ENV_NARRATION),
            ("local-path", LOCAL_PATH),
            ("secret", SECRET),
        ]
        for name, pattern in checks:
            if name not in allow and pattern.search(line):
                errors.append(f"{kind} {path}:{number}: {name}: {line.strip()[:160]}")
        if "email" not in allow and EMAIL.search(line):
            value = EMAIL.search(line).group(0).lower()
            if not (
                value.endswith("users.noreply.github.com")
                or re.search(r"@example\.(com|org|net)$", value)
            ):
                errors.append(f"{kind} {path}:{number}: email: {line.strip()[:160]}")
        if "cjk" not in allow and CJK.search(line):
            errors.append(f"{kind} {path}:{number}: cjk: {line.strip()[:160]}")
        if VERIFICATION.search(line):
            errors.append(f"{kind} {path}:{number}: verification-tail: {line.strip()[:160]}")


def scan_message(path: str, text: str, errors: list[str]) -> None:
    scan_text("message", path, text, set(), errors)


def scan_range(repo: Path, rev_range: str, errors: list[str]) -> None:
    try:
        text = run_git(repo, ["log", "--no-merges", "--format=%B%x1e", rev_range])
    except subprocess.CalledProcessError:
        text = run_git(repo, ["log", "-1", "--format=%B"])
        scan_message("HEAD", text, errors)
        return
    for index, body in enumerate(text.split("\x1e"), 1):
        if body.strip():
            scan_message(f"{rev_range}#{index}", body, errors)


def scan_tree(repo: Path, errors: list[str]) -> None:
    for name in run_git(repo, ["ls-files", "-z"]).split("\x00"):
        if not name:
            continue
        path = repo / name
        if not path.is_file():
            continue
        data = path.read_bytes()
        if b"\x00" in data[:4096]:
            continue
        scan_text("tree", name, data.decode("utf-8", "replace"), allow_for(name), errors)


def scan_tag(repo: Path, tag: str, errors: list[str]) -> None:
    text = run_git(repo, ["tag", "-l", "-n99", tag])
    # Release notes are bilingual English/Chinese by design.
    scan_text("tag", f"tag:{tag}", text, {"cjk"}, errors)


def scan_assets(root: Path, errors: list[str]) -> None:
    if root.is_file():
        scan_asset_file(root, errors)
        return
    for path in root.rglob("*"):
        if path.is_file():
            scan_asset_file(path, errors)


def scan_asset_file(path: Path, errors: list[str]) -> None:
    suffix = path.suffix.lower()
    if suffix == ".zip":
        with zipfile.ZipFile(path) as archive:
            for entry in archive.infolist():
                scan_binary(path, entry.filename, archive.read(entry), errors)
    elif suffix in {".gz", ".tgz"} and path.name.endswith((".tar.gz", ".tgz")):
        with tarfile.open(path, "r:*") as archive:
            for member in archive.getmembers():
                if member.isfile():
                    scan_binary(path, member.name, archive.extractfile(member).read(), errors)
    else:
        scan_binary(path, "", path.read_bytes(), errors)


def scan_binary(path: Path, entry: str, data: bytes, errors: list[str]) -> None:
    label = f"{path}!{entry}" if entry else str(path)
    for needle, reason in BINARY_PATTERNS:
        if needle in data:
            errors.append(f"asset {label}: {reason}: {needle.decode(errors='replace')}")
    for run in PRINTABLE.finditer(data):
        text = run.group().decode("ascii", "ignore")
        if USER_PATH.search(text):
            errors.append(f"asset {label}: local user path: {text[:120]}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", default=".")
    parser.add_argument("--range")
    parser.add_argument("--tag")
    parser.add_argument("--tree", action="store_true")
    parser.add_argument("--assets", action="append")
    args = parser.parse_args()

    repo = Path(args.repo).resolve()
    errors: list[str] = []
    if args.range:
        scan_range(repo, args.range, errors)
    if args.tag:
        scan_tag(repo, args.tag, errors)
    if args.tree:
        scan_tree(repo, errors)
    for asset in args.assets or []:
        scan_assets(Path(asset), errors)

    if errors:
        for error in errors:
            print(f"ERROR {error}")
        print(f"public_surface: {len(errors)} finding(s)")
        return 1
    print("public_surface: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())

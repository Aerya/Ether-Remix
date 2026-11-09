#!/usr/bin/env python3
"""Applique un delta Ether à trois voies sans importer ses ancêtres Git."""
import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

BASE_FILE = Path(".github/pse/upstream-base")
# Les workflows upstream sont examinés séparément, jamais publiés par le robot.
PROTECTED = [
    ".github", "AGENTS.md", "README.md", "README.fr.md", "README.en.md",
    "docs/pse", "internal/pse", "internal/catalog/pse_tier_sources.go", "web/src/pse", "internal/admin/dist",
    "web/public/brand", "web/src/components/brand.tsx", "web/src/lib/brand-art.json", "web/index.html",
]


def git(*args, **kwargs):
    return subprocess.run(["git", *args], check=True, **kwargs)


def merge_catalogs():
    """Résout les conflits de lignes JSON si les clés modifiées sont distinctes."""
    paths = git("diff", "--name-only", "--diff-filter=U", stdout=subprocess.PIPE, text=True).stdout.splitlines()
    missing = object()
    for name in paths:
        if not re.fullmatch(r"web/src/i18n/locales/[A-Za-z-]+\.json", name):
            continue
        try:
            versions = [json.loads(git("show", f":{stage}:{name}", stdout=subprocess.PIPE, text=True).stdout) for stage in (1, 2, 3)]
        except (json.JSONDecodeError, subprocess.CalledProcessError):
            continue
        if not all(isinstance(v, dict) for v in versions):
            continue
        result = {}
        for key in set().union(*versions):
            base, ours, theirs = [v.get(key, missing) for v in versions]
            if ours == theirs or theirs == base:
                value = ours
            elif ours == base:
                value = theirs
            else:
                break  # Même clé modifiée différemment : résolution humaine.
            if value is not missing:
                result[key] = value
        else:
            Path(name).write_text(json.dumps(dict(sorted(result.items())), ensure_ascii=False, indent=2) + "\n")
            git("add", name)
    return git("diff", "--name-only", "--diff-filter=U", stdout=subprocess.PIPE, text=True).stdout.strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("target", help="Commit Ether à intégrer")
    args = parser.parse_args()
    if git("status", "--porcelain", stdout=subprocess.PIPE).stdout:
        parser.error("Le dossier de travail doit être propre.")
    base = BASE_FILE.read_text().strip()
    if not re.fullmatch(r"[0-9a-f]{40}", base):
        parser.error("Révision de référence invalide.")
    target = git("rev-parse", "--verify", args.target + "^{commit}",
                 stdout=subprocess.PIPE, text=True).stdout.strip()
    git("merge-base", "--is-ancestor", base, target)
    patch = git("diff", "--binary", "--full-index", base, target, "--", ".",
                *[":(top,exclude)" + path for path in PROTECTED],
                stdout=subprocess.PIPE).stdout
    if patch:
        result = subprocess.run(["git", "apply", "--3way", "--index"], input=patch, capture_output=True)
        sys.stdout.buffer.write(result.stdout)
        sys.stderr.buffer.write(result.stderr)
        if result.returncode:
            # Les catalogues traduits peuvent se chevaucher en lignes sans
            # modifier les mêmes messages. Les vrais conflits restent bloquants.
            if any(line.startswith((b"error:", b"fatal:")) for line in result.stderr.splitlines()):
                return 2
            unmerged = git("diff", "--name-only", "--diff-filter=U", stdout=subprocess.PIPE, text=True).stdout.strip()
            if not unmerged or merge_catalogs():
                return 2
    BASE_FILE.write_text(target + "\n")
    git("add", str(BASE_FILE))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

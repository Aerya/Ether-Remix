#!/usr/bin/env python3
"""Applique un delta Ether à trois voies sans importer ses ancêtres Git."""
import argparse
import re
import subprocess
from pathlib import Path

BASE_FILE = Path(".github/pse/upstream-base")
# Les workflows upstream sont examinés séparément, jamais publiés par le robot.
PROTECTED = [
    ".github", "README.md", "README.fr.md", "README.en.md",
    "docs/pse", "internal/pse", "web/src/pse", "internal/admin/dist",
    "web/public/brand", "web/src/components/brand.tsx", "web/src/lib/brand-art.json", "web/index.html",
]


def git(*args, **kwargs):
    return subprocess.run(["git", *args], check=True, **kwargs)


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
        result = subprocess.run(["git", "apply", "--3way", "--index"], input=patch)
        if result.returncode:
            # L'index reste disponible pour une résolution manuelle.
            return 2
    BASE_FILE.write_text(target + "\n")
    git("add", str(BASE_FILE))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

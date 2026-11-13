#!/usr/bin/env python3
"""Fusionne Ether à trois voies dans le fork, sans importer ses ancêtres Git.

La fusion passe par trois commits temporaires qui partagent la base
enregistrée : la base, le fork et la cible Ether (chemins protégés ramenés à
la base). Git suit ainsi les fichiers et dossiers déplacés par Ether, et un
conflit reste visible dans l'index et dans les fichiers, comme une fusion
ordinaire. La branche courante ne reçoit aucun commit : le résultat est
seulement indexé.
"""
import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path

BASE_FILE = Path(".github/pse/upstream-base")
# Les workflows upstream sont examinés séparément, jamais publiés par le robot.
PROTECTED = [
    ".github", "AGENTS.md", "README.md", "README.fr.md", "README.en.md",
    "docs/pse", "internal/pse", "internal/catalog/pse_tier_sources.go", "web/src/pse", "internal/admin/dist", "internal/admin/pse_debrid_keys.go", "internal/admin/pse_debrid_keys_test.go",
    "web/public/brand", "web/src/components/brand.tsx", "web/src/lib/brand-art.json", "web/index.html",
]
# Identité des commits temporaires : ils ne sont jamais publiés.
IDENTITY = {
    "GIT_AUTHOR_NAME": "Ether-Remix sync", "GIT_AUTHOR_EMAIL": "sync@invalid",
    "GIT_COMMITTER_NAME": "Ether-Remix sync", "GIT_COMMITTER_EMAIL": "sync@invalid",
}


def git(*args, env=None, **kwargs):
    return subprocess.run(["git", *args], check=True, env=env, **kwargs)


def out(*args, env=None):
    return git(*args, env=env, stdout=subprocess.PIPE, text=True).stdout.strip()


def unmerged():
    return out("diff", "--name-only", "--diff-filter=U")


def merge_catalogs():
    """Résout les conflits de lignes JSON si les clés modifiées sont distinctes."""
    missing = object()
    for name in unmerged().splitlines():
        if not re.fullmatch(r"web/src/i18n/locales/[A-Za-z-]+\.json", name):
            continue
        try:
            versions = [json.loads(out("show", f":{stage}:{name}")) for stage in (1, 2, 3)]
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
    return unmerged()


def filtered_target(base, target, env):
    """Arbre de la cible où les chemins protégés gardent leur état de base."""
    index = Path(out("rev-parse", "--git-path", "pse-sync-index")).resolve()
    tenv = dict(env, GIT_INDEX_FILE=str(index))
    try:
        git("read-tree", target, env=tenv)
        git("rm", "-r", "-q", "-f", "--cached", "--ignore-unmatch", "--", *PROTECTED, env=tenv)
        entries = git("ls-tree", "-r", "--full-tree", "-z", base, "--", *PROTECTED, env=tenv,
                      stdout=subprocess.PIPE).stdout
        if entries:
            git("update-index", "-z", "--index-info", env=tenv, input=entries)
        return out("write-tree", env=tenv)
    finally:
        index.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("target", help="Commit Ether à intégrer")
    args = parser.parse_args()
    if git("status", "--porcelain", stdout=subprocess.PIPE).stdout:
        parser.error("Le dossier de travail doit être propre.")
    base = BASE_FILE.read_text().strip()
    if not re.fullmatch(r"[0-9a-f]{40}", base):
        parser.error("Révision de référence invalide.")
    target = out("rev-parse", "--verify", args.target + "^{commit}")
    git("merge-base", "--is-ancestor", base, target)
    env = dict(os.environ, **IDENTITY)
    head = out("rev-parse", "HEAD")
    branch = subprocess.run(["git", "symbolic-ref", "-q", "HEAD"], stdout=subprocess.PIPE, text=True).stdout.strip()

    root = out("commit-tree", base + "^{tree}", "-m", "base", env=env)
    ours = out("commit-tree", head + "^{tree}", "-p", root, "-m", "fork", env=env)
    theirs = out("commit-tree", filtered_target(base, target, env), "-p", root, "-m", "ether", env=env)

    # Le fork temporaire a l'arbre de HEAD : le détachement ne touche à rien.
    git("checkout", "-q", "--detach", ours)
    merge = subprocess.run(
        ["git", "-c", "merge.renames=true", "-c", "merge.directoryRenames=true",
         "-c", "merge.conflictStyle=diff3", "merge", "--no-ff", "--no-commit", "-q", theirs],
        env=env, capture_output=True, text=True)
    sys.stdout.write(merge.stdout)
    sys.stderr.write(merge.stderr)
    # Revenir sur la branche d'origine sans toucher à l'index ni aux fichiers.
    for name in ("MERGE_HEAD", "MERGE_MSG", "MERGE_MODE", "AUTO_MERGE"):
        Path(out("rev-parse", "--git-path", name)).unlink(missing_ok=True)
    if branch:
        git("symbolic-ref", "HEAD", branch)
    else:
        git("update-ref", "--no-deref", "HEAD", head)

    if merge.returncode:
        # Les catalogues traduits peuvent se chevaucher en lignes sans
        # modifier les mêmes messages. Les vrais conflits restent bloquants.
        if not unmerged() or merge_catalogs():
            return 2
    BASE_FILE.write_text(target + "\n")
    git("add", str(BASE_FILE))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

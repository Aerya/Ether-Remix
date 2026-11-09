import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("apply-upstream.py").resolve()


class UpstreamTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.git("init", "-q", "-b", "ether")
        self.git("config", "user.name", "Test")
        self.git("config", "user.email", "test@example.org")
        self.put("code.txt", "first\n" + "middle\n" * 15 + "last\n")
        self.put("README.md", "Ether\n")
        self.put(".github/workflows/upstream.yml", "old\n")
        self.put("internal/admin/dist/index.html", "generated old\n")
        self.put("delete.txt", "delete me\n")
        self.put("move.txt", "move me\n")
        self.base = self.commit()

    def git(self, *args, check=True):
        return subprocess.run(["git", *args], cwd=self.root, check=check,
                              capture_output=True, text=True).stdout.strip()

    def put(self, name, text):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)

    def commit(self):
        self.git("add", "-A")
        self.git("commit", "-qm", "fixture")
        return self.git("rev-parse", "HEAD")

    def fork(self, code=None):
        self.git("checkout", "--orphan", "accent")
        self.git("rm", "-rf", ".")
        self.put("code.txt", code or "first\n" + "middle\n" * 15 + "Accent\n")
        self.put("README.md", "Accent\n")
        self.put(".github/workflows/upstream.yml", "protected\n")
        self.put("internal/admin/dist/index.html", "Accent generated\n")
        self.put("delete.txt", "delete me\n")
        self.put("move.txt", "move me\n")
        self.put(".github/pse/upstream-base", self.base + "\n")
        return self.commit()

    def apply(self, target):
        return subprocess.run(["python3", str(SCRIPT), target], cwd=self.root,
                              capture_output=True, text=True)

    def test_delta_preserves_fork_and_public_history(self):
        self.put("code.txt", "upstream\n" + "middle\n" * 15 + "last\n")
        self.put("README.md", "new Ether\n")
        self.put(".github/workflows/upstream.yml", "new upstream workflow\n")
        self.put("internal/admin/dist/index.html", "new generated\n")
        (self.root / "delete.txt").unlink()
        self.git("mv", "move.txt", "moved.txt")
        (self.root / "binary.bin").write_bytes(b"\x00\xff\x03")
        target = self.commit()
        parent = self.fork()
        result = self.apply(target)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("upstream", (self.root / "code.txt").read_text())
        self.assertIn("Accent", (self.root / "code.txt").read_text())
        self.assertEqual((self.root / "README.md").read_text(), "Accent\n")
        self.assertEqual((self.root / ".github/workflows/upstream.yml").read_text(), "protected\n")
        self.assertEqual((self.root / "internal/admin/dist/index.html").read_text(), "Accent generated\n")
        self.assertFalse((self.root / "delete.txt").exists())
        self.assertTrue((self.root / "moved.txt").exists())
        self.assertEqual((self.root / "binary.bin").read_bytes(), b"\x00\xff\x03")
        self.assertEqual((self.root / ".github/pse/upstream-base").read_text().strip(), target)
        self.commit()
        self.assertEqual(self.git("show", "-s", "--format=%P", "HEAD"), parent)
        self.assertEqual(self.git("rev-list", "--count", "HEAD"), "2")

    def test_conflict_does_not_advance_checkpoint(self):
        self.put("code.txt", "conflicting upstream\n")
        target = self.commit()
        self.fork("conflicting Accent\n")
        result = self.apply(target)
        self.assertEqual(result.returncode, 2)
        self.assertIn("code.txt", self.git("diff", "--name-only", "--diff-filter=U"))
        self.assertEqual((self.root / ".github/pse/upstream-base").read_text().strip(), self.base)

    def test_no_change_is_clean(self):
        self.fork()
        self.assertEqual(self.apply(self.base).returncode, 0)
        self.assertEqual(self.git("status", "--porcelain"), "")

    def test_rejects_dirty_worktree_and_unrelated_target(self):
        fork = self.fork()
        self.assertNotEqual(self.apply(fork).returncode, 0)
        self.put("local.txt", "unsaved work\n")
        self.assertNotEqual(self.apply(self.base).returncode, 0)
        self.assertEqual((self.root / "local.txt").read_text(), "unsaved work\n")



    def test_independent_json_changes_merge_without_losing_translations(self):
        self.put("web/src/i18n/locales/fr.json", '{"a":"un", "b":"deux", "c":"trois"}\n')
        self.base = self.commit()
        self.put("web/src/i18n/locales/fr.json", '{"a":"amélioré", "b":"deux", "c":"trois"}\n')
        target = self.commit()
        self.fork()
        self.put("web/src/i18n/locales/fr.json", '{"a":"un", "b":"Remix", "c":"trois"}\n')
        self.commit()
        result = self.apply(target)
        self.assertEqual(result.returncode, 0, result.stderr)
        import json
        self.assertEqual(json.loads((self.root / "web/src/i18n/locales/fr.json").read_text()), {"a":"amélioré", "b":"Remix", "c":"trois"})

    def test_same_message_conflict_stays_blocking(self):
        self.put("web/src/i18n/locales/fr.json", '{"a":"un"}\n')
        self.base = self.commit()
        self.put("web/src/i18n/locales/fr.json", '{"a":"upstream"}\n')
        target = self.commit()
        self.fork()
        self.put("web/src/i18n/locales/fr.json", '{"a":"Remix"}\n')
        self.commit()
        result = self.apply(target)
        self.assertEqual(result.returncode, 2)
        self.assertEqual((self.root / ".github/pse/upstream-base").read_text().strip(), self.base)

    def test_non_merge_error_cannot_be_hidden_by_catalog_resolution(self):
        self.put("web/src/i18n/locales/fr.json", '{"a":"un", "b":"deux"}\n')
        self.base = self.commit()
        self.put("web/src/i18n/locales/fr.json", '{"a":"upstream", "b":"deux"}\n')
        self.put("code.txt", "new upstream code\n")
        target = self.commit()
        self.fork()
        self.put("web/src/i18n/locales/fr.json", '{"a":"un", "b":"Remix"}\n')
        (self.root / "code.txt").unlink()
        self.commit()
        result = self.apply(target)
        self.assertEqual(result.returncode, 2)
        self.assertEqual((self.root / ".github/pse/upstream-base").read_text().strip(), self.base)


if __name__ == "__main__":
    unittest.main()

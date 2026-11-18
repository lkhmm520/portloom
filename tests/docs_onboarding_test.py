import os
import pathlib
import re
import shutil
import stat
import subprocess
import tarfile
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
UPGRADE_PAGES = ("docs/operations/backup-upgrade.md", "docs/en/operations/backup-upgrade.md")
CLI_README = "cmd/portloomctl/README.md"


def shell_fences(relative):
    text = (ROOT / relative).read_text()
    for match in re.finditer(r"^( *)```(sh|bash)\n(.*?)^\1```[ \t]*$", text, re.M | re.S):
        yield text.count("\n", 0, match.start()) + 1, match[2], match[3]


def executable(path, text):
    path.write_text(text)
    path.chmod(0o700)


class ExecutableReleaseDocsTest(unittest.TestCase):
    def test_shell_examples_parse_as_written(self):
        for relative in (CLI_README, *UPGRADE_PAGES):
            fences = list(shell_fences(relative))
            self.assertTrue(fences, relative)
            for line, shell, snippet in fences:
                with self.subTest(path=relative, line=line):
                    self.assertNotRegex(snippet, r"(?m)(?:^|[ =])<[^<>\s]+>")
                    result = subprocess.run(
                        [shell, "-n"], input=snippet, text=True, capture_output=True
                    )
                    self.assertEqual(0, result.returncode, result.stderr)

    def test_cutover_fences_require_and_forward_the_confirmation(self):
        snippets = [snippet for _, _, snippet in shell_fences(CLI_README) if "--confirm" in snippet]
        self.assertEqual(3, len(snippets))
        with tempfile.TemporaryDirectory() as tmp:
            work = pathlib.Path(tmp)
            log = work / "args"
            executable(work / "portloomctl", '#!/bin/sh\nprintf "%s\\n" "$@" > args\n')
            for verb, snippet in zip(("switch", "settle", "rollback"), snippets):
                with self.subTest(verb=verb):
                    env = {"PATH": os.defpath}
                    failed = subprocess.run(["sh", "-c", snippet], cwd=work, env=env, capture_output=True)
                    self.assertNotEqual(0, failed.returncode)
                    self.assertIn(b"PORTLOOM_CONFIRM", failed.stderr)
                    self.assertFalse(log.exists())
                    digest = "af" * 32
                    env["PORTLOOM_CONFIRM"] = digest
                    subprocess.run(["sh", "-c", snippet], cwd=work, env=env, check=True)
                    self.assertEqual([
                        verb, "--config", "/secure/switch.json", "--operation", "media-move-1",
                        "--confirm", digest,
                    ], log.read_text().splitlines())
                    log.unlink()

    def test_bilingual_upgrade_fences_pass_the_real_installer_guard(self):
        for relative in UPGRADE_PAGES:
            snippets = [snippet for _, _, snippet in shell_fences(relative) if '"$installer"' in snippet]
            self.assertEqual(1, len(snippets), relative)
            with self.subTest(path=relative), tempfile.TemporaryDirectory() as tmp:
                self.exercise_upgrade(pathlib.Path(tmp), snippets[0])

    def exercise_upgrade(self, work, snippet):
        home = work / "custom install"
        installer = work / "install-server.sh"
        shutil.copy2(ROOT / "docs/public/install-server.sh", installer)
        state = work / "state"
        log = work / "events"
        executable(work / "docker", '''#!/usr/bin/env bash
set -eu
case "$1" in
  compose)
    case " $* " in
      ' compose version ') ;;
      *' ps -q '*) grep -qx true "$STATE"; printf 'portloom-%s\\n' "${@: -1}" ;;
      *' stop server sshd ') echo false > "$STATE"; echo stop >> "$EVENTS" ;;
      *' start server sshd ') echo true > "$STATE"; echo start >> "$EVENTS" ;;
      *' up -d '*) echo up >> "$EVENTS" ;;
      *) exit 1 ;;
    esac ;;
  inspect)
    name=${@: -1}; role=${name#portloom-}
    case "$role" in server) digit=a ;; sshd) digit=c ;; *) exit 1 ;; esac
    printf -v id '%064d' 0; id=sha256:${id//0/$digit}
    case "$3" in
      '{{.Name}} {{.Image}}') printf '/%s %s\\n' "$name" "$id" ;;
      *) printf '%s|%s|%s|%s\\n' "$SERVER_HOME" "$role" "$id" "$(<"$STATE")" ;;
    esac ;;
  image)
    [[ ${@: -1} == *-server:* ]] && digit=d || digit=e
    printf -v id '%064d' 0; printf 'sha256:%s\\n' "${id//0/$digit}" ;;
  pull|run|exec) ;;
  *) exit 1 ;;
esac
''')
        executable(work / "sudo", '''#!/bin/sh
set -eu
[ "$1" != -v ] || exit 0
case "$1 $2" in
  'tar -cpf')
    grep -qx false "$STATE"
    echo archive >> "$EVENTS"
    [ "${FAIL_ARCHIVE:-}" != yes ] || exit 42 ;;
  'tar -df') echo verify >> "$EVENTS" ;;
esac
exec "$@"
''')
        domain = "loom.example.test"
        ports = {"WEB": "18080", "SSH": "12222", "GATEWAY": "18081", "HTTP": "10080", "HTTPS": "10443"}
        env = {
            "HOME": str(work), "PATH": f"{work}:{os.defpath}", "STATE": str(state), "EVENTS": str(log),
            "SERVER_HOME": str(home), "SERVER_DOMAIN": domain,
        }
        config = {"DOMAIN": domain, **{f"{k}_PORT": v for k, v in ports.items()}}
        env.update({f"SERVER_{k}": v for k, v in config.items()})
        for name in ("server-data", "ssh-auth", "ssh-hostkeys"):
            (home / name).mkdir(parents=True)
        (home / "server-data/portloom.db").write_bytes(b"offline state fixture")
        saved_env = "".join(f"PORTLOOM_{k}={v}\n" for k, v in config.items())
        for role, digit in (("SERVER", "a"), ("SSHD", "c")):
            saved_env += f"PORTLOOM_{role}_IMAGE=ghcr.io/lkhmm520/portloom-{role.lower()}:0.4.6\n"
            saved_env += f"PORTLOOM_{role}_IMAGE_ID=sha256:{digit * 64}\n"
        (home / ".env").write_text(saved_env)
        (home / "compose.yml").write_text("# original Compose fixture\n")
        state.write_text("false\n")
        guard = subprocess.run([
            str(installer), "--home", str(home), "--domain", domain, "--version", "0.5.0",
            *[arg for key in ("WEB", "SSH", "HTTP", "HTTPS") for arg in (f"--{key.lower()}-port", ports[key])],
        ], env={**env, "PORTLOOM_GATEWAY_PORT": ports["GATEWAY"]}, capture_output=True, text=True)
        self.assertNotEqual(0, guard.returncode)
        self.assertIn("running server container is not active", guard.stderr)
        self.assertEqual(saved_env, (home / ".env").read_text())

        state.write_text("true\n")
        failed = subprocess.run(["bash", "-c", snippet], cwd=work, env={**env, "FAIL_ARCHIVE": "yes"},
                                capture_output=True, text=True)
        self.assertEqual(42, failed.returncode, failed.stderr)
        self.assertEqual(["stop", "archive"], log.read_text().splitlines())
        self.assertEqual("false\n", state.read_text())
        self.assertEqual(saved_env, (home / ".env").read_text())

        state.write_text("true\n")
        log.unlink()
        upgraded = subprocess.run(["bash", "-c", snippet], cwd=work, env=env, capture_output=True, text=True)
        self.assertEqual(0, upgraded.returncode, upgraded.stderr)
        self.assertEqual(["stop", "archive", "verify", "start", "up"], log.read_text().splitlines())
        archive, = work.glob("portloom-server-v0.4.6.*/server-install.tar")
        self.assertEqual(0o700, stat.S_IMODE(archive.parent.stat().st_mode))
        self.assertEqual(f"/portloom-server sha256:{'a' * 64}\n/portloom-sshd sha256:{'c' * 64}\n",
                         (archive.parent / "images.txt").read_text())
        with tarfile.open(archive) as backup:
            self.assertIn("ssh-hostkeys", backup.getnames())
            self.assertIn("ssh-auth", backup.getnames())
            for name, expected in ((".env", saved_env.encode()), ("server-data/portloom.db", b"offline state fixture")):
                member = backup.extractfile(name)
                assert member is not None, name
                with member:
                    self.assertEqual(expected, member.read())
        self.assertEqual(saved_env, (home / "native-upgrade-backup-0.5.0/.env").read_text())
        self.assertIn("portloom-server:0.5.0\n", (home / ".env").read_text())


class BeginnerComposeOnboardingTest(unittest.TestCase):
    def test_beginner_compose_bundle_and_pages_are_published(self):
        expected_files = [
            ROOT / "examples" / "compose.yml",
            ROOT / "examples" / "compose.env.example",
            ROOT / "docs" / "guide" / "compose-install.md",
            ROOT / "docs" / "en" / "guide" / "compose-install.md",
        ]
        for path in expected_files:
            with self.subTest(path=path):
                self.assertTrue(path.is_file(), f"missing beginner artifact: {path}")

        sync_script = (ROOT / "scripts" / "sync-doc-examples.mjs").read_text()
        self.assertIn("'compose.yml'", sync_script)
        self.assertIn("'compose.env.example'", sync_script)

    def test_synced_downloads_are_readable_by_the_unprivileged_docs_server(self):
        subprocess.run(["node", "scripts/sync-doc-examples.mjs"], cwd=ROOT, check=True)
        for name in ["compose.yml", "compose.env.example"]:
            path = ROOT / "docs" / "public" / "examples" / name
            with self.subTest(path=path):
                self.assertEqual(0o644, stat.S_IMODE(path.stat().st_mode))

    def test_compose_install_is_a_primary_navigation_path(self):
        config = (ROOT / "docs" / ".vitepress" / "config.mts").read_text()
        for link in ["/guide/compose-install", "/en/guide/compose-install"]:
            with self.subTest(link=link):
                self.assertIn(link, config)

        for relative in [
            "README.md",
            "README.en.md",
            "docs/index.md",
            "docs/en/index.md",
            "docs/guide/quick-start.md",
            "docs/en/guide/quick-start.md",
            "docs/install/docker.md",
            "docs/en/install/docker.md",
        ]:
            text = (ROOT / relative).read_text()
            with self.subTest(path=relative):
                self.assertIn("compose-install", text)

        home_flow = (ROOT / "docs" / ".vitepress" / "theme" / "components" / "HomeFlow.vue").read_text()
        self.assertIn("compose.yml", home_flow)
        self.assertNotIn("Run one script", home_flow)
        self.assertNotIn("执行一个脚本", home_flow)
        self.assertNotIn("portloom.look4i.com", config)

    def test_beginner_env_template_fails_closed_until_required_values_are_set(self):
        env_template = (ROOT / "examples" / "compose.env.example").read_text()
        self.assertIn("\nTM_PUBLIC_HOST=\n", env_template)
        self.assertIn("\nTM_ADMIN_TOKEN=\n", env_template)
        self.assertNotIn("REPLACE_WITH_A_LONG_RANDOM_VALUE", env_template)

    def test_beginner_template_is_repeatable_pinned_and_token_safe(self):
        compose = (ROOT / "examples" / "compose.yml").read_text()
        env_template = (ROOT / "examples" / "compose.env.example").read_text()
        self.assertIn("ghcr.io/lkhmm520/portloom-server:0.5.0", compose)
        self.assertIn("ghcr.io/lkhmm520/portloom-sshd:0.5.0", compose)
        self.assertNotIn(":latest", compose)
        self.assertIn("./data/server:/data", compose)
        self.assertIn("./data/ssh-auth:/auth", compose)
        self.assertIn("chown 0:0 /data/certs", compose)
        self.assertLess(
            compose.index("chown 0:0 /data/certs"), compose.index("chmod 0700 /data /data/certs /auth")
        )
        advanced_compose = (ROOT / "examples" / "docker-compose.server.yml").read_text()
        self.assertIn("chown 0:0 /data/certs", advanced_compose)
        self.assertLess(
            advanced_compose.index("chown 0:0 /data/certs"),
            advanced_compose.index("chmod 0700 /data /data/certs /auth"),
        )
        self.assertIn("openssl rand -hex 32", env_template)
        self.assertNotIn("PORTLOOM_SERVER_IMAGE=", env_template)
        self.assertNotIn("TM_SERVER_DATA_DIR=", env_template)
        self.assertNotIn("TM_TLS_CACHE_DIR=", env_template)
        self.assertIn("TM_TLS_CACHE_DIR: /data/certs", compose)

        for relative in ["docs/guide/compose-install.md", "docs/en/guide/compose-install.md"]:
            text = (ROOT / relative).read_text()
            with self.subTest(path=relative):
                self.assertIn("openssl rand -hex 32", text)
                self.assertIn("chmod 0600 .env", text)
                self.assertIn("chmod 0711 .", text)
                self.assertIn("docker compose ps -a", text)

    def test_lifecycle_cleanup_never_removes_foreign_fixed_name_containers(self):
        lifecycle = (ROOT / "tests" / "beginner_compose_lifecycle_test.sh").read_text()
        conflict_guard = ROOT / "tests" / "beginner_compose_conflict_guard_test.sh"
        self.assertNotIn("docker rm -f portloom-server portloom-sshd", lifecycle)
        self.assertIn('com.docker.compose.project', lifecycle)
        self.assertIn('refusing to run: container name $name is already in use', lifecycle)
        self.assertTrue(conflict_guard.exists())

    def test_current_install_and_upgrade_commands_use_current_patch_version(self):
        current_pages = [
            "docs/guide/quick-start.md",
            "docs/en/guide/quick-start.md",
            "docs/guide/compose-install.md",
            "docs/en/guide/compose-install.md",
            "docs/install/docker.md",
            "docs/en/install/docker.md",
            "docs/install/production.md",
            "docs/en/install/production.md",
            "docs/operations/backup-upgrade.md",
            "docs/en/operations/backup-upgrade.md",
            "docs/operations/release-checklist.md",
            "docs/en/operations/release-checklist.md",
            "docs/reference/configuration.md",
            "docs/en/reference/configuration.md",
        ]
        for relative in current_pages:
            text = (ROOT / relative).read_text()
            with self.subTest(path=relative):
                self.assertIn("0.5.0", text)
                self.assertNotIn("0.4.0", text)
                self.assertNotIn("0.4.1", text)
                self.assertNotIn("0.4.2", text)
                if not relative.endswith(("backup-upgrade.md", "release-checklist.md")):
                    self.assertNotIn("0.4.6", text)

    def test_public_copy_does_not_imply_a_required_portloom_subdomain(self):
        public_files = [ROOT / "README.md", ROOT / "README.en.md", ROOT / "web" / "index.html"]
        public_files.extend((ROOT / "docs").rglob("*.md"))
        public_files.extend((ROOT / "examples").glob("*.example"))
        public_files.append(ROOT / "docs" / "public" / "install-server.sh")

        offenders = []
        for path in public_files:
            if ".vitepress/dist" in path.as_posix():
                continue
            if "portloom.example.com" in path.read_text():
                offenders.append(path.relative_to(ROOT).as_posix())
        self.assertEqual([], offenders, f"misleading management hostname remains in: {offenders}")

    def test_compose_pages_explain_that_the_domain_is_user_selected(self):
        required_zh = ["不要求 `portloom.` 前缀", "根域名", "任意子域名"]
        required_en = ["no `portloom.` prefix", "apex domain", "any subdomain"]
        pages = [
            (ROOT / "docs" / "guide" / "compose-install.md", required_zh),
            (ROOT / "docs" / "en" / "guide" / "compose-install.md", required_en),
        ]
        for path, phrases in pages:
            text = path.read_text()
            for phrase in phrases:
                with self.subTest(path=path, phrase=phrase):
                    self.assertIn(phrase, text)


if __name__ == "__main__":
    unittest.main()

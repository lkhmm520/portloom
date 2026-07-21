"use strict";

const assert = require("node:assert/strict");
const { spawnSync } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const root = path.resolve(__dirname, "../..");
const app = fs.readFileSync(path.join(root, "web/assets/app.js"), "utf8");
const html = fs.readFileSync(path.join(root, "web/index.html"), "utf8");
const readme = fs.readFileSync(path.join(root, "README.md"), "utf8");

const desktopResourceSpacing = /\.resource-list:not\(\.empty-state\)\s*\{[^{}]*\bpadding\s*:\s*1rem\s+1\.2rem\s+1\.2rem\s*(?:;|})/;
const mobileResourceSpacing = /\.resource-list:not\(\.empty-state\)\s*\{[^{}]*\bpadding\s*:\s*\.9rem\s+\.85rem\s+1rem\s*(?:;|})/;

function sanitizeCSS(source) {
  const sanitized = source.split("");
  const blank = index => { sanitized[index] = " "; };
  const mask = index => { sanitized[index] = "\uE000"; };
  let cursor = 0;

  while (cursor < source.length) {
    if (source[cursor] === "/" && source[cursor + 1] === "*") {
      blank(cursor++);
      blank(cursor++);
      while (cursor < source.length) {
        if (source[cursor] === "*" && source[cursor + 1] === "/") {
          blank(cursor++);
          blank(cursor++);
          break;
        }
        blank(cursor++);
      }
      continue;
    }

    if (source[cursor] === '"' || source[cursor] === "'") {
      const quote = source[cursor];
      mask(cursor++);
      while (cursor < source.length) {
        const character = source[cursor];
        mask(cursor++);
        if (character === "\\" && cursor < source.length) {
          mask(cursor++);
        } else if (character === quote) {
          break;
        }
      }
      continue;
    }

    if (source[cursor] === "\\") {
      mask(cursor++);
      if (cursor >= source.length) continue;

      if (/[0-9a-f]/i.test(source[cursor])) {
        let hexLength = 0;
        while (cursor < source.length && hexLength < 6 && /[0-9a-f]/i.test(source[cursor])) {
          mask(cursor++);
          hexLength += 1;
        }
        if (/\s/.test(source[cursor] || "")) {
          const whitespace = source[cursor];
          mask(cursor++);
          if (whitespace === "\r" && source[cursor] === "\n") mask(cursor++);
        }
      } else {
        const escaped = source[cursor];
        mask(cursor++);
        if (escaped === "\r" && source[cursor] === "\n") mask(cursor++);
      }
      continue;
    }

    cursor += 1;
  }

  return sanitized.join("");
}

function findMobileMediaBlocks(sanitized) {
  const blocks = [];
  const mediaStart = /@media\s*\(\s*max-width\s*:\s*760px\s*\)\s*\{/gi;
  let match;

  while ((match = mediaStart.exec(sanitized)) !== null) {
    let cursor = mediaStart.lastIndex;
    let depth = 1;
    while (cursor < sanitized.length && depth > 0) {
      if (sanitized[cursor] === "{") depth += 1;
      if (sanitized[cursor] === "}") depth -= 1;
      cursor += 1;
    }
    assert.equal(depth, 0, "max-width: 760px media block has balanced braces");
    blocks.push(sanitized.slice(match.index, cursor));
  }

  return blocks;
}

function hasTopLevelRule(sanitized, rulePattern) {
  const matcher = new RegExp(rulePattern.source, "g");
  let match;
  while ((match = matcher.exec(sanitized)) !== null) {
    let depth = 0;
    for (let cursor = 0; cursor < match.index; cursor += 1) {
      if (sanitized[cursor] === "{") depth += 1;
      if (sanitized[cursor] === "}") depth -= 1;
    }
    if (depth === 0) return true;
  }
  return false;
}

function analyzeResourceSpacing(source) {
  const sanitized = sanitizeCSS(source);
  const mediaBlocks = findMobileMediaBlocks(sanitized);
  return {
    sanitized,
    mediaBlocks,
    desktop: hasTopLevelRule(sanitized, desktopResourceSpacing),
    mobile: mediaBlocks.some(block => mobileResourceSpacing.test(block))
  };
}

test("add-agent UI keeps token API narrow and generates a shell-safe version-pinned install command", () => {
  const tokenForm = html.match(/<form id="token-form"[\s\S]*?<\/form>/)?.[0] || "";
  assert.ok(tokenForm, "add-agent form exists");
  for (const name of ["name", "server_url", "ssh_host", "expires_in"]) assert.match(tokenForm, new RegExp(`name="${name}"`));

  const createToken = app.match(/async function createToken\(event\)[\s\S]*?\n  }/)?.[0] || "";
  assert.ok(createToken, "createToken function exists");
  assert.match(createToken, /expires_in:/);
  assert.doesNotMatch(createToken, /body:\s*JSON\.stringify\(\{[^}]*name:/);

  const quoteSource = app.match(/function shellQuote\(value\)[\s\S]*?\n  }/)?.[0] || "";
  const versionSource = app.match(/function isSafeImageTag\(value\)[\s\S]*?\n  }/)?.[0] || "";
  const commandSource = app.match(/function buildAgentInstallCommand\(options\)[\s\S]*?\n  }/)?.[0] || "";
  assert.ok(quoteSource && versionSource && commandSource, "command helpers exist");
  const { buildAgentInstallCommand } = Function(`"use strict"; ${quoteSource}; ${versionSource}; ${commandSource}; return { buildAgentInstallCommand };`)();
  assert.match(createToken, /version:\s*state\.system\.version/);

  const temp = fs.mkdtempSync(path.join(os.tmpdir(), "portloom-web-command-"));
  const bin = path.join(temp, "bin");
  const argsFile = path.join(temp, "args.txt");
  const injectedFile = path.join(temp, "injected");
  const fakeInstaller = path.join(temp, "fake-installer.sh");
  fs.mkdirSync(bin);
  fs.writeFileSync(fakeInstaller, '#!/bin/sh\nprintf "%s\\n" "$@" > "$ARGS_OUT"\n');
  fs.writeFileSync(path.join(bin, "curl"), '#!/bin/sh\ncp "$FAKE_INSTALLER" "$2"\n', { mode: 0o755 });
  const command = buildAgentInstallCommand({
    serverURL: "https://loom.example.com/a path", name: `nas'; touch '${injectedFile}'; echo '`, token: "one-time-token",
    sshHost: "vps.example.com", sshPort: 2222, sshHostKey: "ssh-ed25519 AAAAhostkey", version: "1.2.3"
  });
  assert.match(command, /install-agent\.sh/);
  assert.match(command, /--server-url 'https:\/\/loom\.example\.com\/a path'/);
  assert.match(command, /--token 'one-time-token'/);
  assert.match(command, /--version '1\.2\.3'/);
  assert.match(command, /'"'"'/, "embedded quote is escaped");
  assert.doesNotMatch(command, /--name nas';/, "name is never emitted unquoted");
  const executed = spawnSync("/bin/sh", ["-c", command], {
    cwd: temp, encoding: "utf8", env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, ARGS_OUT: argsFile, FAKE_INSTALLER: fakeInstaller }
  });
  assert.equal(executed.status, 0, executed.stderr);
  assert.equal(fs.existsSync(injectedFile), false, "quoted Agent name cannot execute shell commands");
  assert.deepEqual(fs.readFileSync(argsFile, "utf8").trim().split("\n"), [
    "--server-url", "https://loom.example.com/a path", "--name", `nas'; touch '${injectedFile}'; echo '`,
    "--token", "one-time-token", "--ssh-host", "vps.example.com", "--ssh-port", "2222",
    "--ssh-host-key", "ssh-ed25519 AAAAhostkey", "--version", "1.2.3"
  ]);

  for (const version of ["v1.2.3; touch /tmp/pwned", "../../latest", "tag$(id)", "", "a".repeat(129)]) {
    const fallback = buildAgentInstallCommand({
      serverURL: "https://loom.example.com", name: "nas", token: "token", sshHost: "vps.example.com",
      sshPort: 2222, sshHostKey: "ssh-ed25519 AAAAhostkey", version
    });
    assert.doesNotMatch(fallback, /--version/, `unsafe version falls back to installer default: ${version}`);
  }
});

test("route creation exposes HTTP and TCP with an explicit public port", () => {
  const routeForm = html.match(/<form id="route-form"[\s\S]*?<\/form>/)?.[0] || "";
  assert.ok(routeForm, "route form exists");
  assert.match(routeForm, /value="http"/i);
  assert.match(routeForm, /value="tcp"/i);
  assert.match(routeForm, /name="public_port"/i);
  assert.match(html, /TCP routes publish an explicit VPS port/i);

  const payload = app.match(/function routePayload\(form\)[\s\S]*?\n  }/)?.[0] || "";
  assert.match(payload, /form\.elements\.protocol\.value/);
  assert.match(payload, /protocol\s*===\s*"tcp"/);
  assert.match(payload, /Number\(data\.get\("public_port"\)\)/);
  assert.match(app, /async function monitorRoutePublication/);
});

test("enrollment token list identifies rows by safe token ID", () => {
  assert.match(html, /<th[^>]*>ID<\/th><th[^>]*>Status<\/th>/);
  const renderTokens = app.match(/function renderTokens\(\)[\s\S]*?\n  }/)?.[0] || "";
  assert.ok(renderTokens, "renderTokens function exists");
  assert.doesNotMatch(renderTokens, /token\.name/);
  assert.match(renderTokens, /token\.id/);
  assert.doesNotMatch(renderTokens, /"Enrollment token"/);
  assert.match(renderTokens, /tokens\.defaultID/);
});

test("route edit locks client ownership and keeps a neutral success message", () => {
  const openDialog = app.match(/function openRouteDialog\(route\)[\s\S]*?\n  }/)?.[0] || "";
  assert.ok(openDialog, "openRouteDialog function exists");
  assert.match(openDialog, /form\.elements\.client_id\.disabled\s*=\s*Boolean\(route\)/);

  const payload = app.match(/function routePayload\(form\)[\s\S]*?\n  }/)?.[0] || "";
  assert.match(payload, /client_id:\s*String\(form\.elements\.client_id\.value\)/);
  assert.match(app, /showNotice\(t\("routes\.waitingForPublish"\)/);
  assert.doesNotMatch(app, /client (?:changed|updated|reassigned)/i);
});

test("public route status mirrors the gateway convergence gate at runtime", () => {
  const publicSource = app.match(/function routePublicStatus\(route\)[\s\S]*?\n  }/)?.[0] || "";
  const healthySource = app.match(/function routeHealthy\(route\)[\s\S]*?\n  }/)?.[0] || "";
  assert.ok(publicSource, "routePublicStatus function exists");
  assert.ok(healthySource, "routeHealthy function exists");
  const hooks = Function(`"use strict"; ${publicSource}; ${healthySource}; return { routePublicStatus, routeHealthy };`)();
  const base = { enabled: true, local_status: "up", tunnel_status: "up", observed_revision: 3, desired_revision: 3 };
  assert.equal(hooks.routePublicStatus(base), "published");
  assert.equal(hooks.routeHealthy(base), true);
  const tcpWaiting = { ...base, protocol: "tcp", public_status: "waiting_agent" };
  assert.equal(hooks.routePublicStatus(tcpWaiting), "waiting_agent");
  assert.equal(hooks.routeHealthy(tcpWaiting), false);
  const tcpPublished = { ...tcpWaiting, public_status: "published" };
  assert.equal(hooks.routePublicStatus(tcpPublished), "published");
  assert.equal(hooks.routeHealthy(tcpPublished), true);
  assert.equal(hooks.routePublicStatus({ ...tcpPublished, enabled: false }), "disabled");
  for (const tunnel_status of ["UP", " up ", "connected", "healthy", "active", "ready", "down"]) {
    const route = { ...base, tunnel_status };
    assert.equal(hooks.routePublicStatus(route), "waiting_agent", tunnel_status);
    assert.equal(hooks.routeHealthy(route), false, tunnel_status);
  }
  assert.equal(hooks.routePublicStatus({ ...base, observed_revision: 2 }), "waiting_agent");
  assert.equal(hooks.routeHealthy({ ...base, observed_revision: 2 }), false);
  assert.equal(hooks.routePublicStatus({ ...base, enabled: false }), "disabled");
  assert.equal(hooks.routeHealthy({ ...base, local_status: "down" }), false);
});

test("401 and 403 responses perform a visible logout", async () => {
  const requestSource = app.match(/async function request\(path, options = \{\}\)[\s\S]*?\n  }/)?.[0] || "";
  const clearSource = app.match(/function clearSensitiveUI\(\)[\s\S]*?\n  }/)?.[0] || "";
  const logoutSource = app.match(/function logout\(\)[\s\S]*?\n  }/)?.[0] || "";
  assert.ok(requestSource, "request function exists");
  assert.ok(clearSource, "clearSensitiveUI function exists");
  assert.ok(logoutSource, "logout function exists");

  for (const status of [401, 403]) {
    const calls = [];
    const request = Function("API", "state", "authAttempt", "Headers", "fetch", "logout", "t", "APIError", `"use strict"; ${requestSource}; return request;`)(
      "/api/v1", { token: "secret" }, 0, Headers, async () => ({ status }), (...args) => calls.push(args), key => key, class APIError extends Error { constructor(message, code) { super(message); this.status = code; } }
    );
    await assert.rejects(request("/routes"), error => error.status === status);
    assert.deepEqual(calls, [[]], `logout called with default visible-login behavior for ${status}`);
  }

  const events = [];
  const dialogs = [{ close() { events.push("close-route"); } }, { close() { events.push("close-token"); } }];
  const nodes = {
    "#app": { hidden: false },
    "#login-screen": { hidden: true },
    "#admin-token": { value: "admin-secret", type: "text", focus() { this.focused = true; events.push("focus"); } },
    "#toggle-token": { textContent: "Hide", setAttribute(name, value) { this[name] = value; } },
    "#created-token": { textContent: "one-time-enrollment-secret" },
    "#copy-token-button": { textContent: "Copied" }
  };
  const state = { token: "secret" };
  let removed = "";
  const logout = Function("state", "sessionStorage", "TOKEN_KEY", "$", "$$", "requestAnimationFrame", "t", `"use strict"; let authAttempt = 0; let loginController = null; ${clearSource}; ${logoutSource}; return logout;`)(
    state,
    { removeItem(key) { removed = key; } },
    "token-key",
    selector => nodes[selector],
    selector => selector === "dialog[open]" ? dialogs : [],
    callback => callback(),
    key => ({ "common.show": "Show", "common.copyCommand": "Copy command" })[key] || key
  );
  logout();
  assert.equal(state.token, "");
  assert.equal(removed, "token-key");
  assert.equal(nodes["#app"].hidden, true);
  assert.equal(nodes["#login-screen"].hidden, false);
  assert.equal(nodes["#admin-token"].value, "");
  assert.equal(nodes["#admin-token"].type, "password");
  assert.equal(nodes["#toggle-token"].textContent, "Show");
  assert.equal(nodes["#created-token"].textContent, "");
  assert.equal(nodes["#copy-token-button"].textContent, "Copy command");
  assert.equal(nodes["#admin-token"].focused, true);
  assert.deepEqual(events, ["close-route", "close-token", "focus"]);
});

test("login and one-time secret lifecycle clear sensitive DOM at runtime", async () => {
  const clearSource = app.match(/function clearSensitiveUI\(\)[\s\S]*?\n  }/)?.[0] || "";
  const loginSource = app.match(/async function login\(token\)[\s\S]*?\n  }/)?.[0] || "";
  const logoutSource = app.match(/function logout\(\)[\s\S]*?\n  }/)?.[0] || "";
  const clearCreatedSource = app.match(/function clearCreatedToken\(\)[\s\S]*?\n  }/)?.[0] || "";
  const bindSecretSource = app.match(/function bindSecretDialogCleanup\(\)[\s\S]*?\n  }/)?.[0] || "";
  const copySource = app.match(/async function copyCreatedToken\(\)[\s\S]*?\n  }/)?.[0] || "";
  for (const [name, source] of Object.entries({ clearSource, loginSource, logoutSource, clearCreatedSource, bindSecretSource, copySource })) assert.ok(source, `${name} exists`);

  const makeNodes = () => ({
    "#app": { hidden: true }, "#login-screen": { hidden: false }, "#login-error": { hidden: false, textContent: "old" },
    "#admin-token": { value: "admin-secret", type: "text", focus() {} }, "#toggle-token": { textContent: "Hide", setAttribute(name, value) { this[name] = value; } },
    "#created-token": { textContent: "one-time-secret" }, "#copy-token-button": { textContent: "Copied" }
  });
  const translate = key => ({ "common.show": "Show", "common.copyCommand": "Copy command", "common.copied": "Copied" })[key] || key;
  const buildLogin = (state, sessionStorage, loadAll, nodes) => Function("state", "sessionStorage", "TOKEN_KEY", "loadAll", "$", "t", `"use strict"; let authAttempt = 0; let loginController = null; ${clearSource}; ${loginSource}; return login;`)(state, sessionStorage, "token-key", loadAll, selector => nodes[selector], translate);

  {
    const nodes = makeNodes(); const state = { token: "" }; const saved = {};
    const login = buildLogin(state, { setItem(k, v) { saved[k] = v; }, removeItem(k) { delete saved[k]; } }, async () => {}, nodes);
    await login("  valid-admin-token  ");
    assert.equal(state.token, "valid-admin-token"); assert.equal(saved["token-key"], "valid-admin-token");
    assert.equal(nodes["#admin-token"].value, ""); assert.equal(nodes["#admin-token"].type, "password"); assert.equal(nodes["#toggle-token"].textContent, "Show");
    assert.equal(nodes["#created-token"].textContent, ""); assert.equal(nodes["#copy-token-button"].textContent, "Copy command");
    assert.equal(nodes["#login-screen"].hidden, true); assert.equal(nodes["#app"].hidden, false); assert.equal(nodes["#login-error"].hidden, true);
  }
  {
    const nodes = makeNodes(); nodes["#app"].hidden = false; nodes["#login-screen"].hidden = true;
    const state = { token: "" }; const saved = {};
    const login = buildLogin(state, { setItem(k, v) { saved[k] = v; }, removeItem(k) { delete saved[k]; } }, async () => { throw new Error("rejected"); }, nodes);
    await login("rejected-admin-token");
    assert.equal(state.token, ""); assert.equal(saved["token-key"], undefined);
    assert.equal(nodes["#admin-token"].value, ""); assert.equal(nodes["#admin-token"].type, "password"); assert.equal(nodes["#toggle-token"].textContent, "Show");
    assert.equal(nodes["#login-screen"].hidden, false); assert.equal(nodes["#app"].hidden, true);
    assert.equal(nodes["#login-error"].textContent, "rejected"); assert.equal(nodes["#login-error"].hidden, false);
  }

  const dialog = new EventTarget(); dialog.open = true;
  dialog.close = function () { this.open = false; this.dispatchEvent(new Event("close")); };
  const secretNodes = { ...makeNodes(), "#secret-dialog": dialog, "#created-token": { textContent: "one-time-secret" }, "#copy-token-button": { textContent: "Copied" } };
  const lifecycle = Function("$", "t", `"use strict"; ${clearCreatedSource}; ${bindSecretSource}; return { clearCreatedToken, bindSecretDialogCleanup };`)(selector => secretNodes[selector], translate);
  lifecycle.bindSecretDialogCleanup();
  dialog.dispatchEvent(new Event("close"));
  assert.equal(secretNodes["#created-token"].textContent, ""); assert.equal(secretNodes["#copy-token-button"].textContent, "Copy command");

  let resolveCopy;
  const pendingCopy = new Promise(resolve => { resolveCopy = resolve; });
  dialog.open = true; secretNodes["#created-token"].textContent = "race-secret"; secretNodes["#copy-token-button"].textContent = "Copy";
  const copyCreatedToken = Function("$", "navigator", "showNotice", "t", `"use strict"; ${copySource}; return copyCreatedToken;`)(selector => secretNodes[selector], { clipboard: { writeText: () => pendingCopy } }, () => {}, translate);
  const authState = { token: "active-admin-token" }; let removed = false;
  const logout = Function("state", "sessionStorage", "TOKEN_KEY", "$", "$$", "requestAnimationFrame", "t", `"use strict"; let authAttempt = 0; let loginController = null; ${clearSource}; ${logoutSource}; return logout;`)(
    authState, { removeItem() { removed = true; } }, "token-key", selector => secretNodes[selector], selector => selector === "dialog[open]" && dialog.open ? [dialog] : [], callback => callback(), translate
  );
  const copying = copyCreatedToken();
  logout(); resolveCopy(); await copying;
  assert.equal(authState.token, ""); assert.equal(removed, true); assert.equal(dialog.open, false);
  assert.equal(secretNodes["#created-token"].textContent, ""); assert.equal(secretNodes["#copy-token-button"].textContent, "Copy command");
});

test("newer login and logout invalidate older pending login attempts", async () => {
  const clearSource = app.match(/function clearSensitiveUI\(\)[\s\S]*?\n  }/)?.[0] || "";
  const loginSource = app.match(/async function login\(token\)[\s\S]*?\n  }/)?.[0] || "";
  const logoutSource = app.match(/function logout\(\)[\s\S]*?\n  }/)?.[0] || "";
  const nodes = {
    "#app": { hidden: true }, "#login-screen": { hidden: false }, "#login-error": { hidden: true, textContent: "" },
    "#admin-token": { value: "", type: "password", focus() {} }, "#toggle-token": { textContent: "Show", setAttribute(name, value) { this[name] = value; } },
    "#created-token": { textContent: "" }, "#copy-token-button": { textContent: "Copy command" }
  };
  const state = { token: "" }; const saved = {}; const loads = [];
  const loadAll = options => new Promise((resolve, reject) => loads.push({ resolve, reject, options }));
  const runtime = Function("state", "sessionStorage", "TOKEN_KEY", "loadAll", "$", "$$", "requestAnimationFrame", "t", `"use strict"; let authAttempt = 0; let loginController = null; ${clearSource}; ${logoutSource}; ${loginSource}; return { login, logout };`)(
    state, { setItem(k, v) { saved[k] = v; }, removeItem(k) { delete saved[k]; } }, "token-key", loadAll,
    selector => nodes[selector], () => [], callback => callback(), key => ({ "common.show": "Show", "common.copyCommand": "Copy command" })[key] || key
  );

  const oldLogin = runtime.login("old-token");
  const newLogin = runtime.login("new-token");
  assert.equal(loads.length, 2);
  loads[1].reject(new Error("new rejected")); await newLogin;
  loads[0].resolve(); await oldLogin;
  assert.equal(state.token, ""); assert.equal(saved["token-key"], undefined);
  assert.equal(nodes["#login-screen"].hidden, false); assert.equal(nodes["#app"].hidden, true);
  assert.equal(nodes["#login-error"].textContent, "new rejected");

  const pendingLogin = runtime.login("pending-token");
  assert.equal(loads.length, 3);
  runtime.logout();
  loads[2].resolve(); await pendingLogin;
  assert.equal(state.token, ""); assert.equal(saved["token-key"], undefined);
  assert.equal(nodes["#login-screen"].hidden, false); assert.equal(nodes["#app"].hidden, true);
});

test("README local server example uses absolute paths", () => {
  assert.match(readme, /TM_DATABASE_PATH="\$\(pwd\)\/data\/portloom\.db"/);
  assert.match(readme, /TM_WEB_DIR="\$\(pwd\)\/web"/);
  assert.doesNotMatch(readme, /TM_DATABASE_PATH=\.\/|TM_WEB_DIR=\.\//);
  assert.match(readme, /HTTP Authorization header using the Bearer scheme/);
});

test("console defaults to complete Simplified Chinese and can persistently switch to English", () => {
  assert.match(html, /<html lang="zh-CN">/);
  assert.match(html, /<script defer src="\/assets\/i18n\.js\?v=[^"]+"><\/script>[\s\S]*<script defer src="\/assets\/app\.js\?v=[^"]+"><\/script>/);
  assert.ok((html.match(/data-language-toggle/g) || []).length >= 2, "language switch is available before and after login");

  const i18nPath = path.join(root, "web/assets/i18n.js");
  assert.ok(fs.existsSync(i18nPath), "browser i18n module exists");
  const i18n = require(i18nPath);
  assert.equal(i18n.normalizeLocale(), "zh-CN");
  assert.equal(i18n.normalizeLocale("zh-TW"), "zh-CN");
  assert.equal(i18n.normalizeLocale("en-US"), "en");
  assert.equal(i18n.translate("zh-CN", "nav.dashboard"), "仪表盘");
  assert.equal(i18n.translate("en", "nav.dashboard"), "Dashboard");
  assert.deepEqual(Object.keys(i18n.messages["zh-CN"]).sort(), Object.keys(i18n.messages.en).sort(), "Chinese and English dictionaries expose the same keys");

  const versionedAssets = [...html.matchAll(/(?:href|src)="\/assets\/(?:app\.css|i18n\.js|app\.js)\?v=([^"]+)"/g)].map(match => match[1]);
  assert.equal(versionedAssets.length, 3, "all custom web assets use cache-busting versions");
  assert.equal(new Set(versionedAssets).size, 1, "custom web assets share one release version");

  const keys = [...html.matchAll(/data-i18n(?:-(?:placeholder|aria-label|title|content))?="([^"]+)"/g)].map(match => match[1]);
  assert.ok(keys.includes("meta.description"), "content attributes are included in the static translation key audit");
  assert.ok(keys.length > 50, "all visible static copy is marked for translation");
  for (const locale of ["zh-CN", "en"]) {
    for (const key of keys) assert.ok(Object.hasOwn(i18n.messages[locale], key), `${locale} is missing ${key}`);
  }
  const dynamicKeys = [...app.matchAll(/["']((?:common|language|meta|login|brand|nav|api|top|updated|page|context|metrics|dashboard|clients|tokens|routes|table|dialog|form|agent|confirm|status|error)\.[A-Za-z][A-Za-z0-9_.-]*)["']/g)].map(match => match[1]);
  assert.ok(dynamicKeys.includes("context.addRouteShort"), "configured runtime keys are included in the dynamic translation audit");
  for (const locale of ["zh-CN", "en"]) {
    for (const key of dynamicKeys) assert.ok(Object.hasOwn(i18n.messages[locale], key), `${locale} is missing runtime key ${key}`);
  }

  assert.match(app, /const LANGUAGE_KEY = "portloom\.language"/);
  assert.match(app, /localStorage\.getItem\(LANGUAGE_KEY\)/);
  assert.match(app, /localStorage\.setItem\(LANGUAGE_KEY, state\.locale\)/);
  assert.match(app, /document\.documentElement\.lang = state\.locale/);
});

test("client count is grammatical in English", () => {
  const i18n = require("./i18n.js");
  const source = app.match(/function clientCountLabel\(count\)[\s\S]*?\n  }/)?.[0] || "";
  assert.ok(source, "clientCountLabel function exists");
  const label = Function("t", `"use strict"; ${source}; return clientCountLabel;`)((key, params) => i18n.translate("en", key, params));
  assert.equal(label(1), "1 client");
  assert.equal(label(2), "2 clients");
});

test("TCP automatic port exposure is localized", () => {
  const i18n = require("./i18n.js");
  const source = app.match(/function routeExposure\(route\)[\s\S]*?\n  }/)?.[0] || "";
  assert.ok(source, "routeExposure function exists");
  const build = locale => Function("t", `"use strict"; ${source}; return routeExposure;`)((key, params) => i18n.translate(locale, key, params));
  assert.equal(build("zh-CN")({ protocol: "tcp", public_port: 0 }), "TCP：自动分配");
  assert.equal(build("en")({ protocol: "tcp", public_port: 0 }), "TCP: auto");
});

test("narrow sticky headers keep one accessible name and update both visual labels", () => {
  const css = fs.readFileSync(path.join(root, "web/assets/app.css"), "utf8");
  const source = app.match(/function updateContextAction\(\)[\s\S]*?\n  }/)?.[0] || "";
  assert.ok(source, "updateContextAction function exists");
  const full = { textContent: "" };
  const short = { textContent: "" };
  const attributes = {};
  const button = {
    hidden: true,
    dataset: {},
    querySelector: selector => selector === ".context-action-full" ? full : short,
    setAttribute: (name, value) => { attributes[name] = value; }
  };
  let locale = "en";
  const translations = {
    en: { "context.addRoute": "Add HTTP route", "context.addRouteShort": "Add route" },
    "zh-CN": { "context.addRoute": "添加 HTTP 路由", "context.addRouteShort": "添加路由" }
  };
  const update = Function("state", "$", "t", `"use strict"; ${source}; return updateContextAction;`)(
    { view: "routes" },
    () => button,
    key => translations[locale][key] || key
  );

  update();
  assert.equal(full.textContent, "Add HTTP route");
  assert.equal(short.textContent, "Add route");
  assert.equal(attributes["aria-label"], "Add HTTP route");
  locale = "zh-CN";
  update();
  assert.equal(full.textContent, "添加 HTTP 路由");
  assert.equal(short.textContent, "添加路由");
  assert.equal(attributes["aria-label"], "添加 HTTP 路由");

  assert.match(html, /id="context-action-button"[\s\S]*context-action-full[^>]*aria-hidden="true"[\s\S]*context-action-short[^>]*aria-hidden="true"/);
  assert.doesNotMatch(css, /#context-action-button::after/);
  assert.match(css, /@media \(max-width: 520px\)[\s\S]*\.topbar-title\s*\{[^}]*flex-shrink:\s*0/);
  assert.match(css, /@media \(max-width: 520px\)[\s\S]*\.topbar \.language-toggle\s*\{[^}]*min-width:\s*0/);
});

test("refresh loading state preserves compact accessible label nodes", () => {
  const source = app.match(/function setLoading\(loading\)[\s\S]*?\n  }/)?.[0] || "";
  assert.ok(source, "setLoading function exists");
  const full = { textContent: "" };
  const attributes = {};
  const button = {
    disabled: false,
    querySelector: () => full,
    setAttribute: (name, value) => { attributes[name] = value; }
  };
  const state = {};
  const setLoading = Function("state", "$", "t", `"use strict"; ${source}; return setLoading;`)(
    state,
    () => button,
    key => ({ "common.refresh": "Refresh", "common.refreshing": "Refreshing…" })[key] || key
  );
  setLoading(true);
  assert.equal(button.disabled, true);
  assert.equal(full.textContent, "Refreshing…");
  assert.equal(attributes["aria-label"], "Refreshing…");
  setLoading(false);
  assert.equal(full.textContent, "Refresh");
  assert.equal(attributes["aria-label"], "Refresh");
});

test("system resource status rows keep readable horizontal panel insets", () => {
  const desktopRule = ".resource-list:not(.empty-state) { padding: 1rem 1.2rem 1.2rem; }";
  const mobileRule = ".resource-list:not(.empty-state) { padding: .9rem .85rem 1rem; }";

  let result = analyzeResourceSpacing(`
    /* ${desktopRule} */
    @media (max-width: 760px) { /* ${mobileRule} */ }
  `);
  assert.equal(result.desktop, false, "commented desktop rule is ignored");
  assert.equal(result.mobile, false, "commented mobile selector and rule are ignored");

  result = analyzeResourceSpacing(`
    .desktop-decoy::before { content: "${desktopRule}"; }
    .media-decoy::after { content: '@media (max-width: 760px) { ${mobileRule} }'; }
    .escaped-quote-decoy::after { content: "still a string: \\" @media (max-width: 760px) { ${mobileRule} }"; }
  `);
  assert.equal(result.desktop, false, "selector and declaration in content are ignored");
  assert.equal(result.mobile, false, "@media and rule in content are ignored");
  assert.equal(result.mediaBlocks.length, 0, "string content does not create a media block");

  result = analyzeResourceSpacing(`/* @media (max-width: 760px) { ${mobileRule} } */`);
  assert.equal(result.mobile, false, "an entirely commented media block is ignored");
  assert.equal(result.mediaBlocks.length, 0, "commented @media is not parsed");

  result = analyzeResourceSpacing(`
    ${desktopRule}
    ${mobileRule}
    @media (max-width: 760px) { .other { padding: 0; } }
  `);
  assert.equal(result.desktop, true, "the real desktop rule remains detectable");
  assert.equal(result.mobile, false, "a mobile declaration outside the media block is rejected");

  result = analyzeResourceSpacing(`
    @media (max-width: 760px) { ${desktopRule} }
  `);
  assert.equal(result.desktop, false, "desktop padding must be a top-level rule, not mobile-only");

  result = analyzeResourceSpacing(`
    .resource-list:not(.empty-state) { padding\\78: 1rem 1.2rem 1.2rem; }
    .resource-list:not(.empty-state) { padding: 1rem "ignored" 1.2rem 1.2rem; }
    @media (max-width: 760px) {
      .resource-list:not(.empty-state) { padding\\78: .9rem .85rem 1rem; }
    }
  `);
  assert.equal(result.desktop, false, "escaped property names and string tokens cannot bridge into padding");
  assert.equal(result.mobile, false, "escaped mobile property names cannot bridge into padding");

  result = analyzeResourceSpacing(`
    @media (max-width: 760px) "invalid prelude" { ${mobileRule} }
  `);
  assert.equal(result.mobile, false, "string tokens make the media prelude invalid instead of disappearing");
  assert.equal(result.mediaBlocks.length, 0, "invalid media preludes are not parsed as target media blocks");

  const robustFixture = `
    ${desktopRule}
    @media (max-width: 760px) {
      .escaped-close { custom: escaped\\}close; }
      @supports (display: grid) { ${mobileRule} }
    }
    @media (max-width: 760px) {
      .escaped\\{open { color: green; }
      @supports (display: block) { .nested { color: green; } }
    }
  `;
  result = analyzeResourceSpacing(robustFixture);
  assert.equal(result.desktop, true);
  assert.equal(result.mobile, true, "a real mobile rule survives escaped braces and nesting");
  assert.equal(result.mediaBlocks.length, 2, "all same-name media blocks are parsed");
  assert.equal(result.sanitized.length, robustFixture.length, "sanitizing preserves source offsets");

  const css = fs.readFileSync(path.join(root, "web/assets/app.css"), "utf8");
  result = analyzeResourceSpacing(css);
  assert.equal(result.desktop, true, "desktop resource-list padding is defined in real CSS");
  assert.equal(result.mobile, true, "mobile resource-list padding is defined inside a max-width: 760px media block");
});

test("each module uses one sticky title and action row", () => {
  const css = fs.readFileSync(path.join(root, "web/assets/app.css"), "utf8");
  const topbar = html.match(/<header class="topbar">[\s\S]*?<\/header>/)?.[0] || "";
  assert.ok(topbar, "module title row exists");
  assert.match(topbar, /id="page-title"/);
  assert.match(topbar, /id="context-action-button"/);
  assert.match(topbar, /id="refresh-button"/);
  assert.match(topbar, /data-language-toggle/);
  assert.doesNotMatch(html.match(/<section id="view-tokens"[\s\S]*?<\/section>/)?.[0] || "", /id="new-token-button"/);
  assert.doesNotMatch(html.match(/<section id="view-routes"[\s\S]*?<\/section>/)?.[0] || "", /id="new-route-button"/);
  assert.doesNotMatch(html.match(/<section id="view-clients"[\s\S]*?<\/section>/)?.[0] || "", /<h2[^>]*data-i18n="clients\.title"/);
  assert.doesNotMatch(html.match(/<section id="view-tokens"[\s\S]*?<\/section>/)?.[0] || "", /<h2[^>]*data-i18n="tokens\.addAgent"/);
  assert.doesNotMatch(html.match(/<section id="view-routes"[\s\S]*?<\/section>/)?.[0] || "", /<h2[^>]*data-i18n="routes\.title"/);

  const topbarCSS = css.match(/\.topbar\s*\{[^}]+\}/)?.[0] || "";
  assert.match(topbarCSS, /position:\s*sticky/);
  assert.match(topbarCSS, /top:\s*0/);
  assert.match(topbarCSS, /z-index:\s*\d+/);
  assert.match(app, /function updateContextAction\(\)/);
  assert.match(app, /tokens:[\s\S]*context\.generateAgent/);
  assert.match(app, /routes:[\s\S]*context\.addRoute/);
});

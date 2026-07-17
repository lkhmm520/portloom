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

test("route creation exposes HTTP only and explains the built-in ingress boundary", () => {
  const routeForm = html.match(/<form id="route-form"[\s\S]*?<\/form>/)?.[0] || "";
  assert.ok(routeForm, "route form exists");
  assert.match(routeForm, /value="http"/i);
  assert.doesNotMatch(routeForm, /value="tcp"|name="public_port"/i);
  assert.match(html, /built-in public ingress fully supports HTTP\/HTTPS routes only/i);

  const payload = app.match(/function routePayload\(form\)[\s\S]*?\n  }/)?.[0] || "";
  assert.match(payload, /protocol:\s*"http"/);
  assert.match(payload, /public_port:\s*0/);
  assert.doesNotMatch(payload, /data\.get\("protocol"\)|protocol\s*===\s*"tcp"/);
});

test("enrollment token list identifies rows by safe token ID", () => {
  assert.match(html, /<th>ID<\/th><th>Status<\/th>/);
  const renderTokens = app.match(/function renderTokens\(\)[\s\S]*?\n  }/)?.[0] || "";
  assert.ok(renderTokens, "renderTokens function exists");
  assert.doesNotMatch(renderTokens, /token\.name/);
  assert.match(renderTokens, /token\.id/);
});

test("route edit locks client ownership and keeps a neutral success message", () => {
  const openDialog = app.match(/function openRouteDialog\(route\)[\s\S]*?\n  }/)?.[0] || "";
  assert.ok(openDialog, "openRouteDialog function exists");
  assert.match(openDialog, /form\.elements\.client_id\.disabled\s*=\s*Boolean\(route\)/);

  const payload = app.match(/function routePayload\(form\)[\s\S]*?\n  }/)?.[0] || "";
  assert.match(payload, /client_id:\s*String\(form\.elements\.client_id\.value\)/);
  assert.match(app, /id \? "Route updated\." : "Route created\."/);
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
  const tcp = { ...base, protocol: "tcp" };
  assert.equal(hooks.routePublicStatus(tcp), "metadata only");
  assert.equal(hooks.routePublicStatus({ ...tcp, enabled: false }), "metadata only");
  assert.equal(hooks.routeHealthy(tcp), false);
  for (const tunnel_status of ["UP", " up ", "connected", "healthy", "active", "ready", "down"]) {
    const route = { ...base, tunnel_status };
    assert.equal(hooks.routePublicStatus(route), "pending", tunnel_status);
    assert.equal(hooks.routeHealthy(route), false, tunnel_status);
  }
  assert.equal(hooks.routePublicStatus({ ...base, observed_revision: 2 }), "pending");
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
    const request = Function("API", "state", "authAttempt", "Headers", "fetch", "logout", "APIError", `"use strict"; ${requestSource}; return request;`)(
      "/api/v1", { token: "secret" }, 0, Headers, async () => ({ status }), (...args) => calls.push(args), class APIError extends Error { constructor(message, code) { super(message); this.status = code; } }
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
    "#toggle-token": { textContent: "Hide" },
    "#created-token": { textContent: "one-time-enrollment-secret" },
    "#copy-token-button": { textContent: "Copied" }
  };
  const state = { token: "secret" };
  let removed = "";
  const logout = Function("state", "sessionStorage", "TOKEN_KEY", "$", "$$", "requestAnimationFrame", `"use strict"; let authAttempt = 0; let loginController = null; ${clearSource}; ${logoutSource}; return logout;`)(
    state,
    { removeItem(key) { removed = key; } },
    "token-key",
    selector => nodes[selector],
    selector => selector === "dialog[open]" ? dialogs : [],
    callback => callback()
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
    "#admin-token": { value: "admin-secret", type: "text", focus() {} }, "#toggle-token": { textContent: "Hide" },
    "#created-token": { textContent: "one-time-secret" }, "#copy-token-button": { textContent: "Copied" }
  });
  const buildLogin = (state, sessionStorage, loadAll, nodes) => Function("state", "sessionStorage", "TOKEN_KEY", "loadAll", "$", `"use strict"; let authAttempt = 0; let loginController = null; ${clearSource}; ${loginSource}; return login;`)(state, sessionStorage, "token-key", loadAll, selector => nodes[selector]);

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
  const lifecycle = Function("$", `"use strict"; ${clearCreatedSource}; ${bindSecretSource}; return { clearCreatedToken, bindSecretDialogCleanup };`)(selector => secretNodes[selector]);
  lifecycle.bindSecretDialogCleanup();
  dialog.dispatchEvent(new Event("close"));
  assert.equal(secretNodes["#created-token"].textContent, ""); assert.equal(secretNodes["#copy-token-button"].textContent, "Copy command");

  let resolveCopy;
  const pendingCopy = new Promise(resolve => { resolveCopy = resolve; });
  dialog.open = true; secretNodes["#created-token"].textContent = "race-secret"; secretNodes["#copy-token-button"].textContent = "Copy";
  const copyCreatedToken = Function("$", "navigator", "showNotice", `"use strict"; ${copySource}; return copyCreatedToken;`)(selector => secretNodes[selector], { clipboard: { writeText: () => pendingCopy } }, () => {});
  const authState = { token: "active-admin-token" }; let removed = false;
  const logout = Function("state", "sessionStorage", "TOKEN_KEY", "$", "$$", "requestAnimationFrame", `"use strict"; let authAttempt = 0; let loginController = null; ${clearSource}; ${logoutSource}; return logout;`)(
    authState, { removeItem() { removed = true; } }, "token-key", selector => secretNodes[selector], selector => selector === "dialog[open]" && dialog.open ? [dialog] : [], callback => callback()
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
    "#admin-token": { value: "", type: "password", focus() {} }, "#toggle-token": { textContent: "Show" },
    "#created-token": { textContent: "" }, "#copy-token-button": { textContent: "Copy command" }
  };
  const state = { token: "" }; const saved = {}; const loads = [];
  const loadAll = options => new Promise((resolve, reject) => loads.push({ resolve, reject, options }));
  const runtime = Function("state", "sessionStorage", "TOKEN_KEY", "loadAll", "$", "$$", "requestAnimationFrame", `"use strict"; let authAttempt = 0; let loginController = null; ${clearSource}; ${logoutSource}; ${loginSource}; return { login, logout };`)(
    state, { setItem(k, v) { saved[k] = v; }, removeItem(k) { delete saved[k]; } }, "token-key", loadAll,
    selector => nodes[selector], () => [], callback => callback()
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

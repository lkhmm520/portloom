(() => {
  "use strict";

  const API = "/api/v1";
  const TOKEN_KEY = "portloom.admin-token";
  const state = { token: sessionStorage.getItem(TOKEN_KEY) || "", system: {}, clients: [], tokens: [], routes: [], view: "dashboard", deleteID: "" };
  let authAttempt = 0;
  let loginController = null;
  const $ = (selector, root = document) => root.querySelector(selector);
  const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];

  class APIError extends Error {
    constructor(message, status) { super(message); this.status = status; }
  }

  async function request(path, options = {}) {
    const requestToken = state.token;
    const requestAuthAttempt = authAttempt;
    const headers = new Headers(options.headers || {});
    headers.set("Accept", "application/json");
    if (requestToken) headers.set("Authorization", `Bearer ${requestToken}`);
    if (options.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    let response;
    try { response = await fetch(`${API}${path}`, { ...options, headers }); }
    catch (_) { throw new APIError("Cannot reach the PortLoom API.", 0); }
    if (response.status === 401 || response.status === 403) {
      if (requestToken && state.token === requestToken && authAttempt === requestAuthAttempt) logout();
      throw new APIError("The administrator token was rejected.", response.status);
    }
    const text = await response.text();
    let payload = null;
    if (text) {
      try { payload = JSON.parse(text); }
      catch (_) { payload = text; }
    }
    if (!response.ok) {
      const message = payload?.error?.message || payload?.error || payload?.message || `${response.status} ${response.statusText}`;
      throw new APIError(String(message), response.status);
    }
    return payload?.data ?? payload;
  }

  function asList(payload, key) {
    if (Array.isArray(payload)) return payload;
    if (Array.isArray(payload?.[key])) return payload[key];
    return [];
  }

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = String(text);
    return node;
  }

  function addCell(row, title, detail) {
    const td = el("td");
    td.append(el("span", "cell-title", title));
    if (detail) td.append(el("span", "cell-detail", detail));
    row.append(td);
    return td;
  }

  function normalizedStatus(value, fallback = "unknown") { return String(value || fallback).trim().toLowerCase(); }
  function tone(value) {
    const status = normalizedStatus(value);
    if (["ok", "up", "online", "healthy", "connected", "active", "ready", "published", "converged", "used"].includes(status)) return "good";
    if (["error", "failed", "down", "offline", "unhealthy", "disconnected", "expired", "revoked"].includes(status)) return "bad";
    return "warn";
  }
  function badge(value, fallback) {
    const status = normalizedStatus(value, fallback);
    return el("span", `badge ${tone(status)}`, status.replaceAll("_", " "));
  }
  function statusLayer(label, value) {
    const status = normalizedStatus(value);
    const row = el("div", `status-layer ${tone(status)}`);
    row.append(el("b", "", label), el("i"), el("span", "", status.replaceAll("_", " ")));
    return row;
  }
  function routePublicStatus(route) {
    const revisionCurrent = Number(route.observed_revision || 0) >= Number(route.desired_revision || 0);
    if (String(route.protocol || "http").toLowerCase() !== "http") return "metadata only";
    if (!route.enabled) return "disabled";
    return revisionCurrent && route.tunnel_status === "up" ? "published" : "pending";
  }
  function routeStatus(route) {
    const stack = el("div", "status-stack");
    stack.append(
      statusLayer("Local", route.local_status || "unknown"),
      statusLayer("Tunnel", route.tunnel_status || "unknown"),
      statusLayer("Public", routePublicStatus(route))
    );
    return stack;
  }
  function routeHealthy(route) {
    const local = route.local_status === "up";
    const tunnel = route.tunnel_status === "up";
    const revisions = Number(route.observed_revision || 0) >= Number(route.desired_revision || 0);
    const supported = String(route.protocol || "http").toLowerCase() === "http";
    return Boolean(supported && route.enabled && local && tunnel && revisions);
  }

  function formatDate(value) {
    if (!value) return "—";
    const date = new Date(value);
    return Number.isNaN(date.valueOf()) ? String(value) : new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date);
  }
  function relativeDate(value) {
    if (!value) return "Never";
    const time = new Date(value).valueOf();
    if (Number.isNaN(time)) return String(value);
    const delta = Math.round((time - Date.now()) / 1000);
    const abs = Math.abs(delta);
    const [unit, divisor] = abs < 60 ? ["second", 1] : abs < 3600 ? ["minute", 60] : abs < 86400 ? ["hour", 3600] : ["day", 86400];
    return new Intl.RelativeTimeFormat(undefined, { numeric: "auto" }).format(Math.round(delta / divisor), unit);
  }
  function clientOnline(client) {
    const explicit = normalizedStatus(client.status || client.connection_status, "");
    if (explicit) return tone(explicit) === "good";
    if (!client.last_seen_at && !client.last_seen) return false;
    return Date.now() - new Date(client.last_seen_at || client.last_seen).valueOf() < 90_000;
  }
  function clientName(client) { return client.name || client.hostname || client.id || "Unnamed client"; }

  function showNotice(message, kind = "error") {
    const notice = $("#notice");
    notice.textContent = message;
    notice.className = `notice ${kind}`;
    notice.hidden = false;
    clearTimeout(showNotice.timer);
    showNotice.timer = setTimeout(() => { notice.hidden = true; }, 6000);
  }
  function setAPIState(online) {
    const indicator = $("#api-indicator");
    indicator.className = `connection ${online ? "online" : "offline"}`;
    indicator.lastChild.textContent = online ? " API connected" : " API unavailable";
  }
  function setLoading(loading) {
    const button = $("#refresh-button");
    button.disabled = loading;
    button.textContent = loading ? "Refreshing…" : "Refresh";
  }

  async function loadAll({ quiet = false, signal } = {}) {
    setLoading(true);
    try {
      const [systemPayload, clientsPayload, tokensPayload, routesPayload] = await Promise.all([
        request("/system", { signal }), request("/clients", { signal }), request("/enrollment-tokens", { signal }), request("/routes", { signal })
      ]);
      if (signal?.aborted) throw new APIError("Request cancelled.", 0);
      state.system = systemPayload || {};
      state.clients = asList(clientsPayload, "clients");
      state.tokens = asList(tokensPayload, "tokens");
      state.routes = asList(routesPayload, "routes");
      renderAll();
      setAPIState(true);
      $("#last-updated").textContent = `Updated ${new Intl.DateTimeFormat(undefined, { timeStyle: "medium" }).format(new Date())}`;
    } catch (error) {
      if (!signal?.aborted) {
        setAPIState(false);
        if (!quiet && error.status !== 401 && error.status !== 403) showNotice(error.message);
      }
      throw error;
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }

  function renderAll() {
    renderDashboard(); renderClients(); renderTokens(); renderRoutes(); populateClientSelect();
  }

  function renderDashboard() {
    const online = state.clients.filter(clientOnline).length;
    const enabled = state.routes.filter(route => route.enabled && String(route.protocol || "http").toLowerCase() === "http").length;
    const healthy = state.routes.filter(routeHealthy).length;
    const drift = state.routes.filter(route => Number(route.observed_revision || 0) < Number(route.desired_revision || 0)).length;
    $("#metric-clients").textContent = String(online);
    $("#metric-clients-detail").textContent = `${state.clients.length} enrolled total`;
    $("#metric-routes").textContent = String(enabled);
    $("#metric-routes-detail").textContent = `${state.routes.length} configured total`;
    $("#metric-tunnels").textContent = String(healthy);
    $("#metric-tunnels-detail").textContent = enabled ? `${Math.round(healthy / enabled * 100)}% of enabled routes` : "No enabled routes";
    $("#metric-drift").textContent = String(drift);
    $("#metric-drift-detail").textContent = drift ? "Awaiting agent convergence" : "Desired and observed match";

    const container = $("#dashboard-routes");
    container.replaceChildren();
    const routes = [...state.routes].sort((a, b) => Number(routeHealthy(a)) - Number(routeHealthy(b))).slice(0, 6);
    if (!routes.length) { container.className = "route-health-list empty-state"; container.textContent = "No routes configured."; return; }
    container.className = "route-health-list";
    routes.forEach(route => {
      const item = el("div", "route-health-item");
      const name = el("div"); name.append(el("span", "cell-title", route.name), el("span", "cell-detail", route.domain || `${route.local_host}:${route.local_port}`));
      item.append(name, routeStatus(route), badge(routeHealthy(route) ? "healthy" : route.enabled ? "attention" : "disabled"));
      container.append(item);
    });
  }

  function renderClients() {
    const body = $("#clients-body"); body.replaceChildren();
    $("#clients-empty").hidden = state.clients.length > 0;
    $("#client-count").textContent = `${state.clients.length} client${state.clients.length === 1 ? "" : "s"}`;
    state.clients.forEach(client => {
      const row = el("tr");
      addCell(row, clientName(client), client.id);
      const statusCell = el("td"); statusCell.append(badge(clientOnline(client) ? "online" : "offline")); row.append(statusCell);
      addCell(row, `${client.observed_revision || 0} / ${client.desired_revision || client.revision || 0}`, "observed / desired");
      addCell(row, client.version || "—", client.platform || client.os || "");
      addCell(row, relativeDate(client.last_seen_at || client.last_seen), formatDate(client.last_seen_at || client.last_seen));
      body.append(row);
    });
  }

  function tokenStatus(token) {
    if (token.status) return token.status;
    if (token.used_at || token.used_by || token.client_id) return "used";
    if (token.revoked_at) return "revoked";
    if (token.expires_at && new Date(token.expires_at) < new Date()) return "expired";
    return "available";
  }
  function renderTokens() {
    const body = $("#tokens-body"); body.replaceChildren();
    $("#tokens-empty").hidden = state.tokens.length > 0;
    state.tokens.forEach(token => {
      const row = el("tr");
      addCell(row, token.id || "Enrollment token");
      const statusCell = el("td"); statusCell.append(badge(tokenStatus(token))); row.append(statusCell);
      addCell(row, formatDate(token.created_at));
      addCell(row, formatDate(token.expires_at), token.expires_at ? relativeDate(token.expires_at) : "No expiry");
      addCell(row, token.used_by || token.client_name || token.client_id || "—", token.used_at ? formatDate(token.used_at) : "");
      body.append(row);
    });
  }

  function renderRoutes() {
    const body = $("#routes-body"); body.replaceChildren();
    $("#routes-empty").hidden = state.routes.length > 0;
    state.routes.forEach(route => {
      const row = el("tr");
      addCell(row, route.name || "Unnamed route", `${route.protocol || "http"} · ${route.enabled ? "enabled" : "disabled"}`);
      addCell(row, `${route.local_host}:${route.local_port}`, state.clients.find(c => c.id === route.client_id)?.name || route.client_id);
      const exposure = route.protocol === "tcp" ? `TCP :${route.public_port || "auto"}` : route.domain || "—";
      addCell(row, exposure, route.remote_port ? `loopback :${route.remote_port}` : "port pending");
      const statusCell = el("td"); statusCell.append(routeStatus(route)); row.append(statusCell);
      addCell(row, `${route.observed_revision || 0} / ${route.desired_revision || 0}`, "observed / desired");
      const actions = el("td", "align-right"); const group = el("div", "action-group");
      const edit = el("button", "action-button", "Edit"); edit.type = "button"; edit.dataset.editRoute = route.id;
      if (String(route.protocol || "http").toLowerCase() !== "http") {
        edit.disabled = true; edit.title = "TCP metadata is read-only because the built-in public ingress supports HTTP/HTTPS only.";
      }
      const remove = el("button", "action-button delete", "Delete"); remove.type = "button"; remove.dataset.deleteRoute = route.id;
      group.append(edit, remove); actions.append(group); row.append(actions); body.append(row);
    });
  }

  function populateClientSelect() {
    const select = $("#route-client"); const selected = select.value; select.replaceChildren();
    if (!state.clients.length) { const option = el("option", "", "No enrolled clients"); option.value = ""; select.append(option); return; }
    state.clients.forEach(client => { const option = el("option", "", clientName(client)); option.value = client.id; select.append(option); });
    if (state.clients.some(client => client.id === selected)) select.value = selected;
  }

  function switchView(view) {
    state.view = view;
    $$(".nav-item").forEach(item => item.classList.toggle("active", item.dataset.view === view));
    $$(".view").forEach(section => section.classList.toggle("active", section.id === `view-${view}`));
    $("#page-title").textContent = ({ dashboard: "Dashboard", clients: "Clients", tokens: "Add Agent", routes: "Routes" })[view];
  }

  function clearSensitiveUI() {
    const adminToken = $("#admin-token");
    adminToken.value = "";
    adminToken.type = "password";
    $("#toggle-token").textContent = "Show";
    $("#created-token").textContent = "";
    $("#copy-token-button").textContent = "Copy command";
  }

  function clearCreatedToken() {
    $("#created-token").textContent = "";
    $("#copy-token-button").textContent = "Copy command";
  }

  function bindSecretDialogCleanup() {
    $("#secret-dialog").addEventListener("close", clearCreatedToken);
  }

  async function copyCreatedToken() {
    const dialog = $("#secret-dialog");
    const tokenNode = $("#created-token");
    const secret = tokenNode.textContent;
    if (!secret) return;
    try {
      await navigator.clipboard.writeText(secret);
      if (dialog.open && tokenNode.textContent === secret) $("#copy-token-button").textContent = "Copied";
    } catch (_) {
      showNotice("Clipboard access was denied. Select and copy the token manually.");
    }
  }

  function logout() {
    authAttempt += 1;
    loginController?.abort();
    loginController = null;
    $$("dialog[open]").forEach(dialog => dialog.close());
    state.token = ""; sessionStorage.removeItem(TOKEN_KEY); clearSensitiveUI(); $("#app").hidden = true;
    $("#login-screen").hidden = false;
    requestAnimationFrame(() => $("#admin-token").focus());
  }
  async function login(token) {
    const attempt = ++authAttempt;
    loginController?.abort();
    const controller = new AbortController();
    loginController = controller;
    state.token = token.trim(); sessionStorage.setItem(TOKEN_KEY, state.token);
    try {
      await loadAll({ quiet: true, signal: controller.signal });
      if (attempt !== authAttempt || controller.signal.aborted) return;
      clearSensitiveUI();
      $("#login-screen").hidden = true; $("#app").hidden = false; $("#login-error").hidden = true;
    } catch (error) {
      if (attempt !== authAttempt || controller.signal.aborted) return;
      state.token = ""; sessionStorage.removeItem(TOKEN_KEY); clearSensitiveUI();
      $("#login-screen").hidden = false; $("#app").hidden = true;
      const target = $("#login-error"); target.textContent = error.message; target.hidden = false;
    } finally {
      if (attempt === authAttempt) loginController = null;
    }
  }

  function openRouteDialog(route) {
    const form = $("#route-form"); form.reset(); $("#route-form-error").hidden = true;
    $("#route-dialog-title").textContent = route ? "Edit route" : "Add route";
    $("#route-id").value = route?.id || "";
    if (route) {
      ["name", "client_id", "domain", "local_host", "local_port", "tunnel_group"].forEach(key => {
        const input = form.elements[key]; if (input && route[key] !== undefined) input.value = route[key];
      });
      form.elements.enabled.checked = Boolean(route.enabled);
    } else { form.elements.local_host.value = "127.0.0.1"; form.elements.tunnel_group.value = "web"; form.elements.enabled.checked = true; }
    form.elements.client_id.disabled = Boolean(route);
    $("#route-dialog").showModal();
  }
  function routePayload(form) {
    const data = new FormData(form);
    return {
      name: String(data.get("name")).trim(), client_id: String(form.elements.client_id.value), protocol: "http",
      domain: String(data.get("domain")).trim().toLowerCase(),
      local_host: String(data.get("local_host")).trim(), local_port: Number(data.get("local_port")),
      public_port: 0, tunnel_group: String(data.get("tunnel_group")).trim(), enabled: data.get("enabled") === "on"
    };
  }
  async function saveRoute(event) {
    event.preventDefault(); const form = event.currentTarget; const id = $("#route-id").value;
    const error = $("#route-form-error"); error.hidden = true;
    const submit = form.querySelector("button[type=submit]"); submit.disabled = true;
    try {
      await request(id ? `/routes/${encodeURIComponent(id)}` : "/routes", { method: id ? "PUT" : "POST", body: JSON.stringify(routePayload(form)) });
      $("#route-dialog").close(); await loadAll(); showNotice(id ? "Route updated." : "Route created.", "success");
    } catch (reason) { error.textContent = reason.message; error.hidden = false; }
    finally { submit.disabled = false; }
  }
  function shellQuote(value) {
    return `'${String(value).replaceAll("'", `'"'"'`)}'`;
  }
  function isSafeImageTag(value) {
    return typeof value === "string" && /^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$/.test(value);
  }
  function buildAgentInstallCommand(options) {
    const args = [
      ["--server-url", options.serverURL], ["--name", options.name], ["--token", options.token],
      ["--ssh-host", options.sshHost], ["--ssh-port", String(options.sshPort)], ["--ssh-host-key", options.sshHostKey]
    ];
    if (isSafeImageTag(options.version)) args.push(["--version", options.version]);
    return [
      "curl -fsSLo portloom-install-agent.sh https://docs.961121.xyz/install-agent.sh",
      "chmod 0700 portloom-install-agent.sh",
      `./portloom-install-agent.sh ${args.map(([flag, value]) => `${flag} ${shellQuote(value)}`).join(" ")}`
    ].join(" && ");
  }

  async function createToken(event) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form); const error = $("#token-form-error"); error.hidden = true;
    const submit = form.querySelector("button[type=submit]"); submit.disabled = true;
    try {
      if (!state.system?.managed_ssh || !state.system?.ssh_host_key) throw new APIError("Managed SSH is not enabled on this Server. Use the advanced installation guide.", 409);
      const result = await request("/enrollment-tokens", { method: "POST", body: JSON.stringify({ expires_in: String(data.get("expires_in")) }) });
      const secret = result?.token || result?.secret || result?.value;
      if (!secret) throw new APIError("The server created a token but did not return its one-time secret.", 500);
      const command = buildAgentInstallCommand({
        serverURL: String(data.get("server_url")).trim(), name: String(data.get("name")).trim(), token: secret,
        sshHost: String(data.get("ssh_host")).trim(), sshPort: Number(data.get("ssh_port")), sshHostKey: state.system.ssh_host_key,
        version: state.system.version
      });
      $("#token-dialog").close(); $("#created-token").textContent = command; $("#copy-token-button").textContent = "Copy command"; $("#secret-dialog").showModal(); await loadAll();
    } catch (reason) { error.textContent = reason.message; error.hidden = false; }
    finally { submit.disabled = false; }
  }
  async function confirmDelete(route) {
    state.deleteID = route.id; $("#confirm-message").textContent = `Delete “${route.name}”? This cannot be undone.`;
    const dialog = $("#confirm-dialog"); dialog.showModal(); const result = await new Promise(resolve => dialog.addEventListener("close", () => resolve(dialog.returnValue), { once: true }));
    if (result !== "confirm") return;
    try { await request(`/routes/${encodeURIComponent(route.id)}`, { method: "DELETE" }); await loadAll(); showNotice("Route deleted.", "success"); }
    catch (error) { showNotice(error.message); }
  }

  function bindEvents() {
    $("#login-form").addEventListener("submit", event => { event.preventDefault(); login(new FormData(event.currentTarget).get("token")); });
    $("#toggle-token").addEventListener("click", () => { const input = $("#admin-token"); const visible = input.type === "text"; input.type = visible ? "password" : "text"; $("#toggle-token").textContent = visible ? "Show" : "Hide"; });
    $("#logout-button").addEventListener("click", () => logout());
    $("#refresh-button").addEventListener("click", () => loadAll().catch(() => {}));
    $$(".nav-item").forEach(item => item.addEventListener("click", () => switchView(item.dataset.view)));
    $$('[data-goto]').forEach(item => item.addEventListener("click", () => switchView(item.dataset.goto)));
    $("#new-route-button").addEventListener("click", () => openRouteDialog());
    $("#new-token-button").addEventListener("click", () => {
      const form = $("#token-form"); form.reset(); $("#token-form-error").hidden = true;
      form.elements.server_url.value = location.origin.startsWith("https://") ? location.origin : "";
      form.elements.ssh_host.value = ["localhost", "127.0.0.1", "::1"].includes(location.hostname) ? "" : location.hostname;
      form.elements.ssh_port.value = state.system?.ssh_port || 2222; $("#token-dialog").showModal();
    });
    $("#route-form").addEventListener("submit", saveRoute);
    $("#token-form").addEventListener("submit", createToken);
    $("#routes-body").addEventListener("click", event => {
      const edit = event.target.closest("[data-edit-route]"); const remove = event.target.closest("[data-delete-route]");
      if (edit) openRouteDialog(state.routes.find(route => route.id === edit.dataset.editRoute));
      if (remove) confirmDelete(state.routes.find(route => route.id === remove.dataset.deleteRoute));
    });
    $$('[data-close-dialog]').forEach(button => button.addEventListener("click", () => button.closest("dialog").close()));
    bindSecretDialogCleanup();
    $("#copy-token-button").addEventListener("click", copyCreatedToken);
  }

  bindEvents();
  if (state.token) login(state.token); else { $("#login-screen").hidden = false; $("#app").hidden = true; }
})();

(() => {
  "use strict";

  const API = "/api/v1";
  const TOKEN_KEY = "portloom.admin-token";
  const LANGUAGE_KEY = "portloom.language";
  const i18n = window.PortLoomI18n;
  if (!i18n) throw new Error("PortLoom translations failed to load");
  let savedLocale = "";
  try { savedLocale = localStorage.getItem(LANGUAGE_KEY) || ""; } catch (_) { /* storage may be unavailable */ }
  const state = {
    token: sessionStorage.getItem(TOKEN_KEY) || "", system: {}, clients: [], tokens: [], routes: [],
    view: "dashboard", deleteID: "", locale: i18n.normalizeLocale(savedLocale), loaded: false,
    loading: false, apiOnline: null, updatedAt: null
  };
  let authAttempt = 0;
  let loginController = null;
  const $ = (selector, root = document) => root.querySelector(selector);
  const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];
  const t = (key, replacements = {}, fallback = key) => i18n.translate(state.locale, key, replacements, fallback);

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
    catch (_) { throw new APIError(t("error.apiUnreachable"), 0); }
    if (response.status === 401 || response.status === 403) {
      if (requestToken && state.token === requestToken && authAttempt === requestAuthAttempt) logout();
      throw new APIError(t("error.adminRejected"), response.status);
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
    const readable = status.replaceAll("_", " ");
    return el("span", `badge ${tone(status)}`, t(`status.${readable}`, {}, readable));
  }
  function statusLayer(label, value) {
    const status = normalizedStatus(value);
    const row = el("div", `status-layer ${tone(status)}`);
    const readable = status.replaceAll("_", " ");
    row.append(el("b", "", t(`status.${label.toLowerCase()}`, {}, label)), el("i"), el("span", "", t(`status.${readable}`, {}, readable)));
    return row;
  }
  function routePublicStatus(route) {
    if (!route.enabled) return "disabled";
    if (route.public_status) return String(route.public_status).trim().toLowerCase();
    const revisionCurrent = Number(route.observed_revision || 0) >= Number(route.desired_revision || 0);
    return revisionCurrent && route.tunnel_status === "up" ? "published" : "waiting_agent";
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
    return Boolean(route.enabled && local && tunnel && revisions && routePublicStatus(route) === "published");
  }

  function formatDate(value) {
    if (!value) return "—";
    const date = new Date(value);
    return Number.isNaN(date.valueOf()) ? String(value) : new Intl.DateTimeFormat(state.locale, { dateStyle: "medium", timeStyle: "short" }).format(date);
  }
  function relativeDate(value) {
    if (!value) return t("common.never");
    const time = new Date(value).valueOf();
    if (Number.isNaN(time)) return String(value);
    const delta = Math.round((time - Date.now()) / 1000);
    const abs = Math.abs(delta);
    const [unit, divisor] = abs < 60 ? ["second", 1] : abs < 3600 ? ["minute", 60] : abs < 86400 ? ["hour", 3600] : ["day", 86400];
    return new Intl.RelativeTimeFormat(state.locale, { numeric: "auto" }).format(Math.round(delta / divisor), unit);
  }
  function clientOnline(client) {
    const explicit = normalizedStatus(client.status || client.connection_status, "");
    if (explicit) return tone(explicit) === "good";
    if (!client.last_seen_at && !client.last_seen) return false;
    return Date.now() - new Date(client.last_seen_at || client.last_seen).valueOf() < 90_000;
  }
  function clientName(client) { return client.name || client.hostname || client.id || t("common.unnamedClient"); }

  function updateLastUpdated() {
    const target = $("#last-updated");
    target.textContent = state.updatedAt
      ? t("updated.at", { time: new Intl.DateTimeFormat(state.locale, { timeStyle: "medium" }).format(state.updatedAt) })
      : t("updated.never");
  }

  function updateContextAction() {
    const actions = {
      tokens: { label: "context.generateAgent", shortLabel: "context.generateAgentShort", action: "token" },
      routes: { label: "context.addRoute", shortLabel: "context.addRouteShort", action: "route" }
    };
    const config = actions[state.view];
    const button = $("#context-action-button");
    const fullLabel = config ? t(config.label) : "";
    const shortLabel = config ? t(config.shortLabel) : "";
    button.hidden = !config;
    button.dataset.action = config?.action || "";
    button.setAttribute("aria-label", fullLabel);
    button.querySelector(".context-action-full").textContent = fullLabel;
    button.querySelector(".context-action-short").textContent = shortLabel;
  }

  function applyLocale() {
    document.documentElement.lang = state.locale;
    $$('[data-i18n]').forEach(node => { node.textContent = t(node.dataset.i18n); });
    for (const [attribute, selector] of [["placeholder", "[data-i18n-placeholder]"], ["aria-label", "[data-i18n-aria-label]"], ["title", "[data-i18n-title]"], ["content", "[data-i18n-content]"]]) {
      $$(selector).forEach(node => node.setAttribute(attribute, t(node.dataset[`i18n${attribute.split("-").map(part => part[0].toUpperCase() + part.slice(1)).join("")}`])));
    }
    $$('[data-language-toggle]').forEach(button => {
      const chinese = state.locale === "zh-CN";
      button.textContent = t(chinese ? "language.english" : "language.chinese");
      button.setAttribute("aria-label", t(chinese ? "language.switchEnglish" : "language.switchChinese"));
    });
    const tokenVisible = $("#admin-token").type === "text";
    $("#toggle-token").textContent = t(tokenVisible ? "common.hide" : "common.show");
    $("#toggle-token").setAttribute("aria-label", t(tokenVisible ? "common.hide" : "common.show"));
    $("#page-title").textContent = t(`page.${state.view}`);
    updateContextAction();
    updateLastUpdated();
    if (state.apiOnline !== null) setAPIState(state.apiOnline);
    setLoading(state.loading);
    if (state.loaded) renderAll();
  }

  function toggleLocale() {
    state.locale = state.locale === "zh-CN" ? "en" : "zh-CN";
    try { localStorage.setItem(LANGUAGE_KEY, state.locale); } catch (_) { /* storage may be unavailable */ }
    applyLocale();
  }

  function showNotice(message, kind = "error") {
    const notice = $("#notice");
    notice.textContent = message;
    notice.className = `notice ${kind}`;
    notice.hidden = false;
    clearTimeout(showNotice.timer);
    showNotice.timer = setTimeout(() => { notice.hidden = true; }, 6000);
  }
  function setAPIState(online) {
    state.apiOnline = online;
    const indicator = $("#api-indicator");
    indicator.className = `connection ${online ? "online" : "offline"}`;
    indicator.lastChild.textContent = ` ${t(online ? "api.connected" : "api.unavailable")}`;
  }
  function setLoading(loading) {
    state.loading = loading;
    const button = $("#refresh-button");
    const label = t(loading ? "common.refreshing" : "common.refresh");
    button.disabled = loading;
    button.setAttribute("aria-label", label);
    button.querySelector(".refresh-full").textContent = label;
  }

  async function loadAll({ quiet = false, signal } = {}) {
    setLoading(true);
    try {
      const [systemPayload, clientsPayload, tokensPayload, routesPayload] = await Promise.all([
        request("/system", { signal }), request("/clients", { signal }), request("/enrollment-tokens", { signal }), request("/routes", { signal })
      ]);
      if (signal?.aborted) throw new APIError(t("error.cancelled"), 0);
      state.system = systemPayload || {};
      state.clients = asList(clientsPayload, "clients");
      state.tokens = asList(tokensPayload, "tokens");
      state.routes = asList(routesPayload, "routes");
      state.loaded = true;
      renderAll();
      setAPIState(true);
      state.updatedAt = new Date();
      updateLastUpdated();
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
    const enabled = state.routes.filter(route => route.enabled).length;
    const healthy = state.routes.filter(routeHealthy).length;
    const drift = state.routes.filter(route => Number(route.observed_revision || 0) < Number(route.desired_revision || 0)).length;
    $("#metric-clients").textContent = String(online);
    $("#metric-clients-detail").textContent = t("metrics.clientsTotal", { count: state.clients.length });
    $("#metric-routes").textContent = String(enabled);
    $("#metric-routes-detail").textContent = t("metrics.routesTotal", { count: state.routes.length });
    $("#metric-tunnels").textContent = String(healthy);
    $("#metric-tunnels-detail").textContent = enabled ? t("metrics.healthyPercent", { percent: Math.round(healthy / enabled * 100) }) : t("metrics.noEnabledRoutes");
    $("#metric-drift").textContent = String(drift);
    $("#metric-drift-detail").textContent = t(drift ? "metrics.awaitingConvergence" : "metrics.revisionsMatch");

    const container = $("#dashboard-routes");
    container.replaceChildren();
    const routes = [...state.routes].sort((a, b) => Number(routeHealthy(a)) - Number(routeHealthy(b))).slice(0, 6);
    if (!routes.length) { container.className = "route-health-list empty-state"; container.textContent = t("dashboard.noRoutes"); return; }
    container.className = "route-health-list";
    routes.forEach(route => {
      const item = el("div", "route-health-item");
      const name = el("div"); name.append(el("span", "cell-title", route.name), el("span", "cell-detail", route.domain || `${route.local_host}:${route.local_port}`));
      item.append(name, routeStatus(route), badge(routeHealthy(route) ? "healthy" : route.enabled ? "attention" : "disabled"));
      container.append(item);
    });
  }

  function clientCountLabel(count) {
    return t(count === 1 ? "clients.countOne" : "clients.countOther", { count });
  }

  function renderClients() {
    const body = $("#clients-body"); body.replaceChildren();
    $("#clients-empty").hidden = state.clients.length > 0;
    $("#client-count").textContent = clientCountLabel(state.clients.length);
    state.clients.forEach(client => {
      const row = el("tr");
      addCell(row, clientName(client), client.id);
      const statusCell = el("td"); statusCell.append(badge(clientOnline(client) ? "online" : "offline")); row.append(statusCell);
      addCell(row, `${client.observed_revision || 0} / ${client.desired_revision || client.revision || 0}`, t("table.observedDesired"));
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
      addCell(row, token.id || t("tokens.defaultID"));
      const statusCell = el("td"); statusCell.append(badge(tokenStatus(token))); row.append(statusCell);
      addCell(row, formatDate(token.created_at));
      addCell(row, formatDate(token.expires_at), token.expires_at ? relativeDate(token.expires_at) : t("common.noExpiry"));
      addCell(row, token.used_by || token.client_name || token.client_id || "—", token.used_at ? formatDate(token.used_at) : "");
      body.append(row);
    });
  }

  function routeExposure(route) {
    if (route.protocol !== "tcp") return route.domain || "—";
    return route.public_port ? `TCP :${route.public_port}` : t("routes.tcpAuto");
  }

  function renderRoutes() {
    const body = $("#routes-body"); body.replaceChildren();
    $("#routes-empty").hidden = state.routes.length > 0;
    state.routes.forEach(route => {
      const row = el("tr");
      addCell(row, route.name || t("common.unnamedRoute"), `${route.protocol || "http"} · ${t(route.enabled ? "routes.enabled" : "routes.disabled")}`);
      addCell(row, `${route.local_host}:${route.local_port}`, state.clients.find(c => c.id === route.client_id)?.name || route.client_id);
      const exposure = routeExposure(route);
      addCell(row, exposure, route.remote_port ? t("routes.loopbackPort", { port: route.remote_port }) : t("routes.portPending"));
      const statusCell = el("td"); statusCell.append(routeStatus(route)); row.append(statusCell);
      addCell(row, `${route.observed_revision || 0} / ${route.desired_revision || 0}`, t("table.observedDesired"));
      const actions = el("td", "align-right"); const group = el("div", "action-group");
      const edit = el("button", "action-button", t("routes.edit")); edit.type = "button"; edit.dataset.editRoute = route.id;
      const remove = el("button", "action-button delete", t("routes.delete")); remove.type = "button"; remove.dataset.deleteRoute = route.id;
      group.append(edit, remove); actions.append(group); row.append(actions); body.append(row);
    });
  }

  function populateClientSelect() {
    const select = $("#route-client"); const selected = select.value; select.replaceChildren();
    if (!state.clients.length) { const option = el("option", "", t("clients.noneSelect")); option.value = ""; select.append(option); return; }
    state.clients.forEach(client => { const option = el("option", "", clientName(client)); option.value = client.id; select.append(option); });
    if (state.clients.some(client => client.id === selected)) select.value = selected;
  }

  function switchView(view) {
    state.view = view;
    $$(".nav-item").forEach(item => item.classList.toggle("active", item.dataset.view === view));
    $$(".view").forEach(section => section.classList.toggle("active", section.id === `view-${view}`));
    $("#page-title").textContent = t(`page.${view}`);
    updateContextAction();
  }

  function clearSensitiveUI() {
    const adminToken = $("#admin-token");
    adminToken.value = "";
    adminToken.type = "password";
    $("#toggle-token").textContent = t("common.show");
    $("#toggle-token").setAttribute("aria-label", t("common.show"));
    $("#created-token").textContent = "";
    $("#copy-token-button").textContent = t("common.copyCommand");
  }

  function clearCreatedToken() {
    $("#created-token").textContent = "";
    $("#copy-token-button").textContent = t("common.copyCommand");
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
      if (dialog.open && tokenNode.textContent === secret) $("#copy-token-button").textContent = t("common.copied");
    } catch (_) {
      showNotice(t("error.clipboardDenied"));
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

  function syncRouteProtocolFields(form) {
    const protocol = String(form.elements.protocol.value || "http").toLowerCase();
    const tcp = protocol === "tcp";
    $("#route-domain-field").hidden = tcp;
    $("#route-public-port-field").hidden = !tcp;
    form.elements.domain.required = !tcp;
    form.elements.public_port.required = tcp;
    if (tcp && !form.elements.tunnel_group.value) form.elements.tunnel_group.value = "tcp";
    if (!tcp && !form.elements.tunnel_group.value) form.elements.tunnel_group.value = "web";
  }

  function openRouteDialog(route) {
    const form = $("#route-form"); form.reset(); $("#route-form-error").hidden = true;
    $("#route-dialog-title").textContent = t(route ? "dialog.editRoute" : "dialog.addRoute");
    $("#route-id").value = route?.id || "";
    const tcpOption = form.elements.protocol.querySelector('option[value="tcp"]');
    tcpOption.disabled = !state.system?.tcp_edge && route?.protocol !== "tcp";
    if (route) {
      ["name", "client_id", "protocol", "domain", "local_host", "local_port", "public_port", "tunnel_group"].forEach(key => {
        const input = form.elements[key]; if (input && route[key] !== undefined) input.value = route[key];
      });
      form.elements.enabled.checked = Boolean(route.enabled);
    } else {
      form.elements.protocol.value = "http"; form.elements.local_host.value = "127.0.0.1";
      form.elements.tunnel_group.value = "web"; form.elements.enabled.checked = true;
    }
    form.elements.client_id.disabled = Boolean(route);
    syncRouteProtocolFields(form);
    $("#route-dialog").showModal();
  }
  function routePayload(form) {
    const data = new FormData(form);
    const protocol = String(form.elements.protocol.value || "http").toLowerCase();
    return {
      name: String(data.get("name")).trim(), client_id: String(form.elements.client_id.value), protocol,
      domain: protocol === "http" ? String(data.get("domain")).trim().toLowerCase() : "",
      local_host: String(data.get("local_host")).trim(), local_port: Number(data.get("local_port")),
      public_port: protocol === "tcp" ? Number(data.get("public_port")) : 0,
      tunnel_group: String(data.get("tunnel_group")).trim(), enabled: data.get("enabled") === "on"
    };
  }
  async function monitorRoutePublication(id) {
    const terminalErrors = new Set(["conflict", "bind_error", "invalid", "tcp_edge_disabled"]);
    for (let attempt = 0; attempt < 90 && state.token; attempt += 1) {
      const route = await request(`/routes/${encodeURIComponent(id)}`);
      const index = state.routes.findIndex(item => item.id === route.id);
      if (index >= 0) state.routes[index] = route; else state.routes.push(route);
      renderRoutes();
      const status = routePublicStatus(route);
      if (status === "published" || status === "disabled") {
        showNotice(t(status === "published" ? "routes.published" : "routes.savedDisabled"), "success");
        return;
      }
      if (terminalErrors.has(status)) {
        showNotice(t(`status.${status.replaceAll("_", " ")}`, {}, status));
        return;
      }
      await new Promise(resolve => setTimeout(resolve, 1000));
    }
    if (state.token) showNotice(t("routes.publishTimeout"));
  }
  async function saveRoute(event) {
    event.preventDefault(); const form = event.currentTarget; const id = $("#route-id").value;
    const error = $("#route-form-error"); error.hidden = true;
    const submit = form.querySelector("button[type=submit]"); submit.disabled = true;
    try {
      const saved = await request(id ? `/routes/${encodeURIComponent(id)}` : "/routes", { method: id ? "PUT" : "POST", body: JSON.stringify(routePayload(form)) });
      $("#route-dialog").close(); await loadAll(); showNotice(t("routes.waitingForPublish"), "success");
      void monitorRoutePublication(saved.id).catch(reason => showNotice(reason.message));
    } catch (reason) { error.textContent = reason.message; error.hidden = false; }
    finally { submit.disabled = false; }
  }
  function shellQuote(value) {
    return `'${String(value).replaceAll("'", `'"'"'`)}'`;
  }
  function isSafeImageTag(value) {
    return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/.test(value);
  }
  function isSafeAgentName(value) {
    return typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(value);
  }
  function buildAgentInstallCommand(options) {
    const args = [
      ["--server-url", options.serverURL], ["--name", options.name], ["--token", options.token],
      ["--ssh-host", options.sshHost], ["--ssh-port", String(options.sshPort)], ["--ssh-host-key", options.sshHostKey]
    ];
    if (isSafeImageTag(options.version)) args.push(["--version", options.version]);
    return [
      "export PATH=/opt/bin:/opt/sbin:$PATH",
      "if command -v curl >/dev/null 2>&1; then curl -fsSLo portloom-install-agent.sh https://docs.961121.xyz/install-agent.sh; elif command -v wget >/dev/null 2>&1; then wget -qO portloom-install-agent.sh https://docs.961121.xyz/install-agent.sh; else echo 'curl or wget is required' >&2; exit 1; fi",
      "chmod 0700 portloom-install-agent.sh",
      `./portloom-install-agent.sh ${args.map(([flag, value]) => `${flag} ${shellQuote(value)}`).join(" ")}`
    ].join(" && ");
  }

  async function createToken(event) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form); const error = $("#token-form-error"); error.hidden = true;
    const submit = form.querySelector("button[type=submit]"); submit.disabled = true;
    try {
      const agentName = String(data.get("name")).trim();
      if (!isSafeAgentName(agentName)) throw new APIError(t("agent.invalidName"), 400);
      if (!state.system?.managed_ssh || !state.system?.ssh_host_key) throw new APIError(t("agent.managedSSHDisabled"), 409);
      const result = await request("/enrollment-tokens", { method: "POST", body: JSON.stringify({ expires_in: String(data.get("expires_in")) }) });
      const secret = result?.token || result?.secret || result?.value;
      if (!secret) throw new APIError(t("agent.noSecret"), 500);
      const command = buildAgentInstallCommand({
        serverURL: String(data.get("server_url")).trim(), name: agentName, token: secret,
        sshHost: String(data.get("ssh_host")).trim(), sshPort: Number(data.get("ssh_port")), sshHostKey: state.system.ssh_host_key,
        version: state.system.version
      });
      $("#token-dialog").close(); $("#created-token").textContent = command; $("#copy-token-button").textContent = t("common.copyCommand"); $("#secret-dialog").showModal(); await loadAll();
    } catch (reason) { error.textContent = reason.message; error.hidden = false; }
    finally { submit.disabled = false; }
  }
  async function confirmDelete(route) {
    state.deleteID = route.id; $("#confirm-message").textContent = t("routes.deleteQuestion", { name: route.name });
    const dialog = $("#confirm-dialog"); dialog.showModal(); const result = await new Promise(resolve => dialog.addEventListener("close", () => resolve(dialog.returnValue), { once: true }));
    if (result !== "confirm") return;
    try { await request(`/routes/${encodeURIComponent(route.id)}`, { method: "DELETE" }); await loadAll(); showNotice(t("routes.deleted"), "success"); }
    catch (error) { showNotice(error.message); }
  }

  function openTokenDialog() {
    const form = $("#token-form"); form.reset(); $("#token-form-error").hidden = true;
    form.elements.server_url.value = location.origin.startsWith("https://") ? location.origin : "";
    form.elements.ssh_host.value = ["localhost", "127.0.0.1", "::1"].includes(location.hostname) ? "" : location.hostname;
    form.elements.ssh_port.value = state.system?.ssh_port || 2222;
    $("#token-dialog").showModal();
  }

  function bindEvents() {
    $("#login-form").addEventListener("submit", event => { event.preventDefault(); login(new FormData(event.currentTarget).get("token")); });
    $("#toggle-token").addEventListener("click", () => {
      const input = $("#admin-token"); const visible = input.type === "text"; input.type = visible ? "password" : "text";
      $("#toggle-token").textContent = t(visible ? "common.show" : "common.hide");
      $("#toggle-token").setAttribute("aria-label", t(visible ? "common.show" : "common.hide"));
    });
    $$('[data-language-toggle]').forEach(button => button.addEventListener("click", toggleLocale));
    $("#logout-button").addEventListener("click", () => logout());
    $("#refresh-button").addEventListener("click", () => loadAll().catch(() => {}));
    $("#context-action-button").addEventListener("click", event => {
      if (event.currentTarget.dataset.action === "token") openTokenDialog();
      if (event.currentTarget.dataset.action === "route") openRouteDialog();
    });
    $$(".nav-item").forEach(item => item.addEventListener("click", () => switchView(item.dataset.view)));
    $$('[data-goto]').forEach(item => item.addEventListener("click", () => switchView(item.dataset.goto)));
    $("#route-form").addEventListener("submit", saveRoute);
    $("#route-protocol").addEventListener("change", event => syncRouteProtocolFields(event.currentTarget.form));
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

  applyLocale();
  bindEvents();
  if (state.token) login(state.token); else { $("#login-screen").hidden = false; $("#app").hidden = true; }
})();

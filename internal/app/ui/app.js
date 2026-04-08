const state = {
  logSource: "manager",
  session: null,
};

const els = {
  loginScreen: document.getElementById("login-screen"),
  dashboard: document.getElementById("dashboard"),
  loginForm: document.getElementById("login-form"),
  loginError: document.getElementById("login-error"),
  logoutButton: document.getElementById("logout-button"),
  userBadge: document.getElementById("user-badge"),
  sourceForm: document.getElementById("source-form"),
  sourceUrl: document.getElementById("source-url"),
  hwid: document.getElementById("hwid-value"),
  ua: document.getElementById("ua-value"),
  refreshValue: document.getElementById("refresh-value"),
  intervalValue: document.getElementById("interval-value"),
  sourceError: document.getElementById("source-error"),
  tunnelsGrid: document.getElementById("tunnels-grid"),
  runtimeBanner: document.getElementById("runtime-banner"),
  refreshButton: document.getElementById("refresh-button"),
  logsOutput: document.getElementById("logs-output"),
  reloadLogsButton: document.getElementById("reload-logs-button"),
};

async function api(path, options = {}) {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
    credentials: "same-origin",
    ...options,
  });

  let body = null;
  try {
    body = await response.json();
  } catch (_) {
    body = null;
  }

  if (!response.ok) {
    throw new Error(body?.error || `HTTP ${response.status}`);
  }
  return body;
}

function setScreen(authenticated, username = "") {
  els.loginScreen.classList.toggle("hidden", authenticated);
  els.dashboard.classList.toggle("hidden", !authenticated);
  els.userBadge.textContent = username ? `router: ${username}` : "";
}

function formatDate(value) {
  if (!value) return "-";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("ru-RU");
}

function renderRuntime(runtime) {
  if (!runtime || !runtime.state || runtime.state === "stopped") {
    els.runtimeBanner.className = "runtime-banner";
    els.runtimeBanner.textContent = "Сейчас активного туннеля нет.";
    return;
  }

  const level = runtime.state === "error" ? "error" : "ok";
  const text =
    runtime.state === "error"
      ? `Ошибка runtime: ${runtime.lastError || "неизвестно"}`
      : `Активен туннель ${runtime.activeTunnelId || "-"} • PID ${runtime.pid || "-"} • подключен ${runtime.connected ? "да" : "нет"}`;

  els.runtimeBanner.className = `runtime-banner ${level}`;
  els.runtimeBanner.textContent = text;
}

function renderTunnels(tunnels, runtime) {
  els.tunnelsGrid.innerHTML = "";
  renderRuntime(runtime);

  if (!Array.isArray(tunnels) || tunnels.length === 0) {
    els.tunnelsGrid.innerHTML = `<div class="card"><h3>Пока пусто</h3><p class="muted">Добавь Remnawave URL во вкладке "Источник".</p></div>`;
    return;
  }

  for (const tunnel of tunnels) {
    const card = document.createElement("article");
    card.className = "card";

    const stateClass = tunnel.missing ? "pill-warn" : tunnel.active ? "pill-ok" : "pill-err";
    const stateLabel = tunnel.missing ? "missing" : tunnel.active ? "active" : "idle";
    const warpLabel = tunnel.isWarp ? "WARP" : "regular";

    card.innerHTML = `
      <div class="card-meta">
        <span class="status-pill ${stateClass}">${stateLabel}</span>
        <span class="status-pill">${warpLabel}</span>
      </div>
      <h3>${escapeHtml(tunnel.name)}</h3>
      <div class="card-meta">
        <span>${escapeHtml(tunnel.interfaceName || "-")}</span>
        <span>${escapeHtml(tunnel.server)}:${tunnel.port}</span>
      </div>
      <div class="card-meta">
        <span>SNI: ${escapeHtml(tunnel.sni || "-")}</span>
        <span>ALPN: ${escapeHtml((tunnel.alpn || []).join(", ") || "-")}</span>
      </div>
      <div class="card-meta">
        <span>Auth: ${escapeHtml(tunnel.authMasked || "-")}</span>
        <span>Seen: ${escapeHtml(formatDate(tunnel.lastSeenInSubscription))}</span>
      </div>
      <div class="card-footer">
        <button class="activate-button">${tunnel.active ? "Перезапустить" : "Активировать"}</button>
        <button class="ghost deactivate-button">Выключить</button>
      </div>
    `;

    const activateButton = card.querySelector(".activate-button");
    const deactivateButton = card.querySelector(".deactivate-button");
    activateButton.disabled = tunnel.missing;
    deactivateButton.disabled = !tunnel.active;

    activateButton.addEventListener("click", async () => {
      activateButton.disabled = true;
      try {
        await api(`/api/tunnels/${tunnel.id}/activate`, { method: "POST" });
        await refreshDashboard();
      } catch (error) {
        alert(error.message);
      } finally {
        activateButton.disabled = tunnel.missing;
      }
    });

    deactivateButton.addEventListener("click", async () => {
      deactivateButton.disabled = true;
      try {
        await api(`/api/tunnels/${tunnel.id}/deactivate`, { method: "POST" });
        await refreshDashboard();
      } catch (error) {
        alert(error.message);
      } finally {
        deactivateButton.disabled = !tunnel.active;
      }
    });

    els.tunnelsGrid.appendChild(card);
  }
}

function renderSettings(settings) {
  els.sourceUrl.value = settings.subscription?.url || "";
  els.hwid.textContent = settings.subscription?.hwid || "-";
  els.ua.textContent = settings.subscription?.userAgent || "-";
  els.refreshValue.textContent = formatDate(settings.subscription?.lastRefreshAt);
  els.intervalValue.textContent = settings.subscription?.refreshIntervalHours ? `${settings.subscription.refreshIntervalHours} ч` : "-";
  els.sourceError.textContent = settings.subscription?.lastError || "-";
}

async function refreshDashboard() {
  const [settings, tunnels, runtime] = await Promise.all([
    api("/api/settings"),
    api("/api/tunnels"),
    api("/api/runtime/status"),
  ]);

  renderSettings(settings);
  renderTunnels(tunnels, runtime);
}

async function refreshLogs() {
  const payload = await api(`/api/logs?source=${encodeURIComponent(state.logSource)}`);
  els.logsOutput.textContent = payload.content || "";
}

async function bootstrap() {
  try {
    const session = await api("/api/session");
    state.session = session;
    if (!session.authenticated) {
      setScreen(false);
      return;
    }

    setScreen(true, session.username);
    await Promise.all([refreshDashboard(), refreshLogs()]);
  } catch (error) {
    setScreen(false);
    els.loginError.textContent = error.message;
    els.loginError.classList.remove("hidden");
  }
}

els.loginForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  els.loginError.classList.add("hidden");
  try {
    const payload = await api("/api/login", {
      method: "POST",
      body: JSON.stringify({
        login: document.getElementById("login-input").value,
        password: document.getElementById("password-input").value,
      }),
    });
    setScreen(true, payload.username);
    await Promise.all([refreshDashboard(), refreshLogs()]);
  } catch (error) {
    els.loginError.textContent = error.message;
    els.loginError.classList.remove("hidden");
  }
});

els.logoutButton.addEventListener("click", async () => {
  await api("/api/logout", { method: "POST" });
  setScreen(false);
});

els.sourceForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  try {
    await api("/api/subscription/import", {
      method: "POST",
      body: JSON.stringify({ url: els.sourceUrl.value }),
    });
    await refreshDashboard();
  } catch (error) {
    alert(error.message);
  }
});

els.refreshButton.addEventListener("click", async () => {
  try {
    await api("/api/subscription/refresh", { method: "POST" });
    await refreshDashboard();
  } catch (error) {
    alert(error.message);
  }
});

els.reloadLogsButton.addEventListener("click", refreshLogs);

document.querySelectorAll(".log-source").forEach((button) => {
  button.addEventListener("click", async () => {
    document.querySelectorAll(".log-source").forEach((item) => item.classList.remove("active"));
    button.classList.add("active");
    state.logSource = button.dataset.source;
    await refreshLogs();
  });
});

document.querySelectorAll(".tab").forEach((button) => {
  button.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach((item) => item.classList.remove("active"));
    document.querySelectorAll(".panel").forEach((item) => item.classList.remove("active"));
    button.classList.add("active");
    document.getElementById(`panel-${button.dataset.tab}`).classList.add("active");
  });
});

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

// --- Routing ---

let routingConfig = null;

async function refreshRouting() {
  routingConfig = await api("/api/routing");
  renderRouting(routingConfig);
}

async function saveRouting(patch) {
  routingConfig = await api("/api/routing", {
    method: "PUT",
    body: JSON.stringify(patch),
  });
  renderRouting(routingConfig);
}

function renderRouting(cfg) {
  if (!cfg) return;

  const modeSelect = document.getElementById("routing-mode-select");
  modeSelect.value = cfg.mode || "selective";

  const selectiveDiv = document.getElementById("selective-config");
  selectiveDiv.style.display = cfg.mode === "global" ? "none" : "";

  renderDomainGroups(cfg.domainGroups || []);
  renderStaticRoutes(cfg.staticRoutes || []);
}

function renderDomainGroups(groups) {
  const container = document.getElementById("domain-groups-list");
  container.innerHTML = "";

  if (groups.length === 0) {
    container.innerHTML = '<div class="card muted">Нет групп доменов. Добавьте ниже.</div>';
    return;
  }

  for (const group of groups) {
    const card = document.createElement("article");
    card.className = "card";
    card.innerHTML = `
      <div class="card-meta">
        <span class="status-pill ${group.enabled ? "pill-ok" : "pill-err"}">${group.enabled ? "вкл" : "выкл"}</span>
        <strong>${escapeHtml(group.name)}</strong>
        <span class="muted">${group.domains.length} домен(ов)</span>
      </div>
      <div class="domain-list">${group.domains.map(d => escapeHtml(d)).join(", ")}</div>
      <div class="card-footer">
        <button class="ghost toggle-dg">${group.enabled ? "Выключить" : "Включить"}</button>
        <button class="ghost delete-dg">Удалить</button>
      </div>
    `;

    card.querySelector(".toggle-dg").addEventListener("click", async () => {
      const updated = (routingConfig.domainGroups || []).map(g =>
        g.name === group.name ? { ...g, enabled: !g.enabled } : g
      );
      await saveRouting({ domainGroups: updated });
    });

    card.querySelector(".delete-dg").addEventListener("click", async () => {
      const updated = (routingConfig.domainGroups || []).filter(g => g.name !== group.name);
      await saveRouting({ domainGroups: updated });
    });

    container.appendChild(card);
  }
}

function renderStaticRoutes(routes) {
  const container = document.getElementById("static-routes-list");
  container.innerHTML = "";

  if (routes.length === 0) {
    container.innerHTML = '<div class="card muted">Нет статических маршрутов.</div>';
    return;
  }

  for (const route of routes) {
    const card = document.createElement("article");
    card.className = "card";
    card.innerHTML = `
      <div class="card-meta">
        <span class="status-pill ${route.enabled ? "pill-ok" : "pill-err"}">${route.enabled ? "вкл" : "выкл"}</span>
        <code>${escapeHtml(route.cidr)}</code>
      </div>
      <div class="card-footer">
        <button class="ghost toggle-sr">${route.enabled ? "Выключить" : "Включить"}</button>
        <button class="ghost delete-sr">Удалить</button>
      </div>
    `;

    card.querySelector(".toggle-sr").addEventListener("click", async () => {
      const updated = (routingConfig.staticRoutes || []).map(r =>
        r.cidr === route.cidr ? { ...r, enabled: !r.enabled } : r
      );
      await saveRouting({ staticRoutes: updated });
    });

    card.querySelector(".delete-sr").addEventListener("click", async () => {
      const updated = (routingConfig.staticRoutes || []).filter(r => r.cidr !== route.cidr);
      await saveRouting({ staticRoutes: updated });
    });

    container.appendChild(card);
  }
}

document.getElementById("routing-mode-select").addEventListener("change", async (e) => {
  await saveRouting({ mode: e.target.value });
});

document.getElementById("add-domain-group-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const name = document.getElementById("dg-name").value.trim();
  const domains = document.getElementById("dg-domains").value
    .split("\n")
    .map(d => d.trim())
    .filter(Boolean);

  if (!name || domains.length === 0) return;

  const existing = routingConfig.domainGroups || [];
  if (existing.some(g => g.name === name)) {
    alert("Группа с таким именем уже существует");
    return;
  }

  const updated = [...existing, { name, domains, enabled: true }];
  await saveRouting({ domainGroups: updated });

  document.getElementById("dg-name").value = "";
  document.getElementById("dg-domains").value = "";
});

document.getElementById("add-static-route-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const cidr = document.getElementById("sr-cidr").value.trim();
  if (!cidr) return;

  const existing = routingConfig.staticRoutes || [];
  if (existing.some(r => r.cidr === cidr)) {
    alert("Такой маршрут уже существует");
    return;
  }

  const updated = [...existing, { cidr, enabled: true }];
  await saveRouting({ staticRoutes: updated });

  document.getElementById("sr-cidr").value = "";
});

// Patch refreshDashboard to also load routing
const _origRefreshDashboard = refreshDashboard;
refreshDashboard = async function () {
  await Promise.all([_origRefreshDashboard(), refreshRouting()]);
};

bootstrap();

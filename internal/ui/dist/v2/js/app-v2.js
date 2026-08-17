window.CR = window.CR || {};

CR.TOKEN_KEY = "overseer_jwt";
CR.BRAND = "Overseer";

(function () {
  if (!document.querySelector('link[rel="icon"]')) {
    const link = document.createElement("link");
    link.rel = "icon";
    link.type = "image/svg+xml";
    link.href = "../img/overseer.svg";
    document.head.appendChild(link);
  }
})();

CR.getToken = function () {
  return localStorage.getItem(CR.TOKEN_KEY) || localStorage.getItem("codereview_jwt") || "";
};

CR.setToken = function (token) {
  localStorage.setItem(CR.TOKEN_KEY, token || "");
};

CR.clearToken = function () {
  localStorage.removeItem(CR.TOKEN_KEY);
};

CR.authHeaders = function (extra) {
  const headers = Object.assign({ Accept: "application/json" }, extra || {});
  const token = CR.getToken();
  if (token) headers.Authorization = "Bearer " + token;
  return headers;
};

CR.redirectToLogin = function () {
  const here = location.pathname.split("/").pop() || "index.html";
  if (here === "login.html") return;
  CR.clearToken();
  location.href = "/v2/login.html";
};

CR.navItems = [
  { href: "/v2/index.html", id: "overview", label: "总览", icon: "bi-grid-1x2" },
  { href: "/v2/instances.html", id: "instances", label: "实例", icon: "bi-hdd-network" },
  { href: "/v2/repos.html", id: "repos", label: "仓库", icon: "bi-git" },
  { href: "/v2/llm.html", id: "llm", label: "LLM", icon: "bi-cpu" },
  { href: "/v2/prompts.html", id: "prompts", label: "提示词", icon: "bi-braces" },
  { href: "/v2/firewall.html", id: "firewall", label: "防火墙", icon: "bi-shield-check" },
  { href: "/v2/notifications.html", id: "notifications", label: "通知", icon: "bi-bell" },
  { href: "/v2/reviews.html", id: "reviews", label: "审查记录", icon: "bi-journal-code" },
  { href: "/v2/settings.html", id: "settings", label: "设置", icon: "bi-sliders" },
];

CR.pageMeta = {
  overview: ["OPERATIONS", "实时掌握审查系统的运行状态与近期活动。"],
  instances: ["CONNECTIONS", "管理代码托管平台连接、凭证与仓库同步。"],
  repos: ["REPOSITORIES", "配置仓库审查策略、触发来源与通知分组。"],
  llm: ["MODEL RUNTIME", "配置审查模型、协议与推理参数。"],
  prompts: ["REVIEW POLICY", "编辑并验证代码审查提示词模板。"],
  firewall: ["GUARDRAILS", "定义审查边界与内容过滤规则。"],
  notifications: ["DELIVERY", "管理 Webhook 渠道与通知分组。"],
  reviews: ["AUDIT TRAIL", "检索审查结果、状态与完整执行日志。"],
  settings: ["SYSTEM", "调整服务运行参数与全局行为。"],
};

CR.mountShell = function (activeId, title) {
  const shell = document.getElementById("app");
  if (!shell) return;
  if (!CR.getToken()) {
    CR.redirectToLogin();
    return;
  }

  const links = CR.navItems.map((item) => {
    const active = item.id === activeId ? " active" : "";
    return `<a class="nav-link${active}" href="${item.href}"${active ? ' aria-current="page"' : ""}>
      <i class="bi ${item.icon}" aria-hidden="true"></i><span>${item.label}</span>
    </a>`;
  }).join("");
  const pageContent = document.getElementById("page-content");
  const contentHTML = pageContent ? pageContent.innerHTML : "";
  const meta = CR.pageMeta[activeId] || ["CONTROL PLANE", "Overseer 管理控制台。"];
  const current = location.pathname.split("/").pop() || "index.html";

  shell.innerHTML = `
    <a class="skip-link" href="#main-content">跳到主要内容</a>
    <aside class="sidebar" aria-label="主导航">
      <a class="sidebar-brand" href="/v2/index.html" aria-label="Overseer 2.0 总览">
        <span class="brand-mark"><i class="bi bi-eye" aria-hidden="true"></i></span>
        <span><strong>OVERSEER</strong><small>CONTROL PLANE · 2.0</small></span>
      </a>
      <div class="nav-section-label">WORKSPACE</div>
      <nav class="sidebar-nav">${links}</nav>
      <div class="sidebar-footer">
        <span class="system-status"><i class="bi bi-circle-fill" aria-hidden="true"></i> SYSTEM ONLINE</span>
        <a href="/${current}" class="version-link"><i class="bi bi-arrow-left-right" aria-hidden="true"></i> 返回 1.0</a>
        <div class="sidebar-copyright">
          <span>© 2026 Overseer</span><span aria-hidden="true"> · </span>
          <a href="https://github.com/Audi-dask/Overseer.git" target="_blank" rel="noopener noreferrer">GitHub</a>
        </div>
      </div>
    </aside>
    <div class="main">
      <header class="topbar">
        <div class="topbar-context"><span>OVERSEER</span><i class="bi bi-chevron-right" aria-hidden="true"></i><strong>${meta[0]}</strong></div>
        <div class="topbar-actions">
          <span class="badge admin-badge" id="admin-badge"><i class="bi bi-person-circle" aria-hidden="true"></i> 管理员</span>
          <button type="button" class="btn btn-sm btn-outline-secondary" id="btn-logout"><i class="bi bi-box-arrow-right" aria-hidden="true"></i> 退出</button>
        </div>
      </header>
      <main class="content" id="main-content" tabindex="-1">
        <div class="page-heading"><div class="page-eyebrow">${meta[0]}</div><h1>${title}</h1><p>${meta[1]}</p></div>
        ${contentHTML}
      </main>
    </div>`;

  document.getElementById("btn-logout")?.addEventListener("click", () => {
    CR.clearToken();
    location.href = "/v2/login.html";
  });
  CR.apiGet("/api/auth/me").then((me) => {
    const badge = document.getElementById("admin-badge");
    if (badge && me.username) badge.innerHTML = `<i class="bi bi-person-circle" aria-hidden="true"></i> ${CR.escapeHtml(me.username)}`;
  }).catch(() => CR.redirectToLogin());
};

CR.apiGet = async function (path) {
  const res = await fetch(path, { headers: CR.authHeaders() });
  if (res.status === 401) {
    CR.redirectToLogin();
    throw new Error("未登录或登录已过期");
  }
  if (!res.ok) throw new Error(await CR.errorText(res));
  return await res.json();
};

CR.apiSend = async function (method, path, body) {
  const res = await fetch(path, {
    method,
    headers: CR.authHeaders({ "Content-Type": "application/json" }),
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 401) {
    CR.redirectToLogin();
    throw new Error("未登录或登录已过期");
  }
  if (!res.ok) throw new Error(await CR.errorText(res));
  const text = await res.text();
  return text ? JSON.parse(text) : {};
};

CR.errorText = async function (res) {
  try {
    const data = await res.json();
    if (data && data.error) return data.error;
  } catch {}
  return res.status + " " + res.statusText;
};

CR.statusBadge = function (status) {
  const labels = { success: "成功", failed: "失败", running: "进行中", skipped: "跳过", connected: "已连接", need_cred: "缺凭证", ok: "已配置", none: "未配置", pending: "配置中" };
  return `<span class="badge rounded-pill badge-status-${status}">${labels[status] || CR.escapeHtml(status)}</span>`;
};

CR.escapeHtml = function (s) {
  return String(s ?? "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
};

CR.shortSha = function (sha, n) {
  n = n || 8;
  sha = String(sha || "").trim();
  return sha.length <= n ? sha : sha.slice(0, n);
};

CR.formatTime = function (iso) {
  if (!iso) return "-";
  try {
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? iso : d.toLocaleString("zh-CN", { hour12: false });
  } catch { return iso; }
};

CR.showToast = function (message, type) {
  let el = document.getElementById("cr-toast");
  if (!el) {
    el = document.createElement("div");
    el.id = "cr-toast";
    el.setAttribute("role", "alert");
    el.innerHTML = `<div class="d-flex"><div class="toast-body"></div><button type="button" class="btn-close btn-close-white me-2 m-auto" data-bs-dismiss="toast" aria-label="关闭"></button></div>`;
    document.body.appendChild(el);
  }
  el.querySelector(".toast-body").textContent = message;
  el.className = "toast align-items-center border-0 position-fixed bottom-0 end-0 m-3 " + (type === "danger" ? "text-bg-danger" : type === "success" ? "text-bg-success" : "text-bg-dark");
  bootstrap.Toast.getOrCreateInstance(el, { delay: 2200 }).show();
};

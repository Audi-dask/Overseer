window.CR = window.CR || {};

CR.TOKEN_KEY = "overseer_jwt";
CR.BRAND = "Overseer";

(function () {
  if (!document.querySelector('link[rel="icon"]')) {
    const link = document.createElement("link");
    link.rel = "icon";
    link.type = "image/svg+xml";
    link.href = "img/overseer.svg";
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
  location.href = "login.html";
};

CR.navItems = [
  { href: "index.html", id: "overview", label: "总览", icon: "bi-speedometer2" },
  { href: "instances.html", id: "instances", label: "实例", icon: "bi-hdd-network" },
  { href: "repos.html", id: "repos", label: "仓库", icon: "bi-git" },
  { href: "llm.html", id: "llm", label: "LLM", icon: "bi-cpu" },
  { href: "prompts.html", id: "prompts", label: "审查提示词", icon: "bi-chat-left-text" },
  { href: "firewall.html", id: "firewall", label: "审查防火墙", icon: "bi-shield-check" },
  { href: "notifications.html", id: "notifications", label: "通知渠道", icon: "bi-bell" },
  { href: "reviews.html", id: "reviews", label: "审查记录", icon: "bi-journal-text" },
  { href: "settings.html", id: "settings", label: "服务设置", icon: "bi-gear" },
];

CR.mountShell = function (activeId, title) {
  const shell = document.getElementById("app");
  if (!shell) return;

  if (!CR.getToken()) {
    CR.redirectToLogin();
    return;
  }

  const links = CR.navItems
    .map((item) => {
      const active = item.id === activeId ? " active" : "";
      const disabled = item.disabled ? " disabled" : "";
      const titleAttr = item.title ? ` title="${item.title}"` : "";
      const href = item.disabled ? "#" : item.href;
      return `<a class="nav-link${active}${disabled}" href="${href}"${titleAttr}>
        <i class="bi ${item.icon}"></i><span>${item.label}${item.disabled ? " · 预留" : ""}</span>
      </a>`;
    })
    .join("");

  const pageContent = document.getElementById("page-content");
  const contentHTML = pageContent ? pageContent.innerHTML : "";

  shell.innerHTML = `
    <aside class="sidebar">
      <div class="sidebar-brand">
        <div class="brand-name"><i class="bi bi-eye"></i> Overseer</div>
        <div class="brand-sub">代码审查控制台</div>
      </div>
      <nav class="sidebar-nav">${links}</nav>
      <div class="sidebar-footer">
        <span>© 2026 Overseer</span>
        <span> · </span>
        <a href="https://github.com/Audi-dask/Overseer.git" target="_blank" rel="noopener noreferrer">GitHub</a>
      </div>
    </aside>
    <div class="main">
      <header class="topbar">
        <h1>${title}</h1>
        <div class="d-flex align-items-center gap-2">
          <span class="badge text-bg-light border" id="admin-badge">管理员</span>
          <button type="button" class="btn btn-sm btn-outline-secondary" id="btn-logout">退出</button>
        </div>
      </header>
      <div class="content">${contentHTML}</div>
    </div>
  `;

  document.getElementById("btn-logout")?.addEventListener("click", () => {
    CR.clearToken();
    location.href = "login.html";
  });

  CR.apiGet("/api/auth/me")
    .then((me) => {
      const badge = document.getElementById("admin-badge");
      if (badge && me.username) badge.textContent = me.username;
    })
    .catch(() => CR.redirectToLogin());
};

CR.apiGet = async function (path) {
  const res = await fetch(path, { headers: CR.authHeaders() });
  if (res.status === 401) {
    CR.redirectToLogin();
    throw new Error("未登录或登录已过期");
  }
  if (!res.ok) {
    throw new Error(await CR.errorText(res));
  }
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
  if (!res.ok) {
    throw new Error(await CR.errorText(res));
  }
  const text = await res.text();
  return text ? JSON.parse(text) : {};
};

CR.errorText = async function (res) {
  try {
    const data = await res.json();
    if (data && data.error) return data.error;
  } catch {
    // fall through to status text
  }
  return res.status + " " + res.statusText;
};

CR.statusBadge = function (status) {
  const cls = "badge rounded-pill badge-status-" + status;
  const labels = {
    success: "成功",
    failed: "失败",
    running: "进行中",
    skipped: "跳过",
    connected: "已连接",
    need_cred: "缺凭证",
    ok: "已配置",
    none: "未配置",
    pending: "配置中",
  };
  return `<span class="${cls}">${labels[status] || status}</span>`;
};

CR.escapeHtml = function (s) {
  return String(s ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
};

CR.shortSha = function (sha, n) {
  n = n || 8;
  sha = String(sha || "").trim();
  if (sha.length <= n) return sha;
  return sha.slice(0, n);
};

CR.formatTime = function (iso) {
  if (!iso) return "-";
  try {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    return d.toLocaleString("zh-CN", { hour12: false });
  } catch {
    return iso;
  }
};

CR.showToast = function (message, type) {
  let el = document.getElementById("cr-toast");
  if (!el) {
    el = document.createElement("div");
    el.id = "cr-toast";
    el.className = "toast align-items-center text-bg-dark border-0 position-fixed bottom-0 end-0 m-3";
    el.setAttribute("role", "alert");
    el.innerHTML = `<div class="d-flex"><div class="toast-body"></div>
      <button type="button" class="btn-close btn-close-white me-2 m-auto" data-bs-dismiss="toast"></button></div>`;
    document.body.appendChild(el);
  }
  el.querySelector(".toast-body").textContent = message;
  el.className =
    "toast align-items-center border-0 position-fixed bottom-0 end-0 m-3 " +
    (type === "danger" ? "text-bg-danger" : type === "success" ? "text-bg-success" : "text-bg-dark");
  bootstrap.Toast.getOrCreateInstance(el, { delay: 2200 }).show();
};

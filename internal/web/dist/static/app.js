const $ = (id) => document.getElementById(id);

const state = {
  nodes: [],
  nodeQuery: "",
  ruleQuery: "",
  rulePage: 1,
  pageSize: 25,
  expanded: new Set(),
  updateAvailable: false,
  ipVisible: false,
  panel: null,
};

const ROUTES = new Set(["overview", "nodes", "rules", "panel", "update"]);

async function getJSON(url) {
  const res = await fetch(url, { credentials: "same-origin" });
  if (res.status === 401) {
    location.href = "/login";
    throw new Error("unauthorized");
  }
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    try {
      const data = await res.json();
      if (data.error) msg = data.error;
    } catch (_) {
      // 忽略非 JSON 响应
    }
    throw new Error(msg);
  }
  return res.json();
}

async function postJSON(url, body) {
  const res = await fetch(url, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body || {}),
  });
  if (res.status === 401) {
    location.href = "/login";
    throw new Error("unauthorized");
  }
  let data = {};
  try {
    data = await res.json();
  } catch (_) {
    // 忽略非 JSON 响应
  }
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

function fmtDuration(seconds) {
  const s = Math.max(0, Math.round(Number(seconds) || 0));
  if (s < 60) return `${s} 秒`;
  if (s < 3600) return `${Math.floor(s / 60)} 分钟`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时 ${Math.floor((s % 3600) / 60)} 分钟`;
  return `${Math.floor(s / 86400)} 天 ${Math.floor((s % 86400) / 3600)} 小时`;
}

function humanBytes(n) {
  const v = Number(n) || 0;
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let i = 0;
  let num = v;
  while (num >= 1024 && i < units.length - 1) {
    num /= 1024;
    i++;
  }
  return `${num >= 100 || i === 0 ? num.toFixed(0) : num.toFixed(2)} ${units[i]}`;
}

function cell(text, mono) {
  const td = document.createElement("td");
  td.textContent = text;
  if (mono) td.className = "mono";
  return td;
}

function badge(text, cls) {
  const span = document.createElement("span");
  span.className = "badge" + (cls ? " " + cls : "");
  span.textContent = text;
  return span;
}

function ruleBadge(st, message) {
  const map = {
    active: ["运行中", "ok"],
    error: ["错误", "err"],
    pending: ["待确认", "warn"],
    offline: ["离线", "offline"],
  };
  const [text, cls] = map[st] || [st || "-", ""];
  const span = badge(text, cls);
  if (message) span.title = message;
  return span;
}

function emptyRow(tbody, cols, text) {
  const tr = document.createElement("tr");
  const td = document.createElement("td");
  td.colSpan = cols;
  td.className = "empty";
  td.textContent = text;
  tr.appendChild(td);
  tbody.appendChild(tr);
}

function setGauge(id, pct) {
  const value = Math.max(0, Math.min(100, Number(pct) || 0));
  $(id).style.setProperty("--p", value.toFixed(1));
  $(id + "-val").textContent = `${value.toFixed(value >= 10 ? 0 : 2)}%`;
}

function renderSystem(sys) {
  setGauge("g-cpu", sys.cpu_percent);
  setGauge("g-mem", sys.mem_percent);
  setGauge("g-swap", sys.swap_percent);
  setGauge("g-disk", sys.disk_percent);

  $("ov-os-uptime").textContent = fmtDuration(sys.uptime_seconds);
  $("ov-rss").textContent = humanBytes(sys.process_rss);
  $("ov-threads").textContent = sys.threads || "-";
  $("ov-goroutines").textContent = sys.goroutines || "-";
  $("ov-up").textContent = `${humanBytes(sys.net_up_bps)}/s`;
  $("ov-down").textContent = `${humanBytes(sys.net_down_bps)}/s`;
  $("ov-sent").textContent = humanBytes(sys.net_sent);
  $("ov-recv").textContent = humanBytes(sys.net_recv);
  $("ov-tcp").textContent = sys.tcp_count;
  $("ov-udp").textContent = sys.udp_count;

  const mask = (list) => {
    if (!list || !list.length) return "-";
    if (!state.ipVisible) return list.map(() => "••••••").join("、");
    return list.join("、");
  };
  $("ov-ipv4").textContent = mask(sys.ipv4);
  $("ov-ipv6").textContent = mask(sys.ipv6);
}

function renderStatus(s) {
  $("uptime").textContent = `已运行 ${fmtDuration(s.uptime_seconds)}`;
  $("side-version").textContent = s.version ? `v${s.version}` : "mtun";
  $("ov-nodes").textContent = s.nodes;
  $("ov-rules").textContent = s.publish_rules;
  $("ov-version").textContent = s.version || "-";
  $("ov-uptime").textContent = fmtDuration(s.uptime_seconds);
}

function renderPanelInfo(p) {
  state.panel = p;
  if (p.page_size > 0) state.pageSize = p.page_size;
  $("ov-panel-addr").textContent = p.dashboard || "-";
  $("ov-session").textContent = `${p.session_minutes} 分钟`;
  $("ov-pagesize").textContent = `${p.page_size} 条`;
  $("p-dashboard").value = p.dashboard || "";
  $("p-session").value = p.session_minutes;
  $("p-pagesize").value = p.page_size;
  $("panel-path").textContent = p.config_path || "";
  $("panel-config").textContent = p.config_path || "-";
  const addr = (p.dashboard || "").toLowerCase();
  const local = !addr || addr.startsWith("127.") || addr.startsWith("localhost") || addr.startsWith("[::1]") || addr.startsWith("::1");
  $("panel-scope").textContent = local ? "仅本机（回环地址）" : "可从外部网络访问";
}

async function loadPanel() {
  try {
    renderPanelInfo(await getJSON("/api/panel"));
  } catch (err) {
    $("panel-msg").textContent = `读取面板设置失败：${err.message}`;
  }
}

function nodeDetail(node) {
  const box = document.createElement("div");
  box.className = "detail";

  const info = document.createElement("div");
  info.className = "muted";
  const endpoints = (node.endpoints || []).join(", ") || "-";
  info.textContent =
    `覆盖 IP ${node.overlay_ip} · 路径 ${node.path === "direct" ? "直连" : "中继"} · ` +
    `最后心跳 ${fmtDuration(node.last_seen_seconds)} · 端点 ${endpoints}`;
  box.appendChild(info);

  const rules = node.rules || [];
  if (!rules.length) {
    const empty = document.createElement("div");
    empty.className = "muted";
    empty.textContent = "该节点暂无转发规则";
    box.appendChild(empty);
    return box;
  }

  const table = document.createElement("table");
  const thead = document.createElement("thead");
  const headRow = document.createElement("tr");
  for (const title of ["规则", "协议", "公网端口", "内网目标", "状态"]) {
    const th = document.createElement("th");
    th.textContent = title;
    headRow.appendChild(th);
  }
  thead.appendChild(headRow);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  for (const rule of rules) {
    const tr = document.createElement("tr");
    tr.appendChild(cell(rule.name || "-"));
    tr.appendChild(cell(rule.proto || "tcp"));
    tr.appendChild(cell(String(rule.listen || 0), true));
    tr.appendChild(cell(rule.target || "-", true));
    const statusTd = document.createElement("td");
    statusTd.appendChild(ruleBadge(rule.state, rule.message));
    if (rule.message) {
      const msg = document.createElement("span");
      msg.className = "rule-msg";
      msg.textContent = rule.message;
      statusTd.appendChild(msg);
    }
    tr.appendChild(statusTd);
    tbody.appendChild(tr);
  }
  table.appendChild(tbody);
  box.appendChild(table);
  return box;
}

function renderNodes() {
  const tbody = $("node-rows");
  tbody.replaceChildren();
  const q = state.nodeQuery.trim().toLowerCase();
  const list = state.nodes.filter((n) => {
    if (!q) return true;
    const hay = [n.id, n.overlay_ip, n.path, (n.endpoints || []).join(" ")].join(" ").toLowerCase();
    return hay.includes(q);
  });
  $("node-count").textContent = `${list.length} / ${state.nodes.length} 个节点`;

  if (!list.length) {
    emptyRow(tbody, 6, state.nodes.length ? "没有匹配的节点" : "还没有节点注册");
    return;
  }

  for (const node of list) {
    const tr = document.createElement("tr");
    const online = node.online !== false;
    const pathLabel = !online ? "离线" : node.path === "direct" ? "直连" : "中继";

    const name = document.createElement("td");
    name.appendChild(document.createTextNode(node.id + " "));
    name.appendChild(badge(online ? "在线" : "离线", online ? "ok" : "offline"));

    const toggle = document.createElement("button");
    toggle.type = "button";
    toggle.className = "btn-link";
    toggle.textContent = state.expanded.has(node.id) ? "收起" : "详情";
    toggle.addEventListener("click", () => {
      if (state.expanded.has(node.id)) state.expanded.delete(node.id);
      else state.expanded.add(node.id);
      renderNodes();
    });
    name.appendChild(toggle);

    tr.appendChild(name);
    tr.appendChild(cell(node.overlay_ip, true));
    tr.appendChild(cell(pathLabel));
    tr.appendChild(cell(String(node.publish)));
    tr.appendChild(cell(fmtDuration(node.last_seen_seconds)));
    tr.appendChild(cell((node.endpoints || []).join(", "), true));
    tbody.appendChild(tr);

    if (state.expanded.has(node.id)) {
      const detailRow = document.createElement("tr");
      detailRow.className = "detail-row";
      const detailCell = document.createElement("td");
      detailCell.colSpan = 6;
      detailCell.appendChild(nodeDetail(node));
      detailRow.appendChild(detailCell);
      tbody.appendChild(detailRow);
    }
  }
}

function renderRules() {
  const tbody = $("rule-rows");
  tbody.replaceChildren();

  const q = state.ruleQuery.trim().toLowerCase();
  const rows = [];
  for (const node of state.nodes) {
    for (const rule of node.rules || []) {
      const hay = [node.id, rule.name, rule.proto, rule.listen, rule.target].join(" ").toLowerCase();
      if (!q || hay.includes(q)) rows.push({ node, rule });
    }
  }

  const size = Math.max(5, state.pageSize || 25);
  const pages = Math.max(1, Math.ceil(rows.length / size));
  if (state.rulePage > pages) state.rulePage = pages;
  if (state.rulePage < 1) state.rulePage = 1;
  const start = (state.rulePage - 1) * size;
  const pageRows = rows.slice(start, start + size);

  $("rule-count").textContent = `${rows.length} 条规则`;
  $("rule-page").textContent = `${state.rulePage} / ${pages} 页`;
  $("rule-prev").disabled = state.rulePage <= 1;
  $("rule-next").disabled = state.rulePage >= pages;

  if (!pageRows.length) {
    emptyRow(tbody, 6, "暂无匹配的转发规则");
    return;
  }

  for (const { node, rule } of pageRows) {
    const tr = document.createElement("tr");
    tr.appendChild(cell(node.id));
    tr.appendChild(cell(rule.name || "-"));
    tr.appendChild(cell(rule.proto || "tcp"));
    tr.appendChild(cell(String(rule.listen || 0), true));
    tr.appendChild(cell(rule.target || "-", true));
    const statusTd = document.createElement("td");
    statusTd.appendChild(ruleBadge(rule.state, rule.message));
    if (rule.message) {
      const msg = document.createElement("span");
      msg.className = "rule-msg";
      msg.textContent = rule.message;
      statusTd.appendChild(msg);
    }
    tr.appendChild(statusTd);
    tbody.appendChild(tr);
  }
}

let updateLoading = false;

async function loadUpdate() {
  if (updateLoading) return;
  updateLoading = true;
  try {
    const res = await fetch("/api/update", { credentials: "same-origin" });
    const data = await res.json().catch(() => ({}));
    $("update-current").textContent = data.current ? `当前版本 ${data.current}` : "-";
    if (!res.ok) {
      $("update-status").textContent = data.error || "检查更新失败";
      $("update-notes").textContent = "";
      state.updateAvailable = false;
      $("btn-update").disabled = true;
      return;
    }
    $("update-status").textContent = data.available
      ? `发现新版本 ${data.latest}（来源 ${data.repo}）`
      : "已是最新版本";
    $("update-notes").textContent = data.notes || "";
    state.updateAvailable = !!data.available;
    $("btn-update").disabled = !state.updateAvailable;
  } catch (err) {
    $("update-status").textContent = "检查更新失败";
  } finally {
    updateLoading = false;
  }
}

async function applyUpdate() {
  if (!state.updateAvailable) return;
  if (!confirm("确定更新并重启服务？只会替换程序，配置文件与节点数据不会丢失。")) return;
  $("btn-update").disabled = true;
  $("update-status").textContent = "正在下载并替换程序...";
  try {
    const res = await fetch("/api/update", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: "{}",
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      $("update-status").textContent = data.error || "更新失败";
      $("btn-update").disabled = false;
      return;
    }
    if (!data.updated) {
      $("update-status").textContent = "已是最新版本";
      state.updateAvailable = false;
      return;
    }
    $("update-status").textContent = `已更新到 ${data.to}，服务正在重启...`;
    setTimeout(() => location.reload(), 5000);
  } catch (err) {
    $("update-status").textContent = "更新请求失败";
    $("btn-update").disabled = false;
  }
}

async function restartPanel() {
  if (!confirm("确定重启面板？节点连接不受影响。")) return;
  try {
    await postJSON("/api/panel/restart");
    setPanelMsg("面板正在重启，稍后需要重新登录...", true);
    setTimeout(() => {
      location.href = "/login";
    }, 4000);
  } catch (err) {
    setPanelMsg(err.message);
  }
}

function setPanelMsg(text, ok) {
  const el = $("panel-msg");
  el.textContent = text;
  el.className = "form-msg" + (ok ? " ok" : "");
}

$("panel-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  setPanelMsg("");
  try {
    const data = await postJSON("/api/panel", {
      dashboard: $("p-dashboard").value.trim(),
      session_minutes: Number($("p-session").value),
      page_size: Number($("p-pagesize").value),
    });
    state.pageSize = Number($("p-pagesize").value);
    const p = data.restart_required ? "已保存。监听地址与会话时长需重启面板后生效。" : "已保存。";
    setPanelMsg(p, true);
    await loadPanel();
    renderRules();
  } catch (err) {
    setPanelMsg(err.message);
  }
});

function navigate() {
  const name = location.hash.replace(/^#\/?/, "") || "overview";
  const page = ROUTES.has(name) ? name : "overview";
  document.querySelectorAll(".page").forEach((el) => {
    el.hidden = el.id !== "page-" + page;
  });
  document.querySelectorAll(".nav a").forEach((a) => {
    a.classList.toggle("active", a.dataset.page === page);
  });
  if (page === "update") loadUpdate();
  if (page === "panel") loadPanel();
}

async function refresh() {
  const results = await Promise.allSettled([
    getJSON("/api/status"),
    getJSON("/api/nodes"),
    getJSON("/api/system"),
  ]);
  const [status, nodes, sys] = results;
  if (status.status === "fulfilled") renderStatus(status.value);
  if (nodes.status === "fulfilled") {
    state.nodes = nodes.value || [];
    renderNodes();
    renderRules();
  }
  if (sys.status === "fulfilled") renderSystem(sys.value);

  const failed = results.filter((r) => r.status === "rejected");
  failed.forEach((r) => console.warn("接口不可用:", r.reason));
  if (failed.length === results.length) {
    $("uptime").textContent = "控制台连接中断";
  } else if (failed.length > 0) {
    $("uptime").textContent = "部分数据不可用";
  }
}

$("node-search").addEventListener("input", (e) => {
  state.nodeQuery = e.target.value;
  renderNodes();
});
$("rule-search").addEventListener("input", (e) => {
  state.ruleQuery = e.target.value;
  state.rulePage = 1;
  renderRules();
});
$("rule-prev").addEventListener("click", () => {
  state.rulePage -= 1;
  renderRules();
});
$("rule-next").addEventListener("click", () => {
  state.rulePage += 1;
  renderRules();
});
$("btn-check").addEventListener("click", loadUpdate);
$("btn-update").addEventListener("click", applyUpdate);
$("btn-restart").addEventListener("click", restartPanel);
$("btn-toggle-ip").addEventListener("click", () => {
  state.ipVisible = !state.ipVisible;
  $("btn-toggle-ip").textContent = state.ipVisible ? "隐藏" : "显示";
  refresh();
});
window.addEventListener("hashchange", navigate);

navigate();
loadPanel();
refresh();
setInterval(refresh, 3000);

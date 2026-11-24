/* Copyright (c) 2026 song08 | SPDX-License-Identifier: MIT */

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
  autostart: null,
  pinSet: false,
  ruleTarget: null,
  ruleEditing: null,
  accessTarget: null,
  passwordTarget: null,
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
  if (!res.ok) {
    const err = new Error(data.error || `HTTP ${res.status}`);
    err.status = res.status;
    err.data = data;
    throw err;
  }
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

function visibilityText(rule) {
  const mode = rule.visibility || "all";
  if (mode === "self") return "仅自己";
  if (mode === "nodes") {
    const ids = rule.visible_to || [];
    return ids.length ? `指定节点（${ids.join("、")}）` : "指定节点（无）";
  }
  return "所有人";
}

// changeVisibility 让管理员修改任意节点规则的可见范围，服务端随后下发到该客户端落盘。
async function changeVisibility(nodeID, rule) {
  const mode = rule.visibility || "all";
  const preset = mode === "nodes" ? `nodes:${(rule.visible_to || []).join(",")}` : mode;
  const input = prompt(
    `设置规则「${rule.name}」的可见范围：\n` +
      "all = 所有客户端可见\n" +
      "self = 仅规则所属客户端可见\n" +
      "nodes:节点1,节点2 = 仅指定节点可见",
    preset
  );
  if (input === null) return;
  let visibility = input.trim().toLowerCase();
  let visibleTo = [];
  if (visibility.startsWith("nodes:")) {
    visibility = "nodes";
    visibleTo = input.split(":").slice(1).join(":").split(",").map((s) => s.trim()).filter(Boolean);
  }
  if (!["all", "self", "nodes"].includes(visibility)) {
    alert("可见范围只能是 all / self / nodes");
    return;
  }
  if (visibility === "nodes" && !visibleTo.length) {
    alert("nodes 模式需要至少一个节点 ID");
    return;
  }
  try {
    await postJSON("/api/rules/visibility", {
      node: nodeID,
      name: rule.name,
      visibility,
      visible_to: visibleTo,
    });
    await refresh();
  } catch (err) {
    alert("修改可见范围失败：" + err.message);
  }
}

function setMsg(id, text, ok) {
  const el = $(id);
  el.textContent = text || "";
  el.className = "form-msg" + (ok ? " ok" : "");
}

function splitLines(value) {
  return String(value || "")
    .split(/[\n,，]/)
    .map((s) => s.trim())
    .filter(Boolean);
}

function openRuleDialog(nodeID, rule) {
  state.ruleTarget = nodeID;
  state.ruleEditing = rule ? rule.name : null;
  $("rule-dialog-title").textContent = rule ? "编辑规则" : "新增规则";
  $("r-node").value = nodeID;
  $("r-name").value = rule ? rule.name : "";
  $("r-name").readOnly = !!rule;
  $("r-proto").value = rule ? (rule.proto || "tcp") : "tcp";
  $("r-listen").value = rule ? rule.listen : "";
  $("r-target").value = rule ? rule.target : "";
  $("r-visibility").value = rule ? (rule.visibility || "all") : "all";
  $("r-visible-to").value = rule && rule.visible_to ? rule.visible_to.join(",") : "";
  setMsg("rule-msg", "");
  $("rule-dialog").showModal();
}

async function submitRule(event) {
  event.preventDefault();
  const rule = {
    name: $("r-name").value.trim(),
    proto: $("r-proto").value,
    listen: Number($("r-listen").value),
    target: $("r-target").value.trim(),
    visibility: $("r-visibility").value,
    visible_to: splitLines($("r-visible-to").value),
  };
  try {
    const data = await postJSON("/api/nodes/rules", {
      node: state.ruleTarget,
      op: state.ruleEditing ? "update" : "add",
      rule,
    });
    $("rule-dialog").close();
    await refresh();
    if (!data.online) alert("节点当前离线，规则将在其下次上线时自动同步");
  } catch (err) {
    setMsg("rule-msg", err.message);
  }
}

async function deleteRule(nodeID, name) {
  if (!confirm(`确定删除规则「${name}」？`)) return;
  try {
    const data = await postJSON("/api/nodes/rules", { node: nodeID, op: "delete", rule: { name } });
    await refresh();
    if (!data.online) alert("节点当前离线，删除操作将在其下次上线时自动同步");
  } catch (err) {
    alert("删除失败：" + err.message);
  }
}

function openAccessDialog(node) {
  state.accessTarget = node.id;
  const p = node.access || {};
  $("a-node").value = node.id;
  $("a-ban").value = 0;
  $("a-permanent").checked = !!p.permanent;
  const w = (p.daily || [])[0] || {};
  $("a-start").value = w.start || "";
  $("a-end").value = w.end || "";
  $("a-allow").value = (p.allow_cidrs || []).join("\n");
  $("a-deny").value = (p.deny_cidrs || []).join("\n");
  document.querySelectorAll(".a-os").forEach((cb) => {
    cb.checked = (p.allow_os || []).includes(cb.value);
  });
  setMsg("access-msg", "");
  $("access-dialog").showModal();
}

async function submitAccess(event) {
  event.preventDefault();
  const daily = [];
  const start = $("a-start").value;
  const end = $("a-end").value;
  if (start && end) daily.push({ start, end });
  const body = {
    id: state.accessTarget,
    ban_seconds: Math.round(Number($("a-ban").value || 0) * 60),
    permanent: $("a-permanent").checked,
    daily,
    allow_cidrs: splitLines($("a-allow").value),
    deny_cidrs: splitLines($("a-deny").value),
    allow_os: Array.from(document.querySelectorAll(".a-os:checked")).map((cb) => cb.value),
  };
  try {
    await postJSON("/api/nodes/access", body);
    $("access-dialog").close();
    await refresh();
  } catch (err) {
    setMsg("access-msg", err.message);
  }
}

async function unbanNode() {
  if (!state.accessTarget) return;
  try {
    await postJSON("/api/nodes/unban", { id: state.accessTarget });
    $("access-dialog").close();
    await refresh();
  } catch (err) {
    setMsg("access-msg", err.message);
  }
}

// ==== 节点独立接入密码 ====

function openPasswordDialog(node) {
  state.passwordTarget = node.id;
  $("p-node").value = node.id;
  $("p-value").value = "";
  $("p-pin").value = "";
  $("p-status").textContent = node.has_password
    ? "该节点已设置独立接入密码（出于安全考虑不显示，仅保存哈希）"
    : "该节点未设置独立密码，当前使用默认密码接入";
  setMsg("password-msg", "");
  $("password-dialog").showModal();
}

function applyPasswordResult(data) {
  if (data.set && data.password) {
    $("p-value").value = data.password;
    $("p-status").textContent = "已设置独立接入密码，请立即复制下方密码交给该节点用户（关闭后无法再次查看）";
  } else if (data.set) {
    $("p-value").value = "";
    $("p-status").textContent = "已设置独立接入密码（出于安全考虑，密码不再显示）";
  } else {
    $("p-value").value = "";
    $("p-status").textContent = "该节点未设置独立密码，当前使用默认密码接入";
  }
}

async function nodePasswordAction(action, password) {
  if (!state.passwordTarget) return null;
  const pin = $("p-pin").value.trim();
  if (!pin) {
    setMsg("password-msg", "请输入 6 位 PIN");
    return null;
  }
  const body = { pin, node: state.passwordTarget, action };
  if (password !== undefined) body.password = password;
  try {
    return await postJSON("/api/nodes/password", body);
  } catch (err) {
    setMsg("password-msg", err.message);
    return null;
  }
}

async function randomNodePassword() {
  const data = await nodePasswordAction("random");
  if (!data) return;
  applyPasswordResult(data);
  setMsg("password-msg", "已生成新密码，请立即复制；关闭窗口后无法再次查看", true);
  await refresh();
}

async function clearNodePassword() {
  if (!confirm("确定清除该节点的独立接入密码？清除后将回落到默认密码策略。")) return;
  const data = await nodePasswordAction("clear");
  if (!data) return;
  applyPasswordResult(data);
  setMsg("password-msg", "已清除独立密码", true);
  await refresh();
}

async function submitPassword(event) {
  event.preventDefault();
  const password = $("p-value").value.trim();
  if (!password) {
    setMsg("password-msg", "请输入密码，或点“随机生成”");
    return;
  }
  const data = await nodePasswordAction("set", password);
  if (!data) return;
  applyPasswordResult(data);
  setMsg("password-msg", "已保存，节点下次连接时生效", true);
  await refresh();
}

async function loadDefaultPassword() {
  try {
    const data = await getJSON("/api/nodes/password/default");
    $("allow-default-password").checked = !!data.allow_default;
  } catch (_) {
    /* 忽略：下次刷新重试 */
  }
}

async function toggleDefaultPassword(event) {
  const allow = event.target.checked;
  const input = prompt(
    allow
      ? "开启后，未设置独立密码的节点可用默认密码接入。\n请输入 6 位 PIN："
      : "关闭后，未设置独立密码的节点将无法接入。\n请输入 6 位 PIN："
  );
  if (input === null) {
    event.target.checked = !allow;
    return;
  }
  try {
    await postJSON("/api/nodes/password/default", { pin: input.trim(), allow });
    await refresh();
  } catch (err) {
    event.target.checked = !allow;
    alert("切换失败：" + err.message);
  }
}

async function submitPIN(event) {
  event.preventDefault();
  const body = { pin: $("pin-new").value.trim(), confirm: $("pin-confirm").value.trim() };
  if (state.pinSet) body.current_pin = $("pin-current").value.trim();
  try {
    await postJSON("/api/pin", body);
    $("pin-new").value = "";
    $("pin-confirm").value = "";
    $("pin-current").value = "";
    setMsg("pin-msg", "PIN 已保存", true);
    await loadPanel();
  } catch (err) {
    setMsg("pin-msg", err.message);
  }
}

async function submitSecure(event) {
  event.preventDefault();
  const body = { pin: $("s-pin").value.trim() };
  const dash = $("s-dashboard").value.trim();
  const control = $("s-control").value.trim();
  const pass = $("s-admin-pass").value;
  const join = $("s-join-token").value;
  if (dash) body.dashboard = dash;
  if (control) body.control = control;
  if (pass) body.admin_password = pass;
  if (join) body.join_token = join;
  if (!dash && !control && !pass && !join) {
    setMsg("secure-msg", "没有需要修改的项");
    return;
  }
  try {
    const data = await postJSON("/api/panel/secure", body);
    $("s-admin-pass").value = "";
    $("s-join-token").value = "";
    $("s-pin").value = "";
    if (data.restarting) {
      setMsg("secure-msg", "已保存，服务端正在重启，约 5 秒后自动刷新...", true);
      setTimeout(() => location.reload(), 6000);
      return;
    }
    setMsg("secure-msg", data.relogin ? "已保存，请重新登录" : "已保存", true);
    if (data.relogin) setTimeout(() => (location.href = "/login"), 1500);
    await loadPanel();
  } catch (err) {
    if (err.data && err.data.need_setup) {
      setMsg("secure-msg", "请先在安全设置中设置 PIN");
      return;
    }
    setMsg("secure-msg", err.message);
  }
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
  state.pinSet = !!p.pin_set;
  $("ov-panel-addr").textContent = p.dashboard || "-";
  $("ov-session").textContent = `${p.session_minutes} 分钟`;
  $("ov-pagesize").textContent = `${p.page_size} 条`;
  $("s-dashboard").value = p.dashboard || "";
  $("s-control").value = p.control || "";
  $("p-session").value = p.session_minutes;
  $("p-pagesize").value = p.page_size;
  $("panel-path").textContent = p.config_path || "";
  $("panel-config").textContent = p.config_path || "-";
  $("pin-status").textContent = state.pinSet ? "已设置 PIN" : "尚未设置 PIN";
  $("pin-current-label").hidden = !state.pinSet;
  $("btn-pin-save").textContent = state.pinSet ? "修改 PIN" : "设置 PIN";
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
  const parts = [
    `覆盖 IP ${node.overlay_ip}`,
    `系统 ${node.os || "未上报"}`,
    `来源 IP ${node.ip || "-"}`,
    `路径 ${node.path === "direct" ? "直连" : "中继"}`,
    `最后心跳 ${fmtDuration(node.last_seen_seconds)}`,
    `端点 ${endpoints}`,
  ];
  if (node.queued_rules) parts.push(`待同步 ${node.queued_rules} 条规则`);
  if (node.banned) parts.push(node.ban_until === "permanent" ? "已永久封禁" : `封禁至 ${node.ban_until}`);
  parts.push(node.has_password ? "独立接入密码已设置" : "使用默认接入密码");
  info.textContent = parts.join(" · ");
  box.appendChild(info);

  const toolbar = document.createElement("div");
  toolbar.className = "detail-toolbar";
  const addBtn = document.createElement("button");
  addBtn.type = "button";
  addBtn.className = "btn-link";
  addBtn.textContent = "新增规则";
  addBtn.addEventListener("click", () => openRuleDialog(node.id, null));
  const accessBtn = document.createElement("button");
  accessBtn.type = "button";
  accessBtn.className = "btn-link";
  accessBtn.textContent = "访问策略";
  accessBtn.addEventListener("click", () => openAccessDialog(node));
  const passBtn = document.createElement("button");
  passBtn.type = "button";
  passBtn.className = "btn-link";
  passBtn.textContent = "接入密码";
  passBtn.addEventListener("click", () => openPasswordDialog(node));
  toolbar.appendChild(addBtn);
  toolbar.appendChild(accessBtn);
  toolbar.appendChild(passBtn);
  box.appendChild(toolbar);

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
  for (const title of ["规则", "协议", "公网端口", "内网目标", "可见范围", "状态", "操作"]) {
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
    tr.appendChild(cell(visibilityText(rule)));
    const statusTd = document.createElement("td");
    statusTd.appendChild(ruleBadge(rule.state, rule.message));
    if (rule.message) {
      const msg = document.createElement("span");
      msg.className = "rule-msg";
      msg.textContent = rule.message;
      statusTd.appendChild(msg);
    }
    tr.appendChild(statusTd);

    const opTd = document.createElement("td");
    const editBtn = document.createElement("button");
    editBtn.type = "button";
    editBtn.className = "btn-link";
    editBtn.textContent = "编辑";
    editBtn.addEventListener("click", () => openRuleDialog(node.id, rule));
    const delBtn = document.createElement("button");
    delBtn.type = "button";
    delBtn.className = "btn-link";
    delBtn.textContent = "删除";
    delBtn.addEventListener("click", () => deleteRule(node.id, rule.name));
    opTd.appendChild(editBtn);
    opTd.appendChild(delBtn);
    tr.appendChild(opTd);
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
    if (node.banned) name.appendChild(badge("已封禁", "err"));

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
      const hay = [node.id, rule.name, rule.proto, rule.listen, rule.target, visibilityText(rule)]
        .join(" ")
        .toLowerCase();
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
    emptyRow(tbody, 8, "暂无匹配的转发规则");
    return;
  }

  for (const { node, rule } of pageRows) {
    const tr = document.createElement("tr");
    tr.appendChild(cell(node.id));
    tr.appendChild(cell(rule.name || "-"));
    tr.appendChild(cell(rule.proto || "tcp"));
    tr.appendChild(cell(String(rule.listen || 0), true));
    tr.appendChild(cell(rule.target || "-", true));
    tr.appendChild(cell(visibilityText(rule)));
    const statusTd = document.createElement("td");
    statusTd.appendChild(ruleBadge(rule.state, rule.message));
    if (rule.message) {
      const msg = document.createElement("span");
      msg.className = "rule-msg";
      msg.textContent = rule.message;
      statusTd.appendChild(msg);
    }
    tr.appendChild(statusTd);

    const opTd = document.createElement("td");
    const editBtn = document.createElement("button");
    editBtn.type = "button";
    editBtn.className = "btn-link";
    editBtn.textContent = "编辑";
    editBtn.addEventListener("click", () => openRuleDialog(node.id, rule));
    const visBtn = document.createElement("button");
    visBtn.type = "button";
    visBtn.className = "btn-link";
    visBtn.textContent = "修改可见";
    visBtn.addEventListener("click", () => changeVisibility(node.id, rule));
    const delBtn = document.createElement("button");
    delBtn.type = "button";
    delBtn.className = "btn-link";
    delBtn.textContent = "删除";
    delBtn.addEventListener("click", () => deleteRule(node.id, rule.name));
    opTd.appendChild(editBtn);
    opTd.appendChild(visBtn);
    opTd.appendChild(delBtn);
    tr.appendChild(opTd);
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
    await postJSON("/api/panel", {
      session_minutes: Number($("p-session").value),
      page_size: Number($("p-pagesize").value),
    });
    state.pageSize = Number($("p-pagesize").value);
    setPanelMsg("已保存。", true);
    await loadPanel();
    renderRules();
  } catch (err) {
    setPanelMsg(err.message);
  }
});

let autostartLoading = false;

function renderAutostart() {
  const s = state.autostart;
  if (!s) return;
  const mode = {
    scheduled_task: "计划任务", task: "计划任务",
    nssm: "NSSM 系统服务", service: "Windows 服务",
    startup_folder: "启动文件夹", startup: "启动文件夹", startup_link: "旧版启动项",
    systemd: "systemd 服务",
  }[s.mode] || "";
  const trigger = {
    startup: "开机后，无需登录", boot: "开机后，无需登录", at_startup: "开机后，无需登录", atstartup: "开机后，无需登录",
    logon: "当前用户登录后", at_logon: "当前用户登录后", atlogon: "当前用户登录后",
    at_system_startup: "系统启动后",
  }[String(s.trigger || "").toLowerCase()] || "";
  $("autostart-status").textContent = !s.supported ? "当前系统不可用" : (s.conflict ? "配置冲突" : (s.enabled ? "已启用" : "未启用"));
  const detail = s.enabled ? [mode, trigger, s.message] : [s.message];
  $("autostart-detail").textContent = detail.filter(Boolean).join(" · ") || "只影响下次启动，不会停止当前服务";
  $("btn-autostart").textContent = autostartLoading ? "处理中..." : (s.enabled ? "禁用自启动" : "启用自启动");
  $("btn-autostart").disabled = autostartLoading || !s.supported;
}

async function loadAutostart() {
  if (autostartLoading) return;
  autostartLoading = true;
  $("autostart-msg").className = "form-msg";
  renderAutostart();
  try {
    state.autostart = await getJSON("/api/autostart");
    $("autostart-msg").textContent = "";
  } catch (err) {
    $("autostart-status").textContent = "读取失败";
    $("autostart-msg").textContent = err.message;
  } finally {
    autostartLoading = false;
    renderAutostart();
  }
}

async function toggleAutostart() {
  if (!state.autostart || autostartLoading) return;
  const enabled = !state.autostart.enabled;
  autostartLoading = true;
  $("autostart-msg").className = "form-msg";
  $("autostart-msg").textContent = "";
  renderAutostart();
  try {
    state.autostart = await postJSON("/api/autostart", { enabled });
    $("autostart-msg").className = "form-msg ok";
    $("autostart-msg").textContent = enabled ? "已启用开机自启动" : "已禁用开机自启动，当前服务保持运行";
  } catch (err) {
    $("autostart-msg").textContent = err.message;
  } finally {
    autostartLoading = false;
    renderAutostart();
  }
}

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
  if (page === "panel") {
    loadPanel();
    loadAutostart();
  }
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
    loadDefaultPassword();
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
$("btn-autostart").addEventListener("click", toggleAutostart);
$("pin-form").addEventListener("submit", submitPIN);
$("secure-form").addEventListener("submit", submitSecure);
$("rule-form").addEventListener("submit", submitRule);
$("access-form").addEventListener("submit", submitAccess);
$("a-unban").addEventListener("click", unbanNode);
$("password-form").addEventListener("submit", submitPassword);
$("p-random").addEventListener("click", randomNodePassword);
$("p-clear").addEventListener("click", clearNodePassword);
$("allow-default-password").addEventListener("change", toggleDefaultPassword);
document.querySelectorAll("dialog [data-close]").forEach((btn) => {
  btn.addEventListener("click", () => btn.closest("dialog").close());
});
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

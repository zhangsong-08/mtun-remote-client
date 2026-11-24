/* Copyright (c) 2026 song08 | SPDX-License-Identifier: MIT */

const $ = (id) => document.getElementById(id);

const state = {
  status: null,
  ruleQuery: "",
  peerQuery: "",
  updateAvailable: false,
  serverPrefilled: false,
  autostart: null,
};

const ROUTES = new Set(["overview", "server", "rules", "peers", "update"]);

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
    body: JSON.stringify(body),
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
  const s = Math.max(0, Number(seconds) || 0);
  if (s < 60) return `${s} 秒`;
  if (s < 3600) return `${Math.floor(s / 60)} 分钟`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时`;
  return `${Math.floor(s / 86400)} 天`;
}

function cell(text, mono) {
  const td = document.createElement("td");
  td.textContent = text;
  if (mono) td.className = "mono";
  return td;
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

const STATE_TEXT = {
  active: ["运行中", "ok"],
  error: ["错误", "err"],
  pending: ["待确认", "warn"],
  offline: ["离线", "offline"],
};

function visibilityText(rule) {
  const mode = rule.visibility || "all";
  if (mode === "self") return "仅自己";
  if (mode === "nodes") {
    const ids = rule.visible_to || [];
    return ids.length ? `指定节点（${ids.join("、")}）` : "指定节点（无）";
  }
  return "所有人";
}

// changeRuleVisibility 修改本机规则的可见范围；修改后客户端会重连刷新各节点的网络页。
async function changeRuleVisibility(rule) {
  const mode = rule.visibility || "all";
  const preset = mode === "nodes" ? `nodes:${(rule.visible_to || []).join(",")}` : mode;
  const input = prompt(
    `设置规则「${rule.name}」的可见范围：\n` +
      "all = 所有客户端可见\n" +
      "self = 仅自己可见\n" +
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
      name: rule.name,
      visibility,
      visible_to: visibleTo,
    });
    await refresh();
  } catch (err) {
    alert("修改可见范围失败：" + err.message);
  }
}

function renderRules(rules) {
  const tbody = $("rows");
  tbody.replaceChildren();
  const q = state.ruleQuery.trim().toLowerCase();
  const list = (rules || []).filter((rule) => {
    if (!q) return true;
    return [rule.name, rule.proto, rule.listen, rule.target, visibilityText(rule)]
      .join(" ")
      .toLowerCase()
      .includes(q);
  });
  $("rule-count").textContent = `${list.length} / ${(rules || []).length} 条规则`;

  if (!list.length) {
    emptyRow(tbody, 7, (rules || []).length ? "没有匹配的规则" : "还没有转发规则，添加后服务端会监听对应公网端口");
    return;
  }

  for (const rule of list) {
    const tr = document.createElement("tr");
    tr.appendChild(cell(rule.name));
    tr.appendChild(cell(rule.proto));
    tr.appendChild(cell(String(rule.listen), true));
    tr.appendChild(cell(rule.target, true));

    const visTd = document.createElement("td");
    visTd.appendChild(document.createTextNode(visibilityText(rule)));
    const visBtn = document.createElement("button");
    visBtn.type = "button";
    visBtn.className = "btn-link";
    visBtn.textContent = "修改";
    visBtn.addEventListener("click", () => changeRuleVisibility(rule));
    visTd.appendChild(visBtn);
    tr.appendChild(visTd);

    const stateTd = document.createElement("td");
    const [text, cls] = STATE_TEXT[rule.state] || [rule.state || "-", ""];
    const badge = document.createElement("span");
    badge.className = "badge" + (cls ? " " + cls : "");
    badge.textContent = text;
    if (rule.message) badge.title = rule.message;
    stateTd.appendChild(badge);
    if (rule.message && rule.state === "error") {
      const msg = document.createElement("span");
      msg.className = "cell-note";
      msg.textContent = rule.message;
      stateTd.appendChild(msg);
    }
    tr.appendChild(stateTd);

    const op = document.createElement("td");
    const del = document.createElement("button");
    del.type = "button";
    del.className = "link-danger";
    del.textContent = "删除";
    del.addEventListener("click", () => removeRule(rule.name));
    op.appendChild(del);
    tr.appendChild(op);

    tbody.appendChild(tr);
  }
}

function serverHost(server) {
  if (!server) return "";
  const idx = server.lastIndexOf(":");
  return idx > 0 ? server.slice(0, idx) : server;
}

function renderPeers(peers, server) {
  const tbody = $("peer-rows");
  tbody.replaceChildren();
  const q = state.peerQuery.trim().toLowerCase();
  const list = (peers || []).filter((p) => {
    if (!q) return true;
    const hay = [
      p.node_id,
      p.overlay_ip,
      (p.rules || []).map((r) => `${r.name} ${r.proto} ${r.listen}`).join(" "),
    ]
      .join(" ")
      .toLowerCase();
    return hay.includes(q);
  });
  $("peer-count").textContent = `${list.length} / ${(peers || []).length} 个节点`;

  if (!list.length) {
    emptyRow(tbody, 5, (peers || []).length ? "没有匹配的节点" : "暂无其他客户端可见");
    return;
  }

  const host = serverHost(server);
  for (const peer of list) {
    const tr = document.createElement("tr");

    const nameTd = document.createElement("td");
    nameTd.appendChild(document.createTextNode(peer.node_id + " "));
    const st = document.createElement("span");
    st.className = "badge " + (peer.online ? "ok" : "offline");
    st.textContent = peer.online ? "在线" : "离线";
    nameTd.appendChild(st);
    tr.appendChild(nameTd);

    tr.appendChild(cell(peer.overlay_ip || "-", true));
    tr.appendChild(cell(peer.path === "direct" ? "直连" : "中继"));

    const rules = peer.rules || [];
    const rulesTd = document.createElement("td");
    if (!rules.length) {
      rulesTd.textContent = "未共享规则";
    } else {
      for (const rule of rules) {
        const line = document.createElement("div");
        line.className = "cell-note";
        const suffix = rule.state === "offline" ? "（离线）" : "";
        line.textContent = `${rule.name} · ${rule.proto || "tcp"} · 端口 ${rule.listen}${suffix}`;
        rulesTd.appendChild(line);
      }
    }
    tr.appendChild(rulesTd);

    const addrTd = document.createElement("td");
    if (!rules.length) {
      addrTd.textContent = "-";
    } else {
      for (const rule of rules) {
        const line = document.createElement("div");
        line.className = "cell-note mono";
        line.textContent = `${host || "服务端"}:${rule.listen}`;
        addrTd.appendChild(line);
      }
    }
    tr.appendChild(addrTd);

    tbody.appendChild(tr);
  }
}

function renderStatus(status) {
  state.status = status;
  $("side-version").textContent = status.version ? `v${status.version}` : "mtun";

  const stateEl = $("conn-state");
  stateEl.textContent = status.connected ? "已连接" : "未连接";
  stateEl.className = "value small " + (status.connected ? "ok" : "bad");

  $("node-id").textContent = status.node_id || status.node || "-";
  $("overlay").textContent = status.overlay_ip || "-";
  $("uptime").textContent = status.connected ? fmtDuration(status.connected_seconds) : "-";

  $("info-server").textContent = status.server || "未配置";
  $("info-access").textContent = status.has_access ? "已保存" : "未配置";
  $("info-rules").textContent = (status.rules || []).length;
  $("info-version").textContent = status.version || "-";

  const errEl = $("error");
  if (status.error) {
    errEl.textContent = status.error;
    errEl.hidden = false;
  } else {
    errEl.hidden = true;
  }

  if (!state.serverPrefilled && status.server) {
    $("server").value = status.server;
  }
  state.serverPrefilled = true;
  $("key").placeholder = status.has_access ? "留空表示不修改（已保存接入密码）" : "首次必填，之后留空表示不修改";

  renderRules(status.rules || []);
  renderPeers(status.peers || [], status.server);
}

async function refresh() {
  try {
    renderStatus(await getJSON("/api/status"));
  } catch (err) {
    $("conn-state").textContent = "控制台连接中断";
    $("conn-state").className = "value small bad";
  }
}

async function removeRule(name) {
  if (!confirm(`删除规则 ${name}？`)) return;
  setMsg("rule-msg", "");
  try {
    await postJSON("/api/rules/delete", { name });
    setMsg("rule-msg", "已删除", true);
    await refresh();
  } catch (err) {
    setMsg("rule-msg", err.message);
  }
}

function setMsg(id, text, ok) {
  const el = $(id);
  el.textContent = text;
  el.className = "form-msg" + (ok ? " ok" : "");
}

$("server-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = event.target.querySelector("button");
  const label = button.textContent;
  button.disabled = true;
  button.textContent = "保存中...";
  setMsg("server-msg", "");
  try {
    await postJSON("/api/server", { server: $("server").value.trim(), key: $("key").value });
    $("key").value = "";
    setMsg("server-msg", "已保存，正在重连", true);
    await refresh();
  } catch (err) {
    setMsg("server-msg", err.message);
  } finally {
    button.disabled = false;
    button.textContent = label;
  }
});

$("rule-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = event.target.querySelector("button");
  const label = button.textContent;
  button.disabled = true;
  button.textContent = "添加中...";
  setMsg("rule-msg", "");
  try {
    const visibility = $("r-visibility").value;
    const visibleTo =
      visibility === "nodes"
        ? $("r-visible-to").value.split(",").map((s) => s.trim()).filter(Boolean)
        : [];
    await postJSON("/api/rules", {
      name: $("r-name").value.trim(),
      proto: $("r-proto").value,
      listen: Number($("r-listen").value),
      target: $("r-target").value.trim(),
      visibility,
      visible_to: visibleTo,
    });
    $("r-name").value = "";
    $("r-listen").value = "";
    $("r-target").value = "";
    $("r-visible-to").value = "";
    $("r-visibility").value = "all";
    $("r-visible-wrap").style.display = "none";
    setMsg("rule-msg", "规则已添加", true);
    await refresh();
  } catch (err) {
    setMsg("rule-msg", err.message);
  } finally {
    button.disabled = false;
    button.textContent = label;
  }
});

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
  if (!confirm("确定更新并重启客户端？只会替换程序，配置与转发规则不会丢失。")) return;
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
    $("update-status").textContent = `已更新到 ${data.to}，客户端正在重启...`;
    setTimeout(() => location.reload(), 5000);
  } catch (err) {
    $("update-status").textContent = "更新请求失败";
    $("btn-update").disabled = false;
  }
}

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
  $("autostart-detail").textContent = detail.filter(Boolean).join(" · ") || "只影响下次启动，不会停止当前客户端";
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
    $("autostart-msg").textContent = enabled ? "已启用开机自启动" : "已禁用开机自启动，当前客户端保持运行";
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
  if (page === "overview") loadAutostart();
}

$("rule-search").addEventListener("input", (e) => {
  state.ruleQuery = e.target.value;
  renderRules(state.status ? state.status.rules : []);
});
$("peer-search").addEventListener("input", (e) => {
  state.peerQuery = e.target.value;
  renderPeers(state.status ? state.status.peers : [], state.status ? state.status.server : "");
});
$("r-visibility").addEventListener("change", (e) => {
  $("r-visible-wrap").style.display = e.target.value === "nodes" ? "flex" : "none";
});
$("btn-check").addEventListener("click", loadUpdate);
$("btn-update").addEventListener("click", applyUpdate);
$("btn-autostart").addEventListener("click", toggleAutostart);
window.addEventListener("hashchange", navigate);

navigate();
refresh();
setInterval(refresh, 3000);

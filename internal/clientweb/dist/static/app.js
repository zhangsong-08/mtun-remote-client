const $ = (id) => document.getElementById(id);

const state = {
  status: null,
  ruleQuery: "",
  updateAvailable: false,
  serverPrefilled: false,
};

const ROUTES = new Set(["overview", "server", "rules", "update"]);

async function getJSON(url) {
  const res = await fetch(url, { credentials: "same-origin" });
  if (res.status === 401) {
    location.href = "/login";
    throw new Error("unauthorized");
  }
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
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

function renderRules(rules) {
  const tbody = $("rows");
  tbody.replaceChildren();
  const q = state.ruleQuery.trim().toLowerCase();
  const list = (rules || []).filter((rule) => {
    if (!q) return true;
    return [rule.name, rule.proto, rule.listen, rule.target].join(" ").toLowerCase().includes(q);
  });
  $("rule-count").textContent = `${list.length} / ${(rules || []).length} 条规则`;

  if (!list.length) {
    emptyRow(tbody, 6, (rules || []).length ? "没有匹配的规则" : "还没有转发规则，添加后服务端会监听对应公网端口");
    return;
  }

  for (const rule of list) {
    const tr = document.createElement("tr");
    tr.appendChild(cell(rule.name));
    tr.appendChild(cell(rule.proto));
    tr.appendChild(cell(String(rule.listen), true));
    tr.appendChild(cell(rule.target, true));

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
    await postJSON("/api/rules", {
      name: $("r-name").value.trim(),
      proto: $("r-proto").value,
      listen: Number($("r-listen").value),
      target: $("r-target").value.trim(),
    });
    $("r-name").value = "";
    $("r-listen").value = "";
    $("r-target").value = "";
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
}

$("rule-search").addEventListener("input", (e) => {
  state.ruleQuery = e.target.value;
  renderRules(state.status ? state.status.rules : []);
});
$("btn-check").addEventListener("click", loadUpdate);
$("btn-update").addEventListener("click", applyUpdate);
window.addEventListener("hashchange", navigate);

navigate();
refresh();
setInterval(refresh, 3000);

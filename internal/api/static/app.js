/* xcut web UI — vanilla JS, no framework, no build step. */
"use strict";

const $ = (id) => document.getElementById(id);
let currentProject = null;
let pollTimer = null;

async function api(path, opts) {
  const resp = await fetch(path, opts);
  let body = {};
  try { body = await resp.json(); } catch (_) { /* non-JSON */ }
  if (!resp.ok) {
    const msg = body.message || resp.statusText;
    throw new Error(`${resp.status}: ${msg}`);
  }
  return body;
}

async function post(path, body) {
  return api(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body || {}),
  });
}

function busy(on) {
  for (const id of ["btn-analyze", "btn-timeline", "btn-render"]) {
    $(id).disabled = on;
  }
}

/* ---------- health ---------- */
async function refreshHealth() {
  const el = $("health");
  try {
    const h = await api("/api/v1/health");
    el.textContent = `v${h.version} · online`;
    el.className = "health ok";
  } catch (_) {
    el.textContent = "offline";
    el.className = "health bad";
  }
}

/* ---------- projects ---------- */
async function refreshProjects() {
  const { projects } = await api("/api/v1/projects");
  const ul = $("project-list");
  ul.innerHTML = "";
  for (const p of projects) {
    const li = document.createElement("li");
    li.textContent = p.name;
    if (currentProject && p.id === currentProject.id) li.classList.add("active");
    li.addEventListener("click", () => selectProject(p));
    ul.appendChild(li);
  }
}

function selectProject(p) {
  currentProject = p;
  $("project-view").hidden = false;
  $("detail").classList.remove("empty");
  const ph = document.querySelector("#detail .placeholder");
  if (ph) ph.style.display = "none";
  $("project-name").textContent = p.name;
  refreshAssets();
  refreshJobs();
}

/* ---------- assets ---------- */
async function refreshAssets() {
  if (!currentProject) return;
  const { assets } = await api(`/api/v1/projects/${currentProject.id}`);
  const tbody = $("assets").querySelector("tbody");
  tbody.innerHTML = "";
  for (const a of assets) {
    const tr = document.createElement("tr");
    const dur = `${Math.floor(a.duration_s / 60)}:${String(Math.round(a.duration_s % 60)).padStart(2, "0")}`;
    const mb = (a.size_bytes / 1048576).toFixed(1) + " MB";
    tr.innerHTML = `<td>${a.filename}</td><td>${dur}</td><td>${mb}</td>` +
      `<td>${a.video_codec}${a.has_audio ? " +" + a.audio_codec : ""}</td>`;
    tbody.appendChild(tr);
  }
}

/* ---------- jobs ---------- */
async function refreshJobs() {
  if (!currentProject) return;
  const { jobs } = await api(`/api/v1/projects/${currentProject.id}/jobs`);
  const ul = $("jobs");
  ul.innerHTML = "";
  let running = false;
  for (const j of jobs.slice(0, 12)) {
    const li = document.createElement("li");
    li.className = j.status;
    const pct = j.status === "running" ? ` ${Math.round((j.progress || 0) * 100)}%` : "";
    li.innerHTML = `<span class="status">${j.status}${pct}</span>` +
      `<span>${j.type}</span>` +
      `<span class="meta">${j.error_message || j.id}</span>`;
    ul.appendChild(li);
    if (j.status === "running" || j.status === "queued") running = true;
  }
  busy(running);
  // Refresh assets once after a batch of work likely changed them.
  if (!running) refreshAssetsSoon();
  schedulePoll(running ? 800 : 2500);
}

let assetsTimer = null;
function refreshAssetsSoon() {
  if (assetsTimer) return;
  assetsTimer = setTimeout(() => { assetsTimer = null; refreshAssets(); }, 400);
}

function schedulePoll(ms) {
  clearTimeout(pollTimer);
  pollTimer = setTimeout(refreshJobs, ms);
}

/* ---------- actions ---------- */
async function trigger(path, body, btn) {
  busy(true);
  try {
    const { job_id } = await post(`/api/v1/projects/${currentProject.id}${path}`, body);
    await refreshJobs();
    watchUntilDone(jobIdOf(job_id));
  } catch (e) {
    alert(`Failed: ${e.message}`);
    busy(false);
  }
}

function jobIdOf(id) { return id; }

async function watchUntilDone(jobID) {
  // refreshJobs polls anyway; this re-enables once nothing is running.
  const timer = setInterval(async () => {
    try {
      const { job } = await api(`/api/v1/jobs/${jobID}`);
      if (["succeeded", "failed", "cancelled"].includes(job.status)) {
        clearInterval(timer);
        if (job.type === "render" && job.status === "succeeded") showPlayer();
        busy(false);
        refreshJobs();
      }
    } catch (_) { /* transient */ }
  }, 1000);
}

function showPlayer() {
  if (!currentProject) return;
  $("player").src = `/api/v1/projects/${currentProject.id}/render?t=${Date.now()}`;
  $("download").href = `/api/v1/projects/${currentProject.id}/render`;
}

/* ---------- wiring ---------- */
$("new-project").addEventListener("submit", async (e) => {
  e.preventDefault();
  try {
    const { project } = await post("/api/v1/projects", { name: $("np-name").value.trim() });
    $("np-name").value = "";
    await refreshProjects();
    selectProject(project);
  } catch (err) { alert(`Create failed: ${err.message}`); }
});

$("import-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!currentProject) return;
  const path = $("import-path").value.trim();
  if (!path) return;
  try {
    await trigger("/assets", { path });
    $("import-path").value = "";
  } catch (err) { alert(`Import failed: ${err.message}`); busy(false); }
});

$("btn-analyze").addEventListener("click", () => trigger("/analyze", {}));
$("btn-timeline").addEventListener("click", async () => {
  trigger("/timeline", { style: $("style").value });
});
$("btn-render").addEventListener("click", async () => {
  try { await trigger("/render", {}); showPlayerSoon(); } catch (e) { alert(e.message); busy(false); }
});

let playerTimer = null;
function showPlayerSoon() {
  clearTimeout(playerTimer);
  playerTimer = setTimeout(showPlayer, 1500);
}

async function loadStyles() {
  try {
    const { styles } = await api("/api/v1/styles");
    const sel = $("style");
    sel.innerHTML = "";
    for (const s of styles) {
      const o = document.createElement("option");
      o.value = s; o.textContent = s;
      sel.appendChild(o);
    }
  } catch (_) { /* non-fatal */ }
}

refreshHealth();
setInterval(refreshHealth, 15000);
refreshProjects();
loadStyles();
refreshJobs();

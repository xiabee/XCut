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

function banner(msg) {
  const el = $("banner");
  if (!msg) { el.hidden = true; return; }
  el.textContent = msg;
  el.hidden = false;
  clearTimeout(banner._t);
  banner._t = setTimeout(() => { el.hidden = true; }, 6000);
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
  $("delete-project").hidden = false;
  refreshAssets();
  refreshJobs();
  refreshTimeline();
}

async function deleteProject() {
  if (!currentProject) return;
  // Two-step confirm without confirm(): the button asks once more.
  const btn = $("delete-project");
  if (btn.dataset.armed !== "1") {
    btn.dataset.armed = "1";
    btn.textContent = `Really delete "${currentProject.name}"? Click again`;
    setTimeout(() => { btn.dataset.armed = ""; btn.textContent = "Delete project"; }, 4000);
    return;
  }
  btn.dataset.armed = "";
  btn.textContent = "Delete project";
  try {
    await api(`/api/v1/projects/${currentProject.id}`, { method: "DELETE" });
    currentProject = null;
    $("project-view").hidden = true;
    $("detail").classList.add("empty");
    const ph = document.querySelector("#detail .placeholder");
    if (ph) ph.style.display = "";
    btn.hidden = true;
    $("player").removeAttribute("src");
    await refreshProjects();
  } catch (e) { banner(`Delete failed: ${e.message}`); }
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
    // Untrusted by policy: filenames are user input, textContent only.
    const tdName = document.createElement("td");
    tdName.textContent = a.filename;
    tdName.title = a.filename;
    const tdDur = document.createElement("td");
    tdDur.textContent = dur;
    const tdSize = document.createElement("td");
    tdSize.textContent = mb;
    const tdCodec = document.createElement("td");
    tdCodec.textContent = a.video_codec + (a.has_audio ? " +" + a.audio_codec : "");
    tr.append(tdName, tdDur, tdSize, tdCodec);
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
    // error_message may echo hostile input (e.g. filenames): textContent only.
    const status = document.createElement("span");
    status.className = "status";
    status.textContent = `${j.status}${pct}`;
    const type = document.createElement("span");
    type.textContent = j.type;
    const meta = document.createElement("span");
    meta.className = "meta";
    meta.textContent = j.error_message || j.id;
    li.append(status, type, meta);
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
async function trigger(path, body) {
  busy(true);
  try {
    const { job_id } = await post(`/api/v1/projects/${currentProject.id}${path}`, body);
    await refreshJobs();
    watchUntilDone(jobIdOf(job_id));
  } catch (e) {
    busy(false);
    throw e; // caller renders the banner
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
        if (job.status === "succeeded" && job.type === "timeline") await refreshTimeline();
        if (job.status === "succeeded" && job.type === "render") showPlayer();
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

/* ---------- timeline editing ---------- */
let timelineDoc = null; // last fetched timeline JSON
let clipEdits = null;   // working copy of timelineDoc.tracks[0].clips

async function refreshTimeline() {
  timelineDoc = null;
  clipEdits = null;
  renderClips();
  if (!currentProject) return;
  try {
    const { timeline } = await api(`/api/v1/projects/${currentProject.id}/timeline`);
    timelineDoc = timeline;
    clipEdits = JSON.parse(JSON.stringify(timeline.tracks[0].clips));
  } catch (_) { /* no timeline yet — expected before first generation */ }
  renderClips();
}

function renderClips() {
  const tbody = document.querySelector("#clips tbody");
  tbody.innerHTML = "";
  const has = Array.isArray(clipEdits);
  $("btn-tl-save").disabled = !has;
  $("btn-tl-reset").disabled = !has;
  if (!has) {
    $("tl-status").textContent = timelineDoc === null ? "generate a timeline first" : "";
    return;
  }
  $("tl-status").textContent = `${clipEdits.filter(c => !c._removed).length} clips · ` +
    `${(clipEdits.filter(c => !c._removed).reduce((s, c) => s + (c.source_end - c.source_start), 0)).toFixed(1)}s`;
  clipEdits.forEach((c, i) => {
    const tr = document.createElement("tr");
    if (c._removed) tr.className = "removed";
    const dur = (c.source_end - c.source_start).toFixed(1) + "s";

    const tdNum = document.createElement("td");
    tdNum.textContent = i + 1;
    const tdRange = document.createElement("td");
    tdRange.textContent = `${c.source_start.toFixed(1)}s – ${c.source_end.toFixed(1)}s`;
    const tdDur = document.createElement("td");
    tdDur.textContent = dur;
    // Clip metadata comes from the style engine (score breakdown / reason).
    // Untrusted by policy: textContent only, never innerHTML.
    const tdScore = document.createElement("td");
    tdScore.textContent = (c.metadata && c.metadata.score) || "";
    tdScore.title = (c.metadata && c.metadata.score_breakdown) || "";
    const tdWhy = document.createElement("td");
    tdWhy.textContent = (c.metadata && c.metadata.reason) || "";
    tdWhy.title = (c.metadata && c.metadata.score_breakdown) || "";
    const tdAct = document.createElement("td");
    tdAct.innerHTML =
      `<button data-act="up" title="move up">↑</button>` +
      `<button data-act="down" title="move down">↓</button>` +
      `<button data-act="del" title="remove">${c._removed ? "undo" : "✕"}</button>`;

    tr.append(tdNum, tdRange, tdDur, tdScore, tdWhy, tdAct);
    tr.addEventListener("click", (e) => {
      const act = e.target.dataset && e.target.dataset.act;
      if (!act) return;
      if (act === "del") c._removed = !c._removed;
      if (act === "up" && i > 0) [clipEdits[i - 1], clipEdits[i]] = [clipEdits[i], clipEdits[i - 1]];
      if (act === "down" && i < clipEdits.length - 1) [clipEdits[i + 1], clipEdits[i]] = [clipEdits[i], clipEdits[i + 1]];
      renderClips();
    });
    tbody.appendChild(tr);
  });
}

async function saveTimeline() {
  if (!currentProject || !timelineDoc) return;
  const kept = clipEdits.filter(c => !c._removed);
  if (kept.length === 0) { banner("Cannot save: every clip is removed"); return; }
  const doc = JSON.parse(JSON.stringify(timelineDoc));
  doc.tracks[0].clips = kept.map((c, i) => {
    const copy = { ...c };
    delete copy._removed;
    copy.id = `clip_${i + 1}`;
    copy.timeline_start = kept.slice(0, i).reduce((s, x) => s + (x.source_end - x.source_start), 0);
    copy.transition = undefined;
    return copy;
  });
  try {
    const resp = await fetch(`/api/v1/projects/${currentProject.id}/timeline`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc),
    });
    const body = await resp.json();
    if (!resp.ok) { banner(`Save rejected: ${body.message || resp.statusText}`); return; }
    banner(`Timeline saved (${body.clips} clips)`);
    await refreshTimeline();
  } catch (e) { banner(`Save failed: ${e.message}`); }
}

$("btn-tl-save").addEventListener("click", saveTimeline);
$("btn-tl-reset").addEventListener("click", refreshTimeline);

/* ---------- wiring ---------- */
$("delete-project").addEventListener("click", deleteProject);

$("new-project").addEventListener("submit", async (e) => {
  e.preventDefault();
  try {
    const { project } = await post("/api/v1/projects", { name: $("np-name").value.trim() });
    $("np-name").value = "";
    banner("");
    await refreshProjects();
    selectProject(project);
  } catch (err) { banner(`Create failed: ${err.message}`); }
});

$("import-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!currentProject) return;
  const path = $("import-path").value.trim();
  if (!path) return;
  try {
    await trigger("/assets", { path });
    $("import-path").value = "";
  } catch (err) { banner(`Import failed: ${err.message}`); busy(false); }
});

$("btn-analyze").addEventListener("click", async () => {
  try { await trigger("/analyze", {}); } catch (e) { banner(`Analyze failed: ${e.message}`); busy(false); }
});
$("btn-timeline").addEventListener("click", async () => {
  try { await trigger("/timeline", { style: $("style").value }); } catch (e) { banner(`Timeline failed: ${e.message}`); busy(false); }
});
$("btn-render").addEventListener("click", async () => {
  try {
    await trigger("/render", {});
    showPlayerSoon();
  } catch (e) { banner(`Render failed: ${e.message}`); busy(false); }
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

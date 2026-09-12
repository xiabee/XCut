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
  for (const id of ["btn-analyze", "btn-timeline", "btn-render", "btn-subtitles", "btn-subs-preview"]) {
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
  // Reset the player: without this, switching from a project that has a
  // render shows the OLD project's video (and its download link) under the
  // new project until the new project renders something.
  $("player").removeAttribute("src");
  $("player").load();
  $("download").href = "#";
  // Disarm the regeneration confirm across project switches.
  const tl = $("btn-timeline");
  tl.dataset.armed = "";
  tl.textContent = "2 · Timeline";
  refreshAssets();
  refreshJobs();
  refreshTimeline();
  refreshSubtitlesStatus();
  refreshROIStatus();
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
// Stale-response guard: every project-scoped refresh captures the project id
// when the request starts and discards the response if the user has since
// switched projects — otherwise a slow response from A lands after B was
// selected and renders A's rows (or worse, A's timeline document) under B.
function projectChangedSince(pid) {
  return !currentProject || currentProject.id !== pid;
}

async function refreshAssets() {
  if (!currentProject) return;
  const pid = currentProject.id;
  const { assets } = await api(`/api/v1/projects/${pid}`);
  if (projectChangedSince(pid)) return;
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
  // The transcribe picker mirrors the asset list (first = default).
  const sel = $("subs-asset");
  sel.innerHTML = "";
  for (const a of assets) {
    const o = document.createElement("option");
    o.value = a.id;
    o.textContent = a.filename;
    sel.appendChild(o);
  }
}

/* ---------- jobs ---------- */
// Polling must survive transient failures (serve restart, laptop sleep):
// one unhandled rejection used to stop rescheduling forever, silently
// freezing the job panel and leaving busy() buttons stuck.
let pollFailures = 0;
async function refreshJobs() {
  if (!currentProject) {
    // Keep the loop alive: a deleted project must not kill polling
    // permanently (the next selection reuses this timer).
    schedulePoll(2500);
    return;
  }
  const pid = currentProject.id;
  try {
    const { jobs } = await api(`/api/v1/projects/${pid}/jobs`);
    pollFailures = 0;
    if (projectChangedSince(pid)) { schedulePoll(2500); return; }
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
      if (j.status === "running" || j.status === "queued") {
        running = true;
        const cancel = document.createElement("button");
        cancel.className = "cancel";
        cancel.textContent = "Cancel";
        cancel.addEventListener("click", () => cancelJob(j.id));
        li.append(cancel);
      }
      ul.appendChild(li);
    }
    busy(running);
    // A subtitles job in the batch (started anywhere — CLI, another tab)
    // means the artifacts may have just changed: refresh the panel.
    if (jobs.some((j) => j.type === "subtitles")) refreshSubtitlesStatus();
    // Refresh assets once after a batch of work likely changed them.
    if (!running) refreshAssetsSoon();
    schedulePoll(running ? 800 : 2500);
  } catch (e) {
    pollFailures++;
    if (pollFailures === 5) banner("Lost contact with the server — retrying…");
    schedulePoll(Math.min(2500 * pollFailures, 15000));
  }
}

async function cancelJob(jobID) {
  try {
    await post(`/api/v1/jobs/${jobID}/cancel`, {});
  } catch (e) {
    banner(`Cancel failed: ${e.message}`);
  }
  refreshJobs();
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
  // A job row that is gone for good (pruned, or serve restarted mid-job)
  // must not keep this timer polling a 404 at 1Hz forever.
  const pid = currentProject && currentProject.id;
  let misses = 0;
  const timer = setInterval(async () => {
    let job;
    try {
      job = (await api(`/api/v1/jobs/${jobID}`)).job;
      misses = 0;
    } catch (_) {
      if (++misses < 5) return; // transient — the poll loop also retries
      clearInterval(timer);
      busy(false);
      refreshJobs();
      return;
    }
    if (["succeeded", "failed", "cancelled"].includes(job.status)) {
      clearInterval(timer);
      if (projectChangedSince(pid)) return; // A's job ending must not touch B's editor
      if (job.status === "succeeded" && job.type === "timeline") await refreshTimeline();
      if (job.status === "succeeded" && job.type === "render") showPlayer();
      if (job.type === "subtitles") await refreshSubtitlesStatus();
    }
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
let dragIndex = -1;     // clipEdits index being dragged (HTML5 DnD reorder)

async function refreshTimeline() {
  timelineDoc = null;
  clipEdits = null;
  dragIndex = -1;
  const prev = $("clip-preview");
  if (prev) { prev.hidden = true; prev.removeAttribute("src"); prev.load(); }
  renderClips();
  if (!currentProject) return;
  const pid = currentProject.id;
  try {
    const { timeline, has_backup } = await api(`/api/v1/projects/${pid}/timeline`);
    if (projectChangedSince(pid)) return; // stale: A's document must not become B's editing state
    timelineDoc = timeline;
    clipEdits = JSON.parse(JSON.stringify(timeline.tracks[0].clips));
    $("btn-tl-restore").hidden = !has_backup;
  } catch (_) { /* no timeline yet — expected before first generation */ }
  renderClips();
}

function renderClips() {
  const tbody = document.querySelector("#clips tbody");
  tbody.innerHTML = "";
  const has = Array.isArray(clipEdits);
  $("btn-tl-save").disabled = !has;
  $("btn-tl-reset").disabled = !has;
  if (!has) $("btn-tl-restore").hidden = true;
  if (!has) {
    $("tl-status").textContent = timelineDoc === null ? "generate a timeline first" : "";
    return;
  }
  const playDur = (c) => (c.source_end - c.source_start) / (c.speed > 0 ? c.speed : 1);
  $("tl-status").textContent = `${clipEdits.filter(c => !c._removed).length} clips · ` +
    `${(clipEdits.filter(c => !c._removed).reduce((s, c) => s + playDur(c), 0)).toFixed(1)}s`;
  clipEdits.forEach((c, i) => {
    const tr = document.createElement("tr");
    if (c._removed) tr.className = "removed";
    const dur = playDur(c).toFixed(1) + "s" + (c.speed && c.speed !== 1 ? ` @${c.speed}x` : "");

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
      `<button data-act="preview" title="preview this clip's source at its start offset">▶</button>` +
      `<button data-act="up" title="move up">↑</button>` +
      `<button data-act="down" title="move down">↓</button>` +
      `<button data-act="del" title="remove">${c._removed ? "undo" : "✕"}</button>`;

    // Drag to reorder (HTML5 DnD, no deps): drop reorders clipEdits and the
    // existing save path recomputes timeline_start from the new order.
    if (!c._removed) {
      tr.draggable = true;
      tr.dataset.index = i;
      tr.addEventListener("dragstart", (e) => {
        dragIndex = i;
        tr.classList.add("dragging");
        e.dataTransfer.effectAllowed = "move";
        try { e.dataTransfer.setData("text/plain", String(i)); } catch (_) { /* IE-style targets */ }
      });
      tr.addEventListener("dragend", () => tr.classList.remove("dragging"));
      tr.addEventListener("dragover", (e) => {
        e.preventDefault();
        e.dataTransfer.dropEffect = "move";
        tr.classList.toggle("drop-target", dragIndex >= 0 && dragIndex !== i);
      });
      tr.addEventListener("dragleave", () => tr.classList.remove("drop-target"));
      tr.addEventListener("drop", (e) => {
        e.preventDefault();
        tr.classList.remove("drop-target");
        if (dragIndex < 0 || dragIndex === i) return;
        const [moved] = clipEdits.splice(dragIndex, 1);
        clipEdits.splice(i, 0, moved);
        dragIndex = -1;
        renderClips();
      });
    }

    tr.append(tdNum, tdRange, tdDur, tdScore, tdWhy, tdAct);
    tr.addEventListener("click", (e) => {
      const act = e.target.dataset && e.target.dataset.act;
      if (!act) return;
      if (act === "del") c._removed = !c._removed;
      if (act === "up" && i > 0) [clipEdits[i - 1], clipEdits[i]] = [clipEdits[i], clipEdits[i - 1]];
      if (act === "down" && i < clipEdits.length - 1) [clipEdits[i + 1], clipEdits[i]] = [clipEdits[i], clipEdits[i + 1]];
      if (act === "preview") { previewClip(c); return; }
      renderClips();
    });
    tbody.appendChild(tr);
  });
}

// previewClip plays the clip's source media from its start offset in the
// editor preview player. The media URL is the project-scoped asset endpoint
// (DB-registered path only — no client-supplied paths).
function previewClip(c) {
  const video = $("clip-preview");
  if (!video || !currentProject || !c.asset_id) return;
  video.hidden = false;
  video.src = `/api/v1/projects/${currentProject.id}/assets/${encodeURIComponent(c.asset_id)}/file`;
  const seek = () => {
    if (c.source_start > 0 && isFinite(c.source_start)) {
      try { video.currentTime = c.source_start; } catch (_) { /* not seekable yet */ }
    }
    video.play().catch(() => { /* autoplay policies — user can press play */ });
  };
  if (video.readyState >= 1) seek();
  else video.addEventListener("loadedmetadata", seek, { once: true });
  video.scrollIntoView({ block: "nearest" });
}

async function restoreBackup() {
  if (!currentProject) return;
  try {
    await api(`/api/v1/projects/${currentProject.id}/timeline/restore-backup`, { method: "POST" });
    banner("Timeline backup restored (the regenerated version is now the backup)");
    await refreshTimeline();
  } catch (e) { banner(`Restore failed: ${e.message}`); }
}

async function saveTimeline() {
  if (!currentProject || !timelineDoc) return;
  const kept = clipEdits.filter(c => !c._removed);
  if (kept.length === 0) { banner("Cannot save: every clip is removed"); return; }
  const doc = JSON.parse(JSON.stringify(timelineDoc));
  // Optimistic concurrency: send the revision we read; a mismatch (another
  // tab saved, or the timeline was regenerated) is refused with 409.
  doc.revision = timelineDoc.revision || 0;
  const playDur = (x) => (x.source_end - x.source_start) / (x.speed > 0 ? x.speed : 1);
  let dropped = 0;
  let prevEnd = 0;
  const outs = [];
  kept.forEach((c, i) => {
    const copy = { ...c };
    delete copy._removed;
    copy.id = `clip_${i + 1}`;
    let start = prevEnd;
    if (i > 0) {
      // The join between kept[i-1] and kept[i] is described by the
      // PREVIOUS clip's transition. An xfade join shares a blend window
      // (the previous clip's tail overlaps this clip's head by the
      // transition duration); every other join is back-to-back (a fade
      // needs no overlap). Preserve the transition when its window still
      // fits both clips after the edit — reordering can change the
      // neighbors — and degrade loudly to a cut when an xfade does not,
      // instead of silently stripping every transition in the document.
      const prev = kept[i - 1];
      const t = prev.transition;
      if (t && t.type === "xfade") {
        const fits = t.duration > 0 &&
          t.duration <= playDur(prev) + 1e-9 && t.duration <= playDur(c) + 1e-9;
        if (fits) {
          start = prevEnd - t.duration;
        } else {
          dropped++;
          delete outs[i - 1].transition;
        }
      }
    }
    copy.timeline_start = start;
    // A trailing xfade has nothing to blend with (the renderer would
    // refuse the save) — degrade it too.
    if (i === kept.length - 1 && copy.transition && copy.transition.type === "xfade") {
      dropped++;
      delete copy.transition;
    }
    outs.push(copy);
    prevEnd = start + playDur(c);
  });
  doc.tracks[0].clips = outs;
  try {
    const resp = await fetch(`/api/v1/projects/${currentProject.id}/timeline`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc),
    });
    const body = await resp.json();
    if (!resp.ok) {
      banner(resp.status === 409
        ? "Save rejected: the timeline changed elsewhere — press Reset to load the current version, then reapply your edits"
        : `Save rejected: ${body.message || resp.statusText}`);
      return;
    }
    timelineDoc.revision = body.revision;
    banner(`Timeline saved (${body.clips} clips${dropped ? `, ${dropped} transition(s) dropped — their joins no longer fit` : ""})`);
    await refreshTimeline();
  } catch (e) { banner(`Save failed: ${e.message}`); }
}

$("btn-tl-save").addEventListener("click", saveTimeline);
$("btn-tl-restore").addEventListener("click", restoreBackup);
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
  const btn = $("btn-timeline");
  // Regeneration replaces the stored timeline — manual edits in the editor
  // are lost (the pre-regeneration document stays recoverable via Restore
  // backup). Two-step confirm, same pattern as delete: the first click asks.
  if (timelineDoc && btn.dataset.armed !== "1") {
    btn.dataset.armed = "1";
    btn.textContent = "Replace timeline? Click again";
    setTimeout(() => { btn.dataset.armed = ""; btn.textContent = "2 · Timeline"; }, 4000);
    return;
  }
  btn.dataset.armed = "";
  btn.textContent = "2 · Timeline";
  try { await trigger("/timeline", { style: $("style").value }); } catch (e) { banner(`Timeline failed: ${e.message}`); busy(false); }
});
$("btn-render").addEventListener("click", async () => {
  try {
    await trigger("/render", { subs: $("burn-subs").checked });
    showPlayerSoon();
  } catch (e) { banner(`Render failed: ${e.message}`); busy(false); }
});

/* ---------- subtitles (AI sidecar) ---------- */
async function refreshSubtitlesStatus() {
  const status = $("subs-status");
  const links = $("subs-links");
  const previewBtn = $("btn-subs-preview");
  const transcript = $("subs-transcript");
  const hideTranscript = () => { transcript.hidden = true; transcript.textContent = ""; };
  if (!currentProject) {
    status.textContent = ""; links.innerHTML = "";
    previewBtn.hidden = true; hideTranscript();
    return;
  }
  const pid = currentProject.id;
  try {
    const st = await api(`/api/v1/projects/${pid}/subtitles`);
    if (projectChangedSince(pid)) return; // stale: panel belongs to the new project now
    if (!st.srt && !st.ass) {
      status.textContent = "none yet — transcribe to create";
      links.innerHTML = "";
      previewBtn.hidden = true; hideTranscript();
      return;
    }
    status.textContent = st.ass ? "srt + karaoke ass ready" : "srt ready";
    const base = `/api/v1/projects/${currentProject.id}/subtitles/file?format=`;
    links.innerHTML = "";
    for (const [fmt, ok] of [["srt", st.srt], ["ass", st.ass]]) {
      if (!ok) continue;
      const a = document.createElement("a");
      a.href = base + fmt;
      a.download = "";
      a.textContent = `download .${fmt}`;
      a.style.marginLeft = "8px";
      links.appendChild(a);
    }
    previewBtn.hidden = !st.srt;
    if (!st.srt) hideTranscript();
  } catch (_) { status.textContent = "status unavailable"; }
}

$("btn-subs-preview").addEventListener("click", async () => {
  const transcript = $("subs-transcript");
  if (!transcript.hidden) { transcript.hidden = true; return; }
  if (!currentProject) return;
  const pid = currentProject.id;
  try {
    const resp = await fetch(`/api/v1/projects/${pid}/subtitles/file?format=srt`);
    if (projectChangedSince(pid)) return; // stale: do not render A's transcript under B
    if (!resp.ok) throw new Error(`${resp.status}: ${resp.statusText}`);
    transcript.textContent = srtToText(await resp.text()); // untrusted: textContent only
    transcript.hidden = false;
  } catch (e) { banner(`Preview failed: ${e.message}`); }
});

function srtToText(srt) {
  return srt.replace(/\r/g, "")
    .split("\n\n")
    .map((b) => b.split("\n")
      .filter((l) => l.trim() && !/^\d+$/.test(l.trim()) && !l.includes("-->"))
      .join(" "))
    .filter(Boolean)
    .join("\n");
}

$("btn-subtitles").addEventListener("click", async () => {
  try {
    await trigger("/subtitles", { asset: $("subs-asset").value || undefined });
    banner("Transcription queued — status updates when the job finishes");
  } catch (e) { banner(`Transcribe failed: ${e.message}`); busy(false); }
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
    refreshROIStatus();
  } catch (_) { /* non-fatal */ }
}

refreshHealth();
setInterval(refreshHealth, 15000);
refreshProjects();
loadStyles();
refreshJobs();

/* ---------- court ROI editor ---------- */
/* A normalized rect (0..1) persisted as a workspace override of the
 * selected style; the style engine feeds it to the frame_diff_roi analyzer
 * so crowd movement off the court cannot dominate the motion signal. */
let roiRect = null;      // {x,y,w,h} drawn but not yet saved
let roiAssetId = null;   // asset whose frame is on display

function roiStyleName() { return $("style").value; }

async function refreshROIStatus() {
  const status = $("roi-status");
  const clearBtn = $("btn-roi-clear");
  if (!currentProject || !roiStyleName()) { status.textContent = ""; clearBtn.hidden = true; return; }
  try {
    const { roi } = await api(`/api/v1/styles/${encodeURIComponent(roiStyleName())}/roi`);
    if (roi) {
      status.textContent = `ROI x=${roi.x.toFixed(2)} y=${roi.y.toFixed(2)} w=${roi.w.toFixed(2)} h=${roi.h.toFixed(2)}`;
      clearBtn.hidden = false;
    } else {
      status.textContent = "full frame (no ROI)";
      clearBtn.hidden = true;
    }
  } catch (_) { status.textContent = "roi status unavailable"; }
}

function drawROIOverlay() {
  const canvas = $("roi-canvas");
  const video = $("roi-video");
  canvas.width = video.clientWidth || 320;
  canvas.height = video.clientHeight || 240;
  const ctx = canvas.getContext("2d");
  ctx.clearRect(0, 0, canvas.width, canvas.height);
  if (!roiRect) return;
  ctx.strokeStyle = "#4cc38a";
  ctx.lineWidth = 2;
  ctx.setLineDash([6, 4]);
  ctx.strokeRect(roiRect.x * canvas.width, roiRect.y * canvas.height,
    roiRect.w * canvas.width, roiRect.h * canvas.height);
}

async function openROIEditor() {
  if (!currentProject) return;
  const opt = $("subs-asset").selectedOptions[0];
  if (!opt) { banner("Import an asset first — the ROI editor draws over its first frame"); return; }
  roiAssetId = opt.value;
  roiRect = null;
  $("btn-roi-save").disabled = true;
  $("roi-editor").hidden = false;
  const video = $("roi-video");
  video.src = `/api/v1/projects/${currentProject.id}/assets/${roiAssetId}/file#t=1`;
  video.currentTime = 1;
  video.pause();
  drawROIOverlay();
}

function closeROIEditor() {
  $("roi-editor").hidden = true;
  const video = $("roi-video");
  video.removeAttribute("src");
  video.load();
}

$("btn-roi").addEventListener("click", openROIEditor);
$("btn-roi-cancel").addEventListener("click", closeROIEditor);
$("btn-roi-clear").addEventListener("click", async () => {
  try {
    await api(`/api/v1/styles/${encodeURIComponent(roiStyleName())}/roi`, { method: "DELETE" });
    banner("Court ROI cleared — analysis uses the full frame again");
    refreshROIStatus();
  } catch (e) { banner(`Clear failed: ${e.message}`); }
});
$("btn-roi-save").addEventListener("click", async () => {
  if (!roiRect) return;
  try {
    await api(`/api/v1/styles/${encodeURIComponent(roiStyleName())}/roi`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(roiRect),
    });
    banner("Court ROI saved as a workspace override of this style");
    closeROIEditor();
    refreshROIStatus();
  } catch (e) { banner(`Save failed: ${e.message}`); }
});
$("style").addEventListener("change", refreshROIStatus);

(() => {
  const video = $("roi-video");
  const canvas = $("roi-canvas");
  let dragStart = null;
  const norm = (e) => {
    const r = canvas.getBoundingClientRect();
    return {
      x: Math.min(Math.max((e.clientX - r.left) / r.width, 0), 1),
      y: Math.min(Math.max((e.clientY - r.top) / r.height, 0), 1),
    };
  };
  canvas.addEventListener("pointerdown", (e) => {
    dragStart = norm(e);
    canvas.setPointerCapture(e.pointerId);
  });
  canvas.addEventListener("pointermove", (e) => {
    if (!dragStart) return;
    const p = norm(e);
    roiRect = {
      x: Math.min(dragStart.x, p.x),
      y: Math.min(dragStart.y, p.y),
      w: Math.abs(p.x - dragStart.x),
      h: Math.abs(p.y - dragStart.y),
    };
    drawROIOverlay();
    $("btn-roi-save").disabled = roiRect.w < 0.02 || roiRect.h < 0.02;
  });
  canvas.addEventListener("pointerup", () => { dragStart = null; });
  video.addEventListener("loadedmetadata", drawROIOverlay);
  window.addEventListener("resize", () => { if (!$("roi-editor").hidden) drawROIOverlay(); });
})();

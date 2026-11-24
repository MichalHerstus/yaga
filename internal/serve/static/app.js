"use strict";

/* WEdit SPA — vanilla JS, no build step. Talks to the JSON REST API in
   internal/serve/handlers.go. The config object mirrors the YAML field names
   (the server round-trips JSON <-> YAML). */

const state = {
  config: null,
  configPath: "",
  page: "panel",
  resource: null, // drill-in: resource being edited
  resourceEditMode: true, // false = view (read-only), true = edit
  resourceTab: "settings", // active sub-tab inside the dataview editor
  pageName: null, // drill-in: page being edited
  dirty: false,
  rawDirty: false, // raw-tab textarea has unsaved typing
  rawScrollTarget: null, // resource name to scroll to when opening Raw YAML
  resourceSearch: "", // filter text for the resources table
  schemaSearch: "", // filter text for the schema browser
  rev: 0, // last-known server config revision
  warnedRev: 0, // rev already announced as "changed on server" (toast dedup)
};

const FIELD_TYPES = [
  "integer", "string", "text", "email", "password", "boolean", "badge",
  "datetime", "date", "image", "file", "select", "relation", "json", "float", "gps",
];

/* ---------- DOM helpers ---------- */

const $ = (sel) => document.querySelector(sel);

function content() {
  const el = $("#content");
  el.innerHTML = "";
  return el;
}

function h2(root, text) {
  const el = document.createElement("h2");
  el.textContent = text;
  root.appendChild(el);
  return el;
}

function h3(root, text) {
  const el = document.createElement("h3");
  el.textContent = text;
  root.appendChild(el);
  return el;
}

function cardEl(root) {
  const el = document.createElement("div");
  el.className = "card";
  root.appendChild(el);
  return el;
}

function gridWrap(card) {
  const el = document.createElement("div");
  el.className = "grid";
  card.appendChild(el);
  return el;
}

function btn(label, cls) {
  const el = document.createElement("button");
  el.className = "btn " + (cls || "");
  el.textContent = label;
  return el;
}

function mkButton(label, onClick) {
  const el = btn(label, "small");
  el.addEventListener("click", onClick);
  return el;
}

function toast(msg, kind) {
  const el = $("#toast");
  el.textContent = msg;
  el.className = "toast " + (kind || "");
  el.classList.remove("hidden");
  clearTimeout(toast._t);
  toast._t = setTimeout(() => el.classList.add("hidden"), 4200);
}

function markDirty() {
  state.dirty = true;
  $("#dirty-indicator").classList.remove("hidden");
}

function clearDirty() {
  state.dirty = false;
  $("#dirty-indicator").classList.add("hidden");
}

/* ---------- editor theme (light/dark) ---------- */

function applyEditorTheme() {
  let saved = null;
  try { saved = localStorage.getItem("wedit-theme"); } catch (e) { /* ignore */ }
  const dark = saved
    ? saved === "dark"
    : window.matchMedia("(prefers-color-scheme: dark)").matches;
  document.documentElement.setAttribute("data-theme", dark ? "dark" : "light");
}

function toggleEditorTheme() {
  const dark = document.documentElement.getAttribute("data-theme") === "dark";
  document.documentElement.setAttribute("data-theme", dark ? "light" : "dark");
  try { localStorage.setItem("wedit-theme", dark ? "light" : "dark"); } catch (e) { /* ignore */ }
}

$("#theme-toggle").addEventListener("click", toggleEditorTheme);
$("#btn-validate").addEventListener("click", () => switchPage("validate"));
$("#btn-preview").addEventListener("click", () => switchPage("preview"));
$("#btn-raw").addEventListener("click", () => switchPage("raw"));

/* ---------- modal helpers ---------- */

let modalResolve = null;

function openModal(title, build) {
  $("#modal-title").textContent = title;
  const body = $("#modal-body");
  body.innerHTML = "";
  const okBtn = $("#modal-ok");
  const cancelBtn = $("#modal-cancel");
  okBtn.classList.remove("hidden");
  cancelBtn.classList.remove("hidden");
  const done = build(body, okBtn, cancelBtn, closeModal);
  if (done) okBtn.classList.add("hidden");
  $("#modal").classList.remove("hidden");
}

function closeModal() {
  $("#modal").classList.add("hidden");
  if (modalResolve) { modalResolve(true); modalResolve = null; }
}

$("#modal-close").addEventListener("click", closeModal);
$("#modal-cancel").addEventListener("click", closeModal);
$("#modal").addEventListener("click", (e) => {
  if (e.target.id === "modal") closeModal();
});

function confirmModal(msg, onYes) {
  openModal("Confirm", (body, ok, cancel, close) => {
    const p = document.createElement("p");
    p.textContent = msg;
    body.appendChild(p);
    ok.textContent = "Confirm";
    ok.addEventListener("click", () => { close(); onYes(); });
    cancel.textContent = "Cancel";
  });
}

function inputModal(title, label, initial, onOk) {
  openModal(title, (body, ok, cancel, close) => {
    const f = document.createElement("div");
    f.className = "field";
    const l = document.createElement("label");
    l.textContent = label;
    const i = document.createElement("input");
    i.type = "text";
    i.value = initial || "";
    f.append(l, i);
    body.appendChild(f);
    ok.addEventListener("click", () => {
      const v = i.value.trim();
      if (!v) return;
      close();
      onOk(v);
    });
    setTimeout(() => i.focus(), 30);
  });
}

function textModal(title, label, initial, onOk) {
  openModal(title, (body, ok, cancel, close) => {
    const f = document.createElement("div");
    f.className = "field";
    const l = document.createElement("label");
    l.textContent = label;
    const i = document.createElement("textarea");
    i.value = initial || "";
    f.append(l, i);
    body.appendChild(f);
    ok.addEventListener("click", () => { close(); onOk(i.value); });
    setTimeout(() => i.focus(), 30);
  });
}

/* ---------- API ---------- */

async function api(method, url, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(url, opts);
  let data = null;
  try { data = await res.json(); } catch (e) { /* non-JSON */ }
  if (!res.ok) {
    const msg = data
      ? (data.errors ? data.errors.join("\n") : (data.error || res.statusText))
      : res.statusText;
    const err = new Error(msg);
    err.status = res.status;
    err.data = data;
    throw err;
  }
  return data;
}

async function apiRawPut(url, text) {
  const res = await fetch(url, { method: "PUT", headers: { "Content-Type": "text/yaml" }, body: text });
  let data = null;
  try { data = await res.json(); } catch (e) { }
  if (!res.ok) {
    const err = new Error(data && data.errors ? data.errors.join("\n") : "invalid YAML");
    err.status = res.status;
    err.data = data;
    throw err;
  }
  return data;
}

/* ---------- E6 debug helpers ---------- */

function debugResultModal(title, data) {
  openModal(title, (body, ok, cancel, close) => {
    if (data.errors && data.errors.length) {
      const errDiv = document.createElement("div");
      errDiv.className = "debug-errors";
      errDiv.textContent = data.errors.map(e => typeof e === "string" ? e : (e.line ? `line ${e.line}: ${e.message}` : e.message || JSON.stringify(e))).join("\n");
      body.appendChild(errDiv);
    }
    if (data.ok !== undefined) {
      const p = document.createElement("p");
      p.textContent = data.ok ? "✓ Syntax OK" : "✗ Errors found (see below)";
      p.style.color = data.ok ? "var(--green, #22c55e)" : "var(--red, #ef4444)";
      body.appendChild(p);
    }
    if (data.results) {
      for (const r of data.results) {
        const sec = document.createElement("div");
        sec.className = "debug-result";
        const h = document.createElement("strong");
        h.textContent = (r.skipped ? "[skipped] " : "") + r.sql.substring(0, 80);
        sec.appendChild(h);
        if (r.error) {
          const errP = document.createElement("div");
          errP.style.color = "var(--red, #ef4444)";
          errP.textContent = "Error: " + r.error;
          sec.appendChild(errP);
        }
        if (r.columns && r.columns.length) {
          const tbl = document.createElement("table");
          tbl.className = "rows";
          const thead = document.createElement("thead");
          const hr = document.createElement("tr");
          for (const c of r.columns) {
            const th = document.createElement("th");
            th.textContent = c;
            hr.appendChild(th);
          }
          thead.appendChild(hr);
          tbl.appendChild(thead);
          const tbody = document.createElement("tbody");
          for (const row of (r.rows || [])) {
            const tr = document.createElement("tr");
            for (const v of row) {
              const td = document.createElement("td");
              td.textContent = v != null ? String(v) : "NULL";
              tr.appendChild(td);
            }
            tbody.appendChild(tr);
          }
          tbl.appendChild(tbody);
          sec.appendChild(tbl);
        }
        if (r.rows_affected !== undefined) {
          const p = document.createElement("p");
          p.className = "mono";
          p.textContent = "Rows affected: " + r.rows_affected + (r.last_insert_id != null ? "  Last insert ID: " + r.last_insert_id : "");
          sec.appendChild(p);
        }
        body.appendChild(sec);
      }
    }
    if (data.output && String(data.output).trim() !== "") {
      const sec = document.createElement("div");
      const label = document.createElement("div");
      label.className = "mono";
      label.textContent = "Output";
      const pre = document.createElement("pre");
      pre.textContent = String(data.output);
      pre.style.maxHeight = "300px";
      pre.style.overflow = "auto";
      sec.append(label, pre);
      body.appendChild(sec);
    }
    if (data.result) {
      const r = data.result;
      if (r.error) {
        const errP = document.createElement("div");
        errP.style.color = "var(--red, #ef4444)";
        errP.textContent = "Error: " + r.error;
        body.appendChild(errP);
      } else if (r.stdout) {
        const pre = document.createElement("pre");
        pre.textContent = r.stdout;
        pre.style.maxHeight = "300px";
        pre.style.overflow = "auto";
        body.appendChild(pre);
      }
      if (r.values) {
        const tbl = document.createElement("table");
        tbl.className = "rows";
        const thead = document.createElement("thead");
        const hr = document.createElement("tr");
        for (const k of Object.keys(r.values)) {
          const th = document.createElement("th");
          th.textContent = k;
          hr.appendChild(th);
        }
        thead.appendChild(hr);
        tbl.appendChild(thead);
        const tbody = document.createElement("tbody");
        const tr = document.createElement("tr");
        for (const v of Object.values(r.values)) {
          const td = document.createElement("td");
          td.textContent = v != null ? String(v) : "NULL";
          tr.appendChild(td);
        }
        tbody.appendChild(tr);
        tbl.appendChild(tbody);
        body.appendChild(tbl);
      }
    }
    if (!data.errors && !data.results && !data.result && !data.ok) {
      const p = document.createElement("p");
      p.textContent = JSON.stringify(data, null, 2);
      body.appendChild(p);
    }
    ok.classList.add("hidden");
  });
}

async function luaCheck(script) {
  try {
    const data = await api("POST", "/api/lua-check", { script });
    debugResultModal("Lua Check", data);
  } catch (e) {
    debugResultModal("Lua Check Error", { errors: [e.message] });
  }
}

async function luaRun(script, resourceName) {
  try {
    const data = await api("POST", "/api/lua-run", { script, resource: resourceName || "" });
    debugResultModal("Lua Run", data);
  } catch (e) {
    debugResultModal("Lua Run Error", { errors: [e.message] });
  }
}

async function sqlRun(sql, resourceName, actionName) {
  try {
    const data = await api("POST", "/api/sql-run", {
      body: sql,
      resource: resourceName || "",
      action: actionName || "",
      row_id: "1",
    });
    debugResultModal("SQL Run", data);
  } catch (e) {
    debugResultModal("SQL Run Error", { errors: [e.message] });
  }
}

/* ---------- form field helpers ---------- */

function fieldEl(grid, label) {
  const wrap = document.createElement("div");
  wrap.className = "field";
  const l = document.createElement("label");
  l.textContent = label;
  wrap.appendChild(l);
  grid.appendChild(wrap);
  return wrap;
}

function textField(grid, label, obj, key, readOnly) {
  const wrap = fieldEl(grid, label);
  const i = document.createElement("input");
  i.type = "text";
  i.value = obj[key] != null ? obj[key] : "";
  if (readOnly) {
    i.readOnly = true;
    i.classList.add("readonly");
  } else {
    i.addEventListener("change", () => {
      const v = i.value.trim();
      if (v === "") delete obj[key];
      else obj[key] = v;
      markDirty();
    });
  }
  wrap.appendChild(i);
  return i;
}

// selectField renders a string field as a dropdown over `options` plus an empty
// "—" entry that clears the key (letting the generator's default apply). A
// current value missing from the options is kept as an extra entry so
// hand-written values are never silently dropped.
function selectField(grid, label, obj, key, options, readOnly, onChange) {
  const wrap = fieldEl(grid, label);
  const s = document.createElement("select");
  const blank = document.createElement("option");
  blank.value = "";
  blank.textContent = "—";
  s.appendChild(blank);

  const current = obj[key] != null ? String(obj[key]) : "";
  const opts = [...options];
  if (current && !opts.includes(current)) opts.unshift(current);
  for (const o of opts) {
    const opt = document.createElement("option");
    opt.value = o;
    opt.textContent = o;
    s.appendChild(opt);
  }
  s.value = current;

  if (readOnly) {
    s.disabled = true;
    s.classList.add("readonly");
  } else {
    s.addEventListener("change", () => {
      if (s.value === "") delete obj[key];
      else obj[key] = s.value;
      markDirty();
      if (onChange) onChange();
    });
  }
  wrap.appendChild(s);
  return s;
}

// policyField renders a pipe-separated RBAC policy as a multi-select of
// configured roles. When no roles are defined yet it falls back to a plain
// text input.
function policyField(grid, label, obj, key, roles, readOnly) {
  const wrap = fieldEl(grid, label);
  const hint = document.createElement("div");
  hint.className = "mono";
  hint.style.fontSize = "11px";
  hint.style.color = "var(--text-faint)";
  hint.textContent = "Pipe-separated roles, e.g. admin|manager";
  wrap.appendChild(hint);

  if (readOnly || roles.length === 0) {
    const i = document.createElement("input");
    i.type = "text";
    i.value = obj[key] != null ? obj[key] : "";
    if (readOnly) i.readOnly = true;
    else {
      i.addEventListener("change", () => {
        const v = i.value.trim();
        if (v === "") delete obj[key];
        else obj[key] = v;
        markDirty();
      });
    }
    wrap.appendChild(i);
    return i;
  }

  const s = document.createElement("select");
  s.multiple = true;
  s.size = Math.min(roles.length, 4);
  const current = (obj[key] || "").split("|").map((r) => r.trim()).filter(Boolean);
  for (const role of roles) {
    const o = document.createElement("option");
    o.value = role;
    o.textContent = role;
    if (current.includes(role)) o.selected = true;
    s.appendChild(o);
  }
  if (readOnly) {
    s.disabled = true;
    s.classList.add("readonly");
  } else {
    s.addEventListener("change", () => {
      const vals = Array.from(s.selectedOptions).map((o) => o.value);
      obj[key] = vals.length ? vals.join("|") : "";
      markDirty();
    });
  }
  wrap.appendChild(s);
  return s;
}

function numField(grid, label, obj, key, readOnly) {
  const wrap = fieldEl(grid, label);
  const i = document.createElement("input");
  i.type = "number";
  i.value = obj[key] != null ? obj[key] : "";
  if (readOnly) {
    i.readOnly = true;
    i.classList.add("readonly");
  } else {
    i.addEventListener("change", () => {
      if (i.value === "") delete obj[key];
      else obj[key] = parseInt(i.value, 10);
      markDirty();
    });
  }
  wrap.appendChild(i);
  return i;
}

function colorField(grid, label, obj, key, fallback, readOnly) {
  const wrap = fieldEl(grid, label);
  const row = document.createElement("div");
  row.className = "color-field";
  const swatch = document.createElement("input");
  swatch.type = "color";
  const text = document.createElement("input");
  text.type = "text";
  const cur = /^#[0-9a-fA-F]{6}$/.test(obj[key] || "") ? obj[key] : (fallback || "#6366f1");
  swatch.value = cur;
  text.value = cur;
  if (readOnly) {
    swatch.disabled = true;
    text.readOnly = true;
    swatch.classList.add("readonly");
    text.classList.add("readonly");
  } else {
    swatch.addEventListener("change", () => {
      obj[key] = swatch.value;
      text.value = swatch.value;
      markDirty();
    });
    text.addEventListener("change", () => {
      const v = text.value.trim();
      if (/^#[0-9a-fA-F]{6}$/.test(v)) {
        obj[key] = v.toLowerCase();
        swatch.value = obj[key];
        markDirty();
      }
    });
  }
  row.append(swatch, text);
  wrap.appendChild(row);
  return wrap;
}

function boolField(grid, label, obj, key, readOnly) {
  const wrap = document.createElement("div");
  wrap.className = "checkbox-row field";
  const cb = document.createElement("input");
  cb.type = "checkbox";
  cb.id = "cb-" + key + "-" + Math.random().toString(36).slice(2, 7);
  cb.checked = !!obj[key];
  if (readOnly) {
    cb.disabled = true;
    cb.classList.add("readonly");
  } else {
    cb.addEventListener("change", () => { obj[key] = cb.checked; markDirty(); });
  }
  const l = document.createElement("label");
  l.htmlFor = cb.id;
  l.textContent = label;
  wrap.append(cb, l);
  grid.appendChild(wrap);
  return cb;
}

function selectField(grid, label, obj, key, options, { allowEmpty } = {}, readOnly) {
  const wrap = fieldEl(grid, label);
  const s = document.createElement("select");
  if (allowEmpty) {
    const o = document.createElement("option");
    o.value = "";
    o.textContent = "—";
    s.appendChild(o);
  }
  for (const opt of options) {
    const o = document.createElement("option");
    o.value = opt;
    o.textContent = opt;
    s.appendChild(o);
  }
  s.value = obj[key] != null ? obj[key] : "";
  if (readOnly) {
    s.disabled = true;
    s.classList.add("readonly");
  } else {
    s.addEventListener("change", () => {
      if (s.value === "") delete obj[key];
      else obj[key] = s.value;
      markDirty();
    });
  }
  wrap.appendChild(s);
  return s;
}

function stringListField(grid, label, arrObj, key, readOnly) {
  const wrap = fieldEl(grid, label);
  const i = document.createElement("input");
  i.type = "text";
  const cur = arrObj[key] || [];
  i.value = cur.join(", ");
  i.placeholder = "comma-separated";
  if (readOnly) {
    i.readOnly = true;
    i.classList.add("readonly");
  } else {
    i.addEventListener("change", () => {
      const vals = i.value.split(",").map((s) => s.trim()).filter(Boolean);
      if (vals.length) arrObj[key] = vals;
      else delete arrObj[key];
      markDirty();
    });
  }
  wrap.appendChild(i);
  return i;
}

/* ---------- icon options (all names from the generator's iconSVG / iconNav / actionIcon) ---------- */

const ICON_OPTIONS = [
  { name: "users", label: "Users" },
  { name: "chart", label: "Chart" },
  { name: "dollar", label: "Dollar" },
  { name: "check", label: "Check" },
  { name: "clock", label: "Clock" },
  { name: "cog", label: "Cog / Settings" },
  { name: "bell", label: "Bell" },
  { name: "home", label: "Home" },
  { name: "mail", label: "Mail" },
  { name: "lock", label: "Lock" },
  { name: "folder", label: "Folder" },
  { name: "user", label: "User" },
  { name: "settings", label: "Settings (sliders)" },
  { name: "document", label: "Document" },
  { name: "shield", label: "Shield" },
  { name: "layout", label: "Layout" },
  { name: "list", label: "List" },
  { name: "grid", label: "Grid" },
  { name: "user-nav", label: "User (nav)" },
  { name: "report", label: "Report" },
  { name: "archive", label: "Archive" },
  { name: "play", label: "Play" },
  { name: "pause", label: "Pause" },
  { name: "trash", label: "Trash" },
  { name: "download", label: "Download" },
  { name: "send", label: "Send" },
  { name: "star", label: "Star" },
  { name: "refresh", label: "Refresh" },
  { name: "x", label: "X / Close" },
  { name: "edit", label: "Edit" },
  { name: "save", label: "Save" },
  { name: "cancel", label: "Cancel" },
  { name: "arrow-right", label: "Arrow right" },
  { name: "plus", label: "Plus" },
];

/* ---------- icon images (SVG path data, mirrors the generator's iconSVG / iconNav / actionIcon) ---------- */
const ICON_SVGS = {
  users: "M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z",
  chart: "M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z",
  dollar: "M12 8c-1.657 0-3 .895-3 2s1.343 2 3 2 3 .895 3 2-1.343 2-3 2m0-8c1.11 0 2.08.402 2.599 1M12 8V7m0 1v8m0 0v1m0-1c-1.11 0-2.08-.402-2.599-1M21 12a9 9 0 11-18 0 9 9 0 0118 0z",
  check: "M5 13l4 4L19 7",
  clock: "M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z",
  cog: "M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z M15 12a3 3 0 11-6 0 3 3 0 016 0z",
  bell: "M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9",
  home: "M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6",
  mail: "M3 8l7.89 5.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z",
  lock: "M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z",
  folder: "M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z",
  user: "M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z",
  settings: "M12 6V4m0 2a2 2 0 100 4m0-4a2 2 0 110 4m-6 8a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4m6 6v10m6-2a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4",
  document: "M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z",
  shield: "M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z",
  layout: "M3 10h18M3 14h18m-9-4v8m-7 0h14a2 2 0 002-2V8a2 2 0 00-2-2H5a2 2 0 00-2 2v8a2 2 0 002 2z",
  list: "M8 12h.01M12 12h.01M16 12h.01M9 16h.01M13 16h.01M15 20h.01M21 12h.01M3 12h.01M3 16h.01M21 16h.01M16 20h.01M12 8h.01M8 20h.01M12 4h.01M16 20h.01M20 16h.01M20 12h.01M4 4h.01M4 8h.01M4 20h.01",
  grid: "M4 6a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2V6zM14 6a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2V6zM4 16a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2v-2zM14 16a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2v-2z",
  "user-nav": "M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z",
  report: "M9 17v-2m3 2v-4m3 4v-6m2 10H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z",
  archive: "M5 8h14M5 8a2 2 0 110-4h14a2 2 0 110 4M7 8v10a2 2 0 002 2h6a2 2 0 002-2V8m-9 4h6",
  play: "M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664zM21 12a9 9 0 11-18 0 9 9 0 0118 0z",
  pause: "M10 9v6m4-6v6m7-3a9 9 0 11-18 0 9 9 0 0118 0z",
  trash: "M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16",
  download: "M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4",
  send: "M12 19l9 2-9-18-9 18 9-2zm0 0v-8",
  star: "M11.049 2.927c.3-.921 1.603-.921 1.902 0l1.519 4.674a1 1 0 00.95.69h4.915c.969 0 1.371 1.24.588 1.81l-3.976 2.888a1 1 0 00-.363 1.118l1.518 4.674c.3.922-.755 1.688-1.538 1.118l-3.976-2.888a1 1 0 00-1.176 0l-3.976 2.888c-.783.57-1.838-.196-1.538-1.118l1.518-4.674a1 1 0 00-.363-1.118l-3.976-2.888c-.783-.57-.38-1.81.588-1.81h4.914a1 1 0 00.951-.69l1.519-4.674z",
  refresh: "M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15",
  x: "M6 18L18 6M6 6l12 12",
  edit: "M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z",
  save: "M4 7v10c0 2.21 3.582 4 8 4s8-1.79 8-4V7M4 7c0 2.21 3.582 4 8 4s8-1.79 8-4M4 7c0-2.21-3.582-4-8-4S0 4.79 0 7z",
  cancel: "M8 8l8 8M8 16l8-8",
  "arrow-right": "M9 5l7 7-7 7",
  plus: "M12 4v16m8-8H4",
};

function iconSelectField(grid, label, obj, key, readOnly) {
  const wrap = fieldEl(grid, label);
  wrap.classList.add("icon-field-wrap");
  wrap.style.position = "relative"; // containing block for the absolutely positioned dropdown

  const trigger = document.createElement("button");
  trigger.type = "button";
  trigger.className = "icon-trigger";
  trigger.setAttribute("aria-haspopup", "listbox");
  trigger.setAttribute("aria-expanded", "false");

  const lead = document.createElement("span");
  lead.className = "icon-trigger-lead";
  const svgWrap = document.createElement("span");
  svgWrap.className = "icon-trigger-svg";
  const nameText = document.createElement("span");
  nameText.className = "icon-trigger-name";
  const chev = document.createElement("span");
  chev.className = "icon-chevron";
  chev.textContent = "▾";

  lead.append(svgWrap, nameText);
  trigger.append(lead, chev);
  if (readOnly) {
    trigger.disabled = true;
    trigger.classList.add("readonly");
  }

  const panel = document.createElement("div");
  panel.className = "icon-dropdown";
  panel.setAttribute("role", "listbox");

  const rows = [];

  function svgEl(name) {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("width", "16");
    svg.setAttribute("height", "16");
    const d = ICON_SVGS[name];
    if (d) {
      const p = document.createElementNS("http://www.w3.org/2000/svg", "path");
      p.setAttribute("stroke", "currentColor");
      p.setAttribute("stroke-linecap", "round");
      p.setAttribute("stroke-linejoin", "round");
      p.setAttribute("stroke-width", "2");
      p.setAttribute("fill", "none");
      p.setAttribute("d", d);
      svg.appendChild(p);
    }
    return svg;
  }

  function setSelection(name) {
    if (readOnly) return;
    if (name === "") delete obj[key];
    else obj[key] = name;
    markDirty();
    renderTrigger();
    rows.forEach((r) => {
      const active = r.dataset.name === name;
      r.classList.toggle("icon-selected", active);
      r.setAttribute("aria-selected", active ? "true" : "false");
    });
    highlight = rows.findIndex((r) => r.dataset.name === name);
    refreshHighlight();
    close();
  }

  function renderTrigger() {
    const v = obj[key] || "";
    svgWrap.innerHTML = "";
    if (v && ICON_SVGS[v]) {
      svgWrap.appendChild(svgEl(v));
      nameText.textContent = v;
      nameText.className = "icon-trigger-name";
    } else {
      nameText.textContent = "— none —";
      nameText.className = "icon-trigger-name empty";
    }
  }

  function addRow(name, label) {
    const row = document.createElement("div");
    row.className = "icon-option";
    row.setAttribute("role", "option");
    row.dataset.name = name;
    row.appendChild(svgEl(name));
    const t = document.createElement("span");
    t.className = "icon-name" + (name === "" ? " empty" : "");
    t.textContent = label;
    row.appendChild(t);
    row.addEventListener("mousedown", (e) => e.preventDefault()); // keep focus on trigger
    row.addEventListener("click", () => setSelection(name));
    panel.appendChild(row);
    rows.push(row);
  }

  addRow("", "— none —");
  const sep = document.createElement("div");
  sep.className = "icon-divider";
  panel.appendChild(sep);
  for (const ic of ICON_OPTIONS) addRow(ic.name, ic.label);

  let highlight = rows.findIndex((r) => r.dataset.name === (obj[key] || ""));
  function refreshHighlight() {
    rows.forEach((r, i) => {
      const active = i === highlight;
      r.classList.toggle("icon-highlight", active);
      if (!active) r.classList.toggle("icon-selected", r.dataset.name === (obj[key] || ""));
      r.setAttribute("aria-selected", active ? "true" : "false");
    });
    if (highlight >= 0) {
      const r = rows[highlight];
      panel.scrollTop = Math.max(0, r.offsetTop - panel.clientHeight + r.offsetHeight);
    }
  }

  function open() {
    panel.classList.add("open");
    trigger.setAttribute("aria-expanded", "true");
    highlight = rows.findIndex((r) => r.dataset.name === (obj[key] || ""));
    refreshHighlight();
    document.addEventListener("mousedown", onDocMouseDown);
  }
  function close() {
    panel.classList.remove("open");
    trigger.setAttribute("aria-expanded", "false");
    document.removeEventListener("mousedown", onDocMouseDown);
  }
  function onDocMouseDown(e) {
    if (!wrap.contains(e.target)) close();
  }

  trigger.addEventListener("click", (e) => {
    e.stopPropagation();
    panel.classList.contains("open") ? close() : open();
  });
  trigger.addEventListener("keydown", (e) => {
    if (e.key === "Enter" || e.key === " ") { e.preventDefault(); open(); }
    else if (e.key === "Escape") close();
  });
  panel.addEventListener("keydown", (e) => {
    if (e.key === "Escape") { e.preventDefault(); close(); trigger.focus(); }
    else if (e.key === "ArrowDown") { e.preventDefault(); highlight = Math.min(highlight + 1, rows.length - 1); refreshHighlight(); }
    else if (e.key === "ArrowUp") { e.preventDefault(); highlight = Math.max(highlight - 1, 0); refreshHighlight(); }
    else if (e.key === "Enter" || e.key === " ") { e.preventDefault(); if (rows[highlight]) setSelection(rows[highlight].dataset.name); close(); trigger.focus(); }
    else if (e.key === "Home") { e.preventDefault(); highlight = 0; refreshHighlight(); }
    else if (e.key === "End") { e.preventDefault(); highlight = rows.length - 1; refreshHighlight(); }
  });

  wrap.append(trigger, panel);
  renderTrigger();
  refreshHighlight();
  return trigger;
}

/* ---------- image field (logo / favicon) ---------- */

function imageField(grid, label, obj, key) {
  const wrap = fieldEl(grid, label);
  wrap.classList.add("image-field-wrap");

  const input = document.createElement("input");
  input.type = "file";
  input.accept = "image/jpeg,image/png";

  const preview = document.createElement("div");
  preview.className = "image-preview";
  preview.style.marginTop = "8px";
  if (obj[key]) {
    const img = document.createElement("img");
    img.src = obj[key];
    img.style.maxWidth = "160px";
    img.style.maxHeight = "160px";
    img.style.border = "1px solid var(--border)";
    img.style.borderRadius = "6px";
    preview.appendChild(img);
  }

  const hint = document.createElement("div");
  hint.className = "mono";
  hint.textContent = "Accepted formats: JPEG (.jpg/.jpeg), PNG (.png)";

  input.addEventListener("change", (e) => {
    const file = e.target.files[0];
    if (!file) return;
    if (file.type !== "image/jpeg" && file.type !== "image/png") {
      toast("Please select a JPEG or PNG image", "error");
      return;
    }
    const reader = new FileReader();
    reader.onload = (ev) => {
      obj[key] = ev.target.result; // data URL
      preview.innerHTML = "";
      const img = document.createElement("img");
      img.src = ev.target.result;
      img.style.maxWidth = "160px";
      img.style.maxHeight = "160px";
      img.style.border = "1px solid var(--border)";
      img.style.borderRadius = "6px";
      preview.appendChild(img);
      markDirty();
    };
    reader.readAsDataURL(file);
  });

  wrap.append(input, preview, hint);
  return input;
}

/* ---------- generic collection editor ---------- */

/* ---- Embedded Lua tokenizer + highlighted textarea for script: bodies ---- */
const LUA_KW = new Set("and break do else elseif end false for function goto if in local nil not or repeat return then true until while".split(" "));
function escHtml(s) {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}
function luaHighlight(src) {
  let out = "";
  let i = 0;
  const n = src.length;
  const span = (cls, s) => `<span class="${cls}">${s}</span>`;
  while (i < n) {
    const c = src[i];
    if (c === "-" && src[i + 1] === "-" && src[i + 2] === "[") {
      const end = src.indexOf("]]", i + 3);
      const j = end < 0 ? n : end + 2;
      out += span("tk-c", escHtml(src.slice(i, j)));
      i = j;
      continue;
    }
    if (c === "-" && src[i + 1] === "-") {
      let j = src.indexOf("\n", i);
      if (j < 0) j = n;
      out += span("tk-c", escHtml(src.slice(i, j)));
      i = j;
      continue;
    }
    if (c === "[" && src[i + 1] === "[") {
      const end = src.indexOf("]]", i + 2);
      const j = end < 0 ? n : end + 2;
      out += span("tk-s", escHtml(src.slice(i, j)));
      i = j;
      continue;
    }
    if (c === "'" || c === '"') {
      let j = i + 1;
      while (j < n && src[j] !== c) {
        if (src[j] === "\\") j++;
        j++;
      }
      j = Math.min(n, j + 1);
      out += span("tk-s", escHtml(src.slice(i, j)));
      i = j;
      continue;
    }
    if (/[0-9]/.test(c) || (c === "." && /[0-9]/.test(src[i + 1] || ""))) {
      let j = i;
      while (j < n && /[0-9a-fA-FxX._]/.test(src[j])) j++;
      out += span("tk-n", escHtml(src.slice(i, j)));
      i = j;
      continue;
    }
    if (/[A-Za-z_]/.test(c)) {
      let j = i;
      while (j < n && /[A-Za-z0-9_]/.test(src[j])) j++;
      const word = src.slice(i, j);
      out += LUA_KW.has(word) ? span("tk-k", escHtml(word)) : escHtml(word);
      i = j;
      continue;
    }
    out += escHtml(c);
    i++;
  }
  return out;
}

function luaTextArea(value, onChange) {
  const wrap = document.createElement("div");
  wrap.className = "lua-field";
  const view = document.createElement("pre");
  view.className = "lua-view";
  view.innerHTML = luaHighlight(value);
  const ta = document.createElement("textarea");
  ta.spellcheck = false;
  ta.value = value;
  ta.className = "lua-edit";
  ta.addEventListener("input", () => {
    view.innerHTML = luaHighlight(ta.value);
    onChange(ta.value);
  });
  ta.addEventListener("scroll", () => {
    view.scrollTop = ta.scrollTop;
    view.scrollLeft = ta.scrollLeft;
  });
  wrap.append(view, ta);
  return wrap;
}

// datalistSeq keeps every combobox datalist id unique within the page.
let datalistSeq = 0;

function collectionEditor(container, items, schema, opts = {}) {
  const wrap = document.createElement("div");
  wrap.className = "table-wrap";
  container.appendChild(wrap);

  const readOnly = !!opts.readOnly;

  function cellInput(s, item, onChange) {
    const set = (v) => { item[s.key] = v; onChange(); };
    if (s.type === "lua") {
      const el = luaTextArea(item[s.key] != null ? item[s.key] : "", (v) => {
        if (v.trim() === "") delete item[s.key];
        else item[s.key] = v;
        onChange();
      });
      if (readOnly) el.readOnly = true;
      return el;
    }
    if (s.type === "bool") {
      const cb = document.createElement("input");
      cb.type = "checkbox";
      cb.checked = !!item[s.key];
      if (readOnly) {
        cb.disabled = true;
        cb.classList.add("readonly");
      } else {
        cb.addEventListener("change", () => set(cb.checked));
      }
      return cb;
    }
    if (s.type === "select") {
      const el = document.createElement("select");
      const none = document.createElement("option");
      none.value = "";
      none.textContent = "—";
      el.appendChild(none);
      for (const o of s.options || []) {
        const opt = document.createElement("option");
        opt.value = o;
        opt.textContent = o;
        el.appendChild(opt);
      }
      el.value = item[s.key] != null ? item[s.key] : "";
      if (readOnly) {
        el.disabled = true;
        el.classList.add("readonly");
      } else {
        el.addEventListener("change", () => {
          if (el.value === "") delete item[s.key];
          else item[s.key] = el.value;
          onChange();
        });
      }
      return el;
    }
    if (s.type === "combo") {
      // Free-text input with a datalist dropdown of schema column names.
      const wrap = document.createElement("span");
      wrap.className = "combo-cell";
      const el = document.createElement("input");
      el.type = "text";
      el.value = item[s.key] != null ? item[s.key] : "";
      const listId = "dl-" + s.key + "-" + (++datalistSeq);
      el.setAttribute("list", listId);
      const dl = document.createElement("datalist");
      dl.id = listId;
      for (const o of s.options || []) {
        const opt = document.createElement("option");
        opt.value = o;
        dl.appendChild(opt);
      }
      if (readOnly) {
        el.readOnly = true;
        el.classList.add("readonly");
      } else {
        el.addEventListener("change", () => {
          const v = el.value.trim();
          if (v === "") delete item[s.key];
          else item[s.key] = v;
          onChange();
        });
      }
      wrap.append(el, dl);
      return wrap;
    }
    const el = document.createElement("input");
    el.type = s.type === "number" ? "number" : "text";
    el.value = item[s.key] != null ? item[s.key] : "";
    if (readOnly) {
      el.readOnly = true;
      el.classList.add("readonly");
    } else {
      el.addEventListener("change", () => {
        let v = el.value;
        if (s.type === "number") v = v === "" ? undefined : parseInt(v, 10);
        else v = v.trim() === "" ? undefined : v;
        if (v === undefined) delete item[s.key];
        else item[s.key] = v;
        onChange();
      });
    }
    return el;
  }

  function render() {
    wrap.innerHTML = "";
    if (items.length === 0) {
      const p = document.createElement("p");
      p.className = "mono";
      p.textContent = "No entries.";
      wrap.appendChild(p);
    }
    const table = document.createElement("table");
    table.className = "rows";
    const thead = document.createElement("thead");
    const hr = document.createElement("tr");
    for (const s of schema) {
      const th = document.createElement("th");
      th.textContent = s.label || s.key;
      hr.appendChild(th);
    }
    const last = document.createElement("th");
    hr.appendChild(last);
    thead.appendChild(hr);
    table.appendChild(thead);

    const tbody = document.createElement("tbody");
    items.forEach((item, idx) => {
      const onChange = () => { markDirty(); if (opts.onChange) opts.onChange(); };
      if (opts.renderRow) {
        const el = opts.renderRow(item, idx, onChange, () => {
          items.splice(idx, 1); markDirty(); render();
          if (opts.onChange) opts.onChange();
        });
        tbody.appendChild(el);
      } else {
        const tr = document.createElement("tr");
        for (const s of schema) {
          const td = document.createElement("td");
          td.appendChild(cellInput(s, item, onChange));
          tr.appendChild(td);
        }
        const td = document.createElement("td");
        td.className = "row-actions";
        const extraBtns = typeof opts.rowActions === "function" ? opts.rowActions(item, idx) : [];
        for (const b of extraBtns) td.appendChild(b);
        const jsonBtn = mkButton("⋯", () => editRowJSON(item));
        if (readOnly) {
          jsonBtn.disabled = true;
          jsonBtn.classList.add("readonly");
        }
        const delBtn = mkButton("✕", () => {
          items.splice(idx, 1);
          markDirty();
          render();
          if (opts.onChange) opts.onChange();
        });
        if (readOnly) {
          delBtn.disabled = true;
          delBtn.classList.add("readonly");
        }
        td.append(jsonBtn, delBtn);
        tr.appendChild(td);
        tbody.appendChild(tr);
      }
    });
    table.appendChild(tbody);
    wrap.appendChild(table);
  }

  function editRowJSON(item) {
    if (readOnly) return;
    openModal(opts.jsonTitle || "Edit entry (JSON, YAML field names)", (body, ok, cancel, close) => {
      const ta = document.createElement("textarea");
      ta.value = JSON.stringify(item, null, 2);
      body.appendChild(ta);
      const err = document.createElement("div");
      err.style.color = "var(--red)";
      body.appendChild(err);
      ok.addEventListener("click", () => {
        let v;
        try { v = JSON.parse(ta.value); } catch (e) { err.textContent = "Invalid JSON: " + e.message; return; }
        if (!v || typeof v !== "object" || Array.isArray(v)) { err.textContent = "Expected a JSON object"; return; }
        for (const k of Object.keys(item)) delete item[k];
        Object.assign(item, v);
        markDirty();
        close();
        render();
        if (opts.onChange) opts.onChange();
      });
      cancel.textContent = "Close";
    });
  }

  render();
  return { refresh: render };
}

/* ---------- tabs ---------- */

/* Sidebar navigation: sections may group page links.
   Validate / Preview / Raw YAML moved to topbar buttons. */
const NAV = [
  { section: "Settings", items: [
    ["panel", "Panel"],
    ["connections", "Connections"],
    ["auth", "Auth"],
  ]},
  ["navigation", "Navigation"],
  ["resources", "Dataviews"],
  ["pages", "Pages"],
  ["schema", "Database"],
];

/* groups whose section items are collapsed; defaults to open */
const sidebarCollapsed = new Set();

function renderTabs() {
  const nav = $("#tabs");
  nav.innerHTML = "";

  /* ensure the section containing the current page stays expanded */
  for (const entry of NAV) {
    if (!Array.isArray(entry) && entry.items.some(([id]) => id === state.page)) {
      sidebarCollapsed.delete(entry.section);
    }
  }

  for (const entry of NAV) {
    if (Array.isArray(entry)) {
      const [id, label] = entry;
      const t = document.createElement("button");
      t.className = "tab" + (state.page === id ? " active" : "");
      t.textContent = label;
      if (id === "resources" && state.config && state.config.resources && state.config.resources.length) {
        const b = document.createElement("span");
        b.className = "badge";
        b.textContent = state.config.resources.length;
        t.appendChild(b);
      }
      t.addEventListener("click", () => switchPage(id));
      nav.appendChild(t);
    } else {
      const li = document.createElement("div");
      li.className = "sidebar-section" + (sidebarCollapsed.has(entry.section) ? " collapsed" : "");

      const head = document.createElement("div");
      head.className = "sidebar-section-head" + (entry.items.some(([id]) => id === state.page) ? " active" : "");
      head.addEventListener("click", () => {
        if (sidebarCollapsed.has(entry.section)) sidebarCollapsed.delete(entry.section);
        else sidebarCollapsed.add(entry.section);
        renderTabs();
      });
      const chev = document.createElement("span");
      chev.className = "sidebar-chevron";
      chev.textContent = sidebarCollapsed.has(entry.section) ? "▸" : "▾";
      const title = document.createElement("span");
      title.className = "sidebar-section-title";
      title.textContent = entry.section;
      head.append(title, chev);
      li.appendChild(head);

      if (!sidebarCollapsed.has(entry.section)) {
        const sub = document.createElement("div");
        sub.className = "sidebar-section-items";
        for (const [id, label] of entry.items) {
          const t = document.createElement("button");
          t.className = "tab tab-sub" + (state.page === id ? " active" : "");
          t.textContent = label;
          if (id === "resources" && state.config && state.config.resources && state.config.resources.length) {
            const b = document.createElement("span");
            b.className = "badge";
            b.textContent = state.config.resources.length;
            t.appendChild(b);
          }
          t.addEventListener("click", () => switchPage(id));
          sub.appendChild(t);
        }
        li.appendChild(sub);
      }
      nav.appendChild(li);
    }
  }
}

function syncTopbar() {
  for (const id of ["validate", "preview", "raw"]) {
    const el = $("#btn-" + id);
    if (el) el.classList.toggle("active", state.page === id);
  }
}

function switchPage(id) {
  // Remember which dataview we came from so Raw YAML can scroll to it.
  if (id === "raw" && state.resource) {
    state.rawScrollTarget = state.resource;
  }
  state.page = id;
  state.resource = null;
  state.pageName = null;
  renderTabs();
  syncTopbar();
  renderPage();
}

function renderPage() {
  const fns = {
    panel: pagePanel,
    connections: pageConnections,
    auth: pageAuth,
    navigation: pageNavigation,
    resources: pageResources,
    pages: pagePages,
    schema: pageSchema,
    validate: pageValidate,
    preview: pagePreview,
    raw: pageRaw,
  };
  (fns[state.page] || pagePanel)();
}

/* ---------- page: Panel ---------- */

function pagePanel() {
  const c = state.config;
  const root = content();
  h2(root, "Panel");
  const card = cardEl(root);
  const g = gridWrap(card);

  textField(g, "Name", c.panel, "name");
  textField(g, "ID", c.panel, "id");
  textField(g, "Path", c.panel, "path");
  imageField(g, "Logo", c.panel.brand, "logo");
  imageField(g, "Favicon", c.panel.brand, "favicon");
  colorField(g, "Primary color", c.panel.brand.colors, "primary", "#6366f1");
  colorField(g, "Secondary color", c.panel.brand.colors, "secondary", "#8b5cf6");
  numField(g, "Sidebar width", c.panel.layout.sidebar, "width");
  numField(g, "Collapsed width", c.panel.layout.sidebar, "collapsed_width");
  selectField(g, "Max content width", c.panel.layout, "max_content_width",
    ["none", "sm", "md", "lg", "xl", "2xl", "3xl", "4xl", "5xl", "6xl", "7xl"], { allowEmpty: true });
  textField(g, "Font family", c.panel.theme.font, "family");
  textField(g, "Mono font", c.panel.theme.font, "mono");
  const checks = document.createElement("div");
  checks.className = "checkbox-group";
  boolField(checks, "Sidebar collapsible", c.panel.layout.sidebar, "collapsible");
  boolField(checks, "Topbar sticky", c.panel.layout.topbar, "sticky");
  boolField(checks, "Dark mode", c.panel.theme, "dark_mode");
  card.appendChild(checks);
}

/* ---------- page: Connections ---------- */

function pageConnections() {
  const c = state.config;
  if (!c.connections) c.connections = {};
  const root = content();
  h2(root, "Connections");
  for (const name of Object.keys(c.connections)) {
    const conn = c.connections[name];
    const card = cardEl(root);
    const head = document.createElement("div");
    head.className = "toolbar";
    const t = document.createElement("h3");
    t.style.margin = "0";
    t.textContent = name;
    head.appendChild(t);
    const spacer = document.createElement("div");
    spacer.className = "spacer";
    head.appendChild(spacer);
    card.appendChild(head);

    const g = gridWrap(card);
    selectField(g, "Driver", conn, "driver", ["postgres", "sqlite", "sqlite3", "mssql", "sqlserver"]);
    textField(g, "DSN", conn, "dsn");
    if (!conn.pool) conn.pool = {};
    numField(g, "Max open", conn.pool, "max_open");
    numField(g, "Max idle", conn.pool, "max_idle");
    textField(g, "Conn lifetime (e.g. 30m)", conn.pool, "lifetime");
  }

}

/* ---------- page: Auth ---------- */

function pageAuth() {
  const a = state.config.auth;
  const root = content();
  h2(root, "Auth");
  const card = cardEl(root);
  const g = gridWrap(card);
  textField(g, "Guard", a, "guard");
  textField(g, "Provider", a, "provider");
  textField(g, "Auth table", a, "table");
  stringListField(g, "Login fields", a.login, "fields");
  textField(g, "Login redirect (fallback)", a.login, "redirect");
  if (!a.login.redirects) a.login.redirects = {};
  h3(root, "Role redirect targets");
  const redirectCard = cardEl(root);
  const redirectGrid = gridWrap(redirectCard);
  const rhint = document.createElement("p");
  rhint.className = "mono";
  rhint.textContent = "When set, a role redirects to its target after login instead of the fallback. Empty = start with no per-role overrides.";
  redirectGrid.appendChild(rhint);

  const rtable = document.createElement("table");
  rtable.className = "rows";
  const rthead = document.createElement("thead");
  const rhr = document.createElement("tr");
  ["Role", "Redirect to", ""].forEach((t) => {
    const th = document.createElement("th");
    th.textContent = t;
    rhr.appendChild(th);
  });
  rthead.appendChild(rhr);
  rtable.appendChild(rthead);
  const rtbody = document.createElement("tbody");
  rtable.appendChild(rtbody);
  redirectGrid.appendChild(rtable);

  const renderRedirects = () => {
    rtbody.innerHTML = "";
    const roles = (state.config.auth.roles || []).map((r) => r.name);
    for (const [role, path] of Object.entries(a.login.redirects)) {
      const tr = document.createElement("tr");

      const tdRole = document.createElement("td");
      const iRole = document.createElement("select");
      for (const r of roles) {
        const o = document.createElement("option");
        o.value = r;
        o.textContent = r;
        o.selected = r === role;
        iRole.appendChild(o);
      }
      // Allow free-text entry for roles not yet defined in auth.roles.
      const free = document.createElement("option");
      free.value = role;
      free.textContent = role + " (custom)";
      free.selected = true;
      iRole.appendChild(free);
      iRole.addEventListener("change", () => {
        const nr = iRole.value.trim();
        if (!nr) {
          delete a.login.redirects[role];
        } else if (nr !== role && nr !== role + " (custom)") {
          const p = a.login.redirects[role];
          delete a.login.redirects[role];
          a.login.redirects[nr] = p;
        }
        markDirty();
      });
      tdRole.appendChild(iRole);
      tr.appendChild(tdRole);

      const tdPath = document.createElement("td");
      const iPath = document.createElement("input");
      iPath.type = "text";
      iPath.value = path;
      iPath.placeholder = "/admin/dashboard";
      iPath.addEventListener("change", () => {
        const r = iRole.value.replace(/ \(custom\)$/, "").trim();
        const v = iPath.value.trim();
        if (r) {
          if (v) a.login.redirects[r] = v;
          else delete a.login.redirects[r];
        }
        markDirty();
      });
      tdPath.appendChild(iPath);
      tr.appendChild(tdPath);

      const tdAct = document.createElement("td");
      tdAct.className = "row-actions";
      const del = mkButton("✕", () => {
        delete a.login.redirects[role];
        markDirty();
        renderRedirects();
      });
      tdAct.appendChild(del);
      tr.appendChild(tdAct);
      rtbody.appendChild(tr);
    }
  };
  renderRedirects();

  const addBtn = mkButton("Add role", () => {
    const roles = (state.config.auth.roles || []).map((r) => r.name);
    const resources = (state.config.resources || []).map((r) => {
      const label = (r.label || r.name).toString();
      return { value: "/" + String(r.name).toLowerCase(), text: label };
    });
    const pages = (state.config.pages || []).map((p) => {
      const path = p.path || "/" + String(p.name).toLowerCase();
      return { value: path, text: String(p.name || path) };
    });

    openModal("Add role redirect", (body, ok, cancel, close) => {
      const f1 = document.createElement("div");
      f1.className = "field";
      const l1 = document.createElement("label");
      l1.textContent = "Role";
      const iRole = document.createElement("select");
      if (roles.length === 0) {
        const o = document.createElement("option");
        o.value = "";
        o.textContent = "(no roles defined — add one under Roles below)";
        o.disabled = true;
        iRole.appendChild(o);
      }
      for (const role of roles) {
        const o = document.createElement("option");
        o.value = role;
        o.textContent = role;
        iRole.appendChild(o);
      }
      f1.append(l1, iRole);
      body.appendChild(f1);

      const f2 = document.createElement("div");
      f2.className = "field";
      const l2 = document.createElement("label");
      l2.textContent = "Target type";
      const tSel = document.createElement("select");
      ["Resource", "Page", "Custom path"].forEach((t) => {
        const o = document.createElement("option");
        o.value = t;
        o.textContent = t;
        tSel.appendChild(o);
      });
      f2.append(l2, tSel);
      body.appendChild(f2);

      const f3 = document.createElement("div");
      f3.className = "field";
      const l3 = document.createElement("label");
      l3.textContent = "Target";
      const tSelEl = document.createElement("select");
      const cInput = document.createElement("input");
      cInput.type = "text";
      cInput.placeholder = "/admin/customers";
      cInput.style.display = "none";
      const fill = () => {
        tSelEl.innerHTML = "";
        if (tSel.value === "Resource") {
          for (const r of resources) {
            const o = document.createElement("option");
            o.value = r.value;
            o.textContent = r.text;
            tSelEl.appendChild(o);
          }
        } else if (tSel.value === "Page") {
          for (const p of pages) {
            const o = document.createElement("option");
            o.value = p.value;
            o.textContent = p.text;
            tSelEl.appendChild(o);
          }
        }
        tSelEl.style.display = tSel.value === "Custom path" ? "none" : "";
        cInput.style.display = tSel.value === "Custom path" ? "" : "none";
      };
      tSel.addEventListener("change", fill);
      fill();
      f3.append(l3, tSelEl, cInput);
      body.appendChild(f3);

      let resolving = false;
      ok.textContent = "Add";
      ok.addEventListener("click", () => {
        if (resolving) return;
        const role = iRole.value.trim();
        if (!role) { iRole.focus(); return; }
        let target = "";
        if (tSel.value === "Custom path") target = cInput.value.trim();
        else target = tSelEl.value;
        if (!target) { tSelEl.focus(); return; }
        resolving = true;
        a.login.redirects[role] = target;
        markDirty();
        renderRedirects();
        resolving = false;
        close();
      });
      setTimeout(() => iRole.focus(), 30);
    });
  });
  redirectGrid.appendChild(addBtn);

  /* ---------- Roles ---------- */
  if (!Array.isArray(a.roles)) a.roles = [];
  h3(root, "Roles");
  const rolesCard = cardEl(root);
  const rolesGrid = gridWrap(rolesCard);
  const rhint2 = document.createElement("p");
  rhint2.className = "mono";
  rhint2.textContent = "Roles are used for RBAC policies and for seeding demo users in the system DB. Each role gets a demo user {role}@demo.test.";
  rolesGrid.appendChild(rhint2);

  const rolesTable = document.createElement("table");
  rolesTable.className = "rows";
  const rolesThead = document.createElement("thead");
  const rolesHr = document.createElement("tr");
  ["Name", "Label", ""].forEach((t) => {
    const th = document.createElement("th");
    th.textContent = t;
    rolesHr.appendChild(th);
  });
  rolesThead.appendChild(rolesHr);
  rolesTable.appendChild(rolesThead);
  const rolesTbody = document.createElement("tbody");
  rolesTable.appendChild(rolesTbody);
  rolesGrid.appendChild(rolesTable);

  const renderRoles = () => {
    rolesTbody.innerHTML = "";
    for (const role of a.roles) {
      const tr = document.createElement("tr");

      const tdName = document.createElement("td");
      const iName = document.createElement("input");
      iName.type = "text";
      iName.value = role.name;
      iName.placeholder = "e.g. admin";
      iName.addEventListener("change", () => {
        const v = iName.value.trim();
        if (!v) return;
        role.name = v;
        markDirty();
        renderRoles();
      });
      tdName.appendChild(iName);
      tr.appendChild(tdName);

      const tdLabel = document.createElement("td");
      const iLabel = document.createElement("input");
      iLabel.type = "text";
      iLabel.value = role.label || "";
      iLabel.placeholder = "e.g. Administrator";
      iLabel.addEventListener("change", () => {
        role.label = iLabel.value.trim() || "";
        markDirty();
      });
      tdLabel.appendChild(iLabel);
      tr.appendChild(tdLabel);

      const tdAct = document.createElement("td");
      tdAct.className = "row-actions";
      const del = mkButton("✕", () => {
        const idx = a.roles.indexOf(role);
        if (idx >= 0) a.roles.splice(idx, 1);
        markDirty();
        renderRoles();
      });
      tdAct.appendChild(del);
      tr.appendChild(tdAct);
      rolesTbody.appendChild(tr);
    }
  };
  renderRoles();

  const addRoleBtn = mkButton("Add role", () => {
    openModal("Add role", (body, ok, cancel, close) => {
      const f1 = document.createElement("div");
      f1.className = "field";
      const l1 = document.createElement("label");
      l1.textContent = "Name";
      const iName = document.createElement("input");
      iName.type = "text";
      iName.placeholder = "e.g. admin";
      f1.append(l1, iName);
      body.appendChild(f1);

      const f2 = document.createElement("div");
      f2.className = "field";
      const l2 = document.createElement("label");
      l2.textContent = "Label";
      const iLabel = document.createElement("input");
      iLabel.type = "text";
      iLabel.placeholder = "e.g. Administrator";
      f2.append(l2, iLabel);
      body.appendChild(f2);

      ok.textContent = "Add";
      ok.addEventListener("click", () => {
        const name = iName.value.trim();
        if (!name) { iName.focus(); return; }
        if (a.roles.some((r) => r.name === name)) { iName.focus(); return; }
        a.roles.push({ name: name, label: iLabel.value.trim() });
        markDirty();
        renderRoles();
        close();
      });
      setTimeout(() => iName.focus(), 30);
    });
  });
  rolesGrid.appendChild(addRoleBtn);

  if (!a.login.rate_limit) a.login.rate_limit = {};
  h3(root, "Login rate limit");
  const card2 = cardEl(root);
  const g2 = gridWrap(card2);
  numField(g2, "Max attempts", a.login.rate_limit, "max_attempts");
  numField(g2, "Window seconds", a.login.rate_limit, "window_seconds");
  const hint = document.createElement("p");
  hint.className = "mono";
  hint.textContent = "max_attempts: 0 (or absent) disables rate limiting";
  root.appendChild(hint);
}

/* ---------- page: Navigation (tree) ---------- */

const NAV_ITEM_TYPES = ["resource", "page", "url"];
const NAV_ITEM_LABELS = { resource: "Dataview", page: "Page", url: "URL" };

/* groups whose items are collapsed; defaults to open */
const navCollapsed = new Set();

function navItemTarget(item) {
  if (item.resource) return "/" + String(item.resource).toLowerCase();
  if (item.page) return item.page;
  return item.url || "";
}

function navItemMissing(c, item) {
  if (item.resource) {
    const name = String(item.resource).toLowerCase();
    return !(c.resources || []).some((r) => String(r.name || "").toLowerCase() === name);
  }
  if (item.page) {
    return !(c.pages || []).some((p) => p.name === item.page);
  }
  return false;
}

function navItemModal(c, item, onSave) {
  const typeSel = document.createElement("select");
  for (const t of NAV_ITEM_TYPES) {
    const o = document.createElement("option");
    o.value = t;
    o.textContent = NAV_ITEM_LABELS[t] || t;
    typeSel.appendChild(o);
  }
  const cur = item.resource ? "resource" : item.page ? "page" : item.url ? "url" : (item.type || "resource");
  typeSel.value = NAV_ITEM_TYPES.includes(cur) ? cur : "resource";

  openModal("Edit nav item", (body, ok, cancel, close) => {
    const typeWrap = document.createElement("div");
    typeWrap.className = "field";
    const typeLabel = document.createElement("label");
    typeLabel.textContent = "Type";
    typeWrap.append(typeLabel, typeSel);
    body.appendChild(typeWrap);

    const resourceWrap = document.createElement("div");
    resourceWrap.className = "field";
    const resLabel = document.createElement("label");
    resLabel.textContent = "Dataview";
    const resourceSel = document.createElement("select");
    (c.resources || []).forEach((r) => {
      const o = document.createElement("option");
      o.value = r.name;
      o.textContent = r.name + (r.label && r.label !== r.name ? "  —  " + r.label : "");
      resourceSel.appendChild(o);
    });
    resourceSel.value = item.resource || "";
    resourceWrap.append(resLabel, resourceSel);

    const pageWrap = document.createElement("div");
    pageWrap.className = "field";
    const pageLabel = document.createElement("label");
    pageLabel.textContent = "Page";
    const pageIn = document.createElement("input");
    pageIn.type = "text";
    pageIn.value = item.page || "";
    pageWrap.append(pageLabel, pageIn);

    const urlWrap = document.createElement("div");
    urlWrap.className = "field";
    const urlLabel = document.createElement("label");
    urlLabel.textContent = "URL";
    const urlIn = document.createElement("input");
    urlIn.type = "text";
    urlIn.value = item.url || "";
    urlWrap.append(urlLabel, urlIn);

    const labelWrap = document.createElement("div");
    labelWrap.className = "field";
    const labelLabel = document.createElement("label");
    labelLabel.textContent = "Label (optional override)";
    const labelIn = document.createElement("input");
    labelIn.type = "text";
    labelIn.value = item.label || "";
    labelWrap.append(labelLabel, labelIn);

    const newTab = document.createElement("label");
    newTab.className = "checkbox-row";
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.checked = !!item.opens_in_new_tab;
    newTab.append(cb, document.createTextNode("Open in new tab"));
    body.append(resourceWrap, pageWrap, urlWrap, labelWrap, newTab);

    const err = document.createElement("div");
    err.style.color = "var(--red)";
    body.appendChild(err);

    const sync = () => {
      resourceWrap.style.display = typeSel.value === "resource" ? "" : "none";
      pageWrap.style.display = typeSel.value === "page" ? "" : "none";
      urlWrap.style.display = typeSel.value === "url" ? "" : "none";
    };
    typeSel.addEventListener("change", sync);
    sync();

    ok.addEventListener("click", () => {
      if (typeSel.value === "url" && !urlIn.value.trim()) { err.textContent = "URL is required for url items"; return; }
      if (typeSel.value === "resource" && !resourceSel.value) { err.textContent = "Pick a dataview"; return; }
      if (typeSel.value === "page" && !pageIn.value.trim()) { err.textContent = "Page is required"; return; }
      for (const k of Object.keys(item)) delete item[k];
      item.type = typeSel.value;
      if (typeSel.value === "resource") item.resource = resourceSel.value;
      else if (typeSel.value === "page") item.page = pageIn.value.trim();
      else item.url = urlIn.value.trim();
      if (labelIn.value.trim()) item.label = labelIn.value.trim();
      if (cb.checked) item.opens_in_new_tab = true;
      close();
      onSave();
    });
    cancel.textContent = "Cancel";
  });
}

function pageNavigation() {
  const c = state.config;
  if (!Array.isArray(c.navigation)) c.navigation = [];
  const root = content();
  h2(root, "Navigation");
  const hint = document.createElement("p");
  hint.className = "mono";
  hint.textContent = "Groups sort by their sort value. Click a group to expand/collapse; hover a row for actions.";
  root.appendChild(hint);

  const list = document.createElement("ul");
  list.className = "tree";
  root.appendChild(list);

  const rerender = () => pageNavigation();

  c.navigation.forEach((group, gi) => {
    if (!Array.isArray(group.items)) group.items = [];

    const li = document.createElement("li");
    li.className = "tree-group" + (navCollapsed.has(gi) ? " collapsed" : "");

    const head = document.createElement("div");
    head.className = "tree-group-head";
    head.addEventListener("click", () => {
      if (navCollapsed.has(gi)) navCollapsed.delete(gi);
      else navCollapsed.add(gi);
      li.classList.toggle("collapsed");
    });

    const chev = document.createElement("span");
    chev.className = "chevron";
    chev.textContent = "▾";

    const title = document.createElement("span");
    title.className = "tree-group-title";
    title.textContent = group.group || "(unnamed)";

    const meta = document.createElement("span");
    meta.className = "tree-group-meta";
    meta.textContent = (group.items || []).length + " item" + (group.items.length === 1 ? "" : "s") +
      (group.icon ? "  ·  " + group.icon : "");

    const actions = document.createElement("span");
    actions.className = "tree-actions";
    actions.addEventListener("click", (e) => e.stopPropagation());
    const editG = mkButton("Edit", () => {
      openModal("Edit group: " + (group.group || "(unnamed)"), (body, ok, cancel, close) => {
        const g = gridWrap(cardEl(body));
        textField(g, "Group name", group, "group");
        iconSelectField(g, "Icon", group, "icon");
        numField(g, "Sort", group, "sort");
        ok.addEventListener("click", () => { close(); markDirty(); rerender(); });
        cancel.textContent = "Cancel";
      });
    });
    const addI = mkButton("+ Item", () => {
      const item = { type: "resource" };
      group.items.push(item);
      markDirty();
      navItemModal(c, item, () => { rerender(); });
    });
    const delG = mkButton("✕", () => confirmModal(`Delete navigation group "${group.group}"?`, () => {
      c.navigation.splice(gi, 1);
      markDirty();
      rerender();
    }));

    /* move group up / down within navigation */
    const mvUpG = mkButton("▲", () => {
      if (gi === 0) return;
      [c.navigation[gi - 1], c.navigation[gi]] = [c.navigation[gi], c.navigation[gi - 1]];
      markDirty();
      rerender();
    });
    mvUpG.title = "Move group up";
    const mvDnG = mkButton("▼", () => {
      if (gi === c.navigation.length - 1) return;
      [c.navigation[gi], c.navigation[gi + 1]] = [c.navigation[gi + 1], c.navigation[gi]];
      markDirty();
      rerender();
    });
    mvDnG.title = "Move group down";
    actions.append(mvUpG, mvDnG, editG, addI, delG);

    head.append(chev, title, meta, actions);
    li.appendChild(head);

    const children = document.createElement("ul");
    children.className = "tree-children";
    group.items.forEach((item, ii) => {
      const itemLi = document.createElement("li");
      itemLi.className = "tree-item";

      const badge = document.createElement("span");
      badge.className = "type-badge";
      badge.textContent = item.type ? (NAV_ITEM_LABELS[item.type] || item.type) : (item.resource ? "Dataview" : item.page ? "Page" : "URL");

      const label = document.createElement("span");
      label.className = "tree-label";
      label.textContent = item.label || item.resource || item.page || item.url || "(unnamed)";

      const metaEl = document.createElement("span");
      metaEl.className = "tree-meta";
      metaEl.textContent = navItemTarget(item);

      const itActions = document.createElement("span");
      itActions.className = "tree-actions";
      const editI = mkButton("Edit", () => navItemModal(c, item, () => { markDirty(); rerender(); }));
      const mvI = mkButton("Move", () => {
        const seen = {};
        const opts = [];
        c.navigation.forEach((g2, g2i) => {
          if (g2i === gi) return;
          let name = g2.group || "(unnamed)";
          const n = (seen[name] = (seen[name] || 0) + 1);
          if (n > 1) name += " · #" + g2i;
          opts.push({ g: g2, name });
        });
        const doMove = (target) => {
          group.items.splice(ii, 1);
          if (!Array.isArray(target.items)) target.items = [];
          target.items.push(item);
          markDirty();
          rerender();
        };
        openModal("Move nav item", (body, ok, cancel, close) => {
          const f = document.createElement("div");
          f.className = "field";
          const l = document.createElement("label");
          l.textContent = "Move to group";
          const s = document.createElement("select");
          for (const o of opts) {
            const el = document.createElement("option");
            el.value = String(c.navigation.indexOf(o.g));
            el.textContent = o.name;
            s.appendChild(el);
          }
          const newOpt = document.createElement("option");
          newOpt.value = "__new__";
          newOpt.textContent = "→ New group…";
          s.appendChild(newOpt);
          f.append(l, s);
          body.appendChild(f);
          ok.addEventListener("click", () => {
            if (s.value === "__new__") {
              close();
              inputModal("New group", "Group name", group.group || "", (name) => {
                if (!name.trim()) { toast("Group name is required", "error"); return; }
                c.navigation.push({ group: name.trim(), items: [] });
                doMove(c.navigation[c.navigation.length - 1]);
              });
              return;
            }
            doMove(c.navigation[Number(s.value)]);
            close();
          });
          cancel.textContent = "Cancel";
        });
      });
      const delI = mkButton("✕", () => confirmModal(`Delete nav item "${label.textContent}"?`, () => {
        group.items.splice(ii, 1);
        markDirty();
        rerender();
      }));

      /* move item up / down within its group */
      const mvUpI = mkButton("▲", () => {
        if (ii === 0) return;
        [group.items[ii - 1], group.items[ii]] = [group.items[ii], group.items[ii - 1]];
        markDirty();
        rerender();
      });
      mvUpI.title = "Move item up";
      const mvDnI = mkButton("▼", () => {
        if (ii === group.items.length - 1) return;
        [group.items[ii], group.items[ii + 1]] = [group.items[ii + 1], group.items[ii]];
        markDirty();
        rerender();
      });
      mvDnI.title = "Move item down";
      itActions.append(mvUpI, mvDnI, editI, mvI, delI);

      itemLi.append(badge, label, metaEl);
      if (item.opens_in_new_tab) {
        const dot = document.createElement("span");
        dot.className = "new-tab-dot";
        dot.title = "opens in new tab";
        itemLi.appendChild(dot);
      }
      if (navItemMissing(c, item)) {
        const m = document.createElement("span");
        m.className = "missing";
        m.textContent = "missing";
        m.title = "references a dataview/page that does not exist in this config";
        itemLi.appendChild(m);
      }
      itemLi.appendChild(itActions);
      children.appendChild(itemLi);
    });
    li.appendChild(children);
    list.appendChild(li);
  });

  const add = btn("+ Add group", "primary");
  add.addEventListener("click", () => inputModal("Add navigation group", "Group name", "", (name) => {
    c.navigation.push({ group: name, items: [] });
    markDirty();
    rerender();
  }));
  root.appendChild(add);
}

/* ---------- page: Dataviews ---------- */

const COLUMN_SCHEMA = [
  { key: "name", label: "Name" },
  { key: "label", label: "Label" },
  { key: "type", label: "Type", type: "select", options: FIELD_TYPES },
];

const FORM_FIELD_SCHEMA = [
  { key: "name", label: "Name" },
  { key: "label", label: "Label" },
  { key: "type", label: "Type", type: "select", options: FIELD_TYPES },
  { key: "required", label: "Req", type: "bool" },
  { key: "options_query", label: "Options query" },
];

// schemaColumnsFor returns the column-name suggestions for a dataview's table,
// read from the captured `schema:` block. `includeLabelAliases` adds the
// "{fk}_label" names the generator derives for LEFT JOINed label columns (list
// / card / detail views), so those stay selectable too. A missing schema block
// or unknown table yields no suggestions (the field stays free text).
function schemaColumnsFor(r, includeLabelAliases) {
  const schema = state.config && state.config.schema;
  if (!schema || !Array.isArray(schema.tables)) return [];
  const want = resourceTableName(r);
  let t = schema.tables.find((x) => x.name === want);
  if (!t) t = schema.tables.find((x) => String(x.name || "").toLowerCase() === String(want).toLowerCase());
  if (!t) return [];
  const names = (t.columns || []).map((c) => c.name).filter(Boolean);
  if (includeLabelAliases) {
    for (const fk of t.foreign_keys || []) {
      if (fk && fk.column) names.push(fk.column + "_label");
    }
  }
  return names;
}

// schemaPkColumnsFor returns the names of primary-key columns of the dataview's
// table from the captured schema block, so the Settings "ID column" dropdown only
// offers real key candidates. Empty when there is no schema block/table or no PK.
function schemaPkColumnsFor(r) {
  const schema = state.config && state.config.schema;
  if (!schema || !Array.isArray(schema.tables)) return [];
  const want = resourceTableName(r);
  let t = schema.tables.find((x) => x.name === want);
  if (!t) t = schema.tables.find((x) => String(x.name || "").toLowerCase() === String(want).toLowerCase());
  if (!t) return [];
  return (t.columns || [])
    .filter((c) => c && c.primary_key)
    .map((c) => c.name)
    .filter(Boolean);
}

// withColumnChoices returns a copy of a collection schema whose `name` column
// becomes a combobox (datalist) offering `choices`. The value
// stays freely editable so derived aliases, computed names and SQL aliases can
// still be typed when they are not in the schema block.
function withColumnChoices(base, choices) {
  if (!choices || choices.length === 0) return base;
  return base.map((s) => (s.key === "name" ? { ...s, type: "combo", options: choices } : s));
}

const COMPUTED_SCHEMA = [
  { key: "name", label: "Name" },
  { key: "label", label: "Label" },
  { key: "type", label: "Type", type: "select", options: FIELD_TYPES },
  { key: "expression", label: "Expression (helpers.*)" },
];

const ACTION_SCHEMA = [
  { key: "name", label: "Name" },
  { key: "label", label: "Label" },
  { key: "query", label: "Query" },
  { key: "script", label: "Script (Lua)", type: "lua" },
  { key: "bulk", label: "Bulk", type: "bool" },
  { key: "requires_confirmation", label: "Confirm", type: "bool" },
  { key: "policy", label: "Policy", type: "roleSelect" },
];

const CHILD_SCHEMA = [
  { key: "name", label: "Section name" },
  { key: "resource", label: "Child dataview" },
  { key: "column", label: "FK column" },
];

/* Action row layout: fields + type toggle on row 1, full-width query/script on row 2, buttons on row 3. */
const ACTION_FIELDS = ["name", "label", "bulk", "requires_confirmation", "policy"];
const ACTION_SCHEMA_MAP = Object.fromEntries(ACTION_SCHEMA.map(s => [s.key, s]));

function actionRenderRow(resourceName) {
  return (item, idx, onChange, onDelete) => {
    const tr = document.createElement("tr");
    const td = document.createElement("td");
    td.colSpan = ACTION_SCHEMA.length + 1;
    td.className = "action-cell";

    /* --- mode: single source of truth for which textarea+buttons to show --- */
    let mode = (item.script && item.script.trim()) ? "lua"
             : (item.query && item.query.trim()) ? "sql"
             : "sql";

    /* --- row 1: compact fields + type toggle --- */
    const fieldsRow = document.createElement("div");
    fieldsRow.className = "action-fields";
    for (const key of ACTION_FIELDS) {
      const s = ACTION_SCHEMA_MAP[key];
      const field = document.createElement("div");
      field.className = "action-field";
      if (s.type === "bool") {
        const cb = document.createElement("input");
        cb.type = "checkbox";
        cb.checked = !!item[key];
        cb.addEventListener("change", () => { item[key] = cb.checked; onChange(); });
        field.appendChild(cb);
        const lbl = document.createElement("span");
        lbl.className = "action-field-label";
        lbl.textContent = s.label;
        field.append(lbl);
      } else if (s.type === "roleSelect") {
        const roles = (state.config.auth.roles || []).map((r) => r.name);
        const lbl = document.createElement("label");
        lbl.textContent = s.label;
        const el = document.createElement("select");
        el.multiple = true;
        el.size = Math.min(roles.length, 3);
        const current = (item[key] || "").split("|").map((r) => r.trim()).filter(Boolean);
        for (const role of roles) {
          const o = document.createElement("option");
          o.value = role;
          o.textContent = role;
          if (current.includes(role)) o.selected = true;
          el.appendChild(o);
        }
        if (roles.length === 0) {
          const o = document.createElement("option");
          o.value = "";
          o.textContent = "(no roles defined)";
          o.disabled = true;
          el.appendChild(o);
        }
        el.addEventListener("change", () => {
          const vals = Array.from(el.selectedOptions).map((o) => o.value);
          item[key] = vals.length ? vals.join("|") : "";
          onChange();
        });
        field.append(lbl, el);
      } else {
        const lbl = document.createElement("label");
        lbl.textContent = s.label;
        const el = document.createElement("input");
        el.type = "text";
        el.value = item[key] != null ? item[key] : "";
        el.addEventListener("change", () => {
          const v = el.value.trim();
          if (v === "") delete item[key]; else item[key] = v;
          onChange();
        });
        field.append(lbl, el);
      }
      fieldsRow.appendChild(field);
    }

    /* type toggle: active only before creation (no name yet), locked once named */
    const typeField = document.createElement("div");
    typeField.className = "action-field";
    const typeLabel = document.createElement("label");
    typeLabel.textContent = "Type";
    const typeBtns = document.createElement("div");
    typeBtns.className = "type-toggle";
    const sqlBtn = document.createElement("button");
    sqlBtn.type = "button";
    sqlBtn.className = "btn small" + (mode === "sql" ? " primary" : "");
    sqlBtn.textContent = "SQL";
    const luaBtn = document.createElement("button");
    luaBtn.type = "button";
    luaBtn.className = "btn small" + (mode === "lua" ? " primary" : "");
    luaBtn.textContent = "Lua Script";
    const typeLocked = !!item.name;
    if (typeLocked) {
      sqlBtn.disabled = true;
      luaBtn.disabled = true;
      sqlBtn.title = "Type locked after creation — edit via ⋯ JSON";
      luaBtn.title = "Type locked after creation — edit via ⋯ JSON";
    }
    function setType(t) {
      mode = t;
      if (t === "sql") {
        delete item.script;
        if (!item.query) item.query = "";
      } else {
        delete item.query;
        if (!item.script) item.script = "";
      }
      sqlBtn.className = "btn small" + (t === "sql" ? " primary" : "");
      luaBtn.className = "btn small" + (t === "lua" ? " primary" : "");
      onChange();
      renderRow2();
      renderRow3();
    }
    sqlBtn.addEventListener("click", () => setType("sql"));
    luaBtn.addEventListener("click", () => setType("lua"));
    typeBtns.append(sqlBtn, luaBtn);
    typeField.append(typeLabel, typeBtns);
    fieldsRow.appendChild(typeField);
    td.appendChild(fieldsRow);

    /* --- row 2: full-width query or script, driven by mode --- */
    const codeRow = document.createElement("div");
    codeRow.className = "action-script-row";
    function renderRow2() {
      codeRow.innerHTML = "";
      if (mode === "lua") {
        const lbl = document.createElement("label");
        lbl.textContent = "Script (Lua)";
        const ta = luaTextArea(
          item.script || "",
          (v) => { if (v.trim() === "") delete item.script; else item.script = v; onChange(); }
        );
        codeRow.append(lbl, ta);
      } else {
        const lbl = document.createElement("label");
        lbl.textContent = "Query (SQL)";
        const ta = document.createElement("textarea");
        ta.value = item.query || "";
        ta.rows = 2;
        ta.addEventListener("input", () => {
          const v = ta.value;
          if (v === "") delete item.query; else item.query = v;
          onChange();
        });
        codeRow.append(lbl, ta);
      }
    }
    renderRow2();
    td.appendChild(codeRow);

    /* --- row 3: buttons, driven by mode --- */
    const btnRow = document.createElement("div");
    btnRow.className = "action-buttons";
    function renderRow3() {
      btnRow.innerHTML = "";
      if (mode === "lua") {
        btnRow.appendChild(mkButton("Check", () => luaCheck(item.script)));
        btnRow.appendChild(mkButton("Run", () => luaRun(item.script, resourceName)));
      } else {
        btnRow.appendChild(mkButton("Run SQL", () => sqlRun(item.query, resourceName, item.name)));
      }
      const jsonBtn = mkButton("⋯", () => {
        openModal("Edit action (JSON)", (body, ok, cancel, close) => {
          const ta = document.createElement("textarea");
          ta.value = JSON.stringify(item, null, 2);
          body.appendChild(ta);
          const err = document.createElement("div");
          err.style.color = "var(--red)";
          body.appendChild(err);
          ok.addEventListener("click", () => {
            let v;
            try { v = JSON.parse(ta.value); } catch (e) { err.textContent = "Invalid JSON: " + e.message; return; }
            if (!v || typeof v !== "object" || Array.isArray(v)) { err.textContent = "Expected a JSON object"; return; }
            for (const k of Object.keys(item)) delete item[k];
            Object.assign(item, v);
            /* recompute mode after JSON edit */
            if (item.script && item.script.trim()) mode = "lua";
            else if (item.query && item.query.trim()) mode = "sql";
            markDirty(); close(); renderRow2(); renderRow3();
          });
          cancel.textContent = "Close";
        });
      });
      const delBtn = mkButton("✕", () => onDelete());
      btnRow.append(jsonBtn, delBtn);
    }
    renderRow3();
    td.appendChild(btnRow);

    tr.appendChild(td);
    return tr;
  };
}

function renderResourceList() {
  const c = state.config;
  if (!Array.isArray(c.resources)) c.resources = [];
  const root = content();
  h2(root, "Dataviews");

  const toolbar = document.createElement("div");
  toolbar.className = "toolbar";
  const si = document.createElement("input");
  si.type = "search";
  si.placeholder = "Search dataviews by name…";
  si.spellcheck = false;
  si.value = state.resourceSearch;
  toolbar.appendChild(si);
  const add = btn("+ Add dataview", "primary");
  add.addEventListener("click", () => inputModal("Add dataview", "Dataview name (PascalCase)", "", (name) => {
    c.resources.push({ name });
    markDirty();
    renderResourceList();
  }));
  toolbar.appendChild(add);
  root.appendChild(toolbar);

  const list = [...(c.resources || [])].sort((a, b) => {
    const na = (a.name || "").toLowerCase();
    const nb = (b.name || "").toLowerCase();
    return na < nb ? -1 : na > nb ? 1 : 0;
  });
  const term = state.resourceSearch.toLowerCase();
  const filtered = term
    ? list.filter((r) => (r.name || "").toLowerCase().includes(term) || (r.label || "").toLowerCase().includes(term))
    : list;

  const table = document.createElement("table");
  table.className = "rows";
  const thead = document.createElement("thead");
  const hr = document.createElement("tr");
  for (const h of ["Name", "Label", "Table", "ID column", "Actions"]) {
    const th = document.createElement("th");
    th.textContent = h;
    hr.appendChild(th);
  }
  thead.appendChild(hr);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  if (filtered.length === 0) {
    const tr = document.createElement("tr");
    const td = document.createElement("td");
    td.colSpan = 5;
    td.textContent = "No dataviews.";
    td.style.textAlign = "center";
    td.style.color = "var(--text-faint)";
    td.style.padding = "20px";
    tr.appendChild(td);
    tbody.appendChild(tr);
  } else {
    for (const r of filtered) {
      const tr = document.createElement("tr");
      const tdName = document.createElement("td");
      tdName.textContent = r.name;
      const tdLabel = document.createElement("td");
      tdLabel.textContent = r.label || "";
      const tdTable = document.createElement("td");
      tdTable.textContent = resourceTableName(r);
      const tdId = document.createElement("td");
      tdId.textContent = r.id_column || "id";
      const tdAct = document.createElement("td");
      tdAct.className = "row-actions";
      const viewBtn = mkButton("View", () => {
        state.resourceEditMode = false;
        renderResourceEditor(r.name);
      });
      const editBtn = mkButton("Edit", () => {
        state.resourceEditMode = true;
        renderResourceEditor(r.name);
      });
      const delBtn = btn("Delete", "danger small");
      delBtn.addEventListener("click", (e) => {
        e.stopPropagation();
        confirmModal(`Delete dataview "${r.name}"?`, () => {
          const idx = c.resources.indexOf(r);
          if (idx >= 0) {
            c.resources.splice(idx, 1);
            markDirty();
            renderResourceList();
          }
        });
      });
      tdAct.append(viewBtn, editBtn, delBtn);
      tr.append(tdName, tdLabel, tdTable, tdId, tdAct);
      tbody.appendChild(tr);
    }
  }
  table.appendChild(tbody);
  root.appendChild(table);

  si.addEventListener("input", () => {
    state.resourceSearch = si.value;
    renderResourceList();
  });
}

function resourceCollection(root, r, key, label, schema, title) {
  if (!r[key]) r[key] = {};
  const section = r[key];
  const head = document.createElement("div");
  head.className = "toolbar";
  const it = document.createElement("h3");
  it.className = "sub-heading";
  it.textContent = label;
  head.appendChild(it);
  const addIt = btn("+ Add", "small");
  addIt.addEventListener("click", () => {
    section.push({});
    markDirty();
    renderResourceEditor(r.name);
  });
  head.appendChild(addIt);
  root.appendChild(head);
  collectionEditor(root, section, schema, { jsonTitle: title });
}

function renderResourceEditor(name) {
  const c = state.config;
  const r = c.resources.find((x) => x.name === name);
  if (!r) { renderResourceList(); return; }
  state.resource = name;
  const root = content();

  const readOnly = !state.resourceEditMode;
  const tab = state.resourceTab || "settings";

  const back = btn("← Dataviews", "small");
  back.classList.add("nav-back");
  back.addEventListener("click", () => { state.resource = null; renderPage(); });
  root.appendChild(back);

  const head = document.createElement("div");
  head.className = "toolbar";
  const t = document.createElement("h2");
  t.style.margin = "0";
  t.textContent = "Dataview: " + r.name;
  head.appendChild(t);
  root.appendChild(head);

  /* sub-tab bar */
  const tabBar = document.createElement("div");
  tabBar.className = "toolbar";
  const tabs = [
    { id: "settings", label: "Settings" },
    { id: "list", label: "List" },
    { id: "form", label: "Form" },
    { id: "actions", label: "Actions" },
    { id: "data", label: "Show data" },
  ];
  for (const tb of tabs) {
    const b = mkButton(tb.label, () => {
      state.resourceTab = tb.id;
      renderResourceEditor(name);
    });
    if (tb.id === tab) b.classList.add("active");
    b.classList.add("resource-tab");
    tabBar.appendChild(b);
  }
  root.appendChild(tabBar);

  const body = document.createElement("div");
  root.appendChild(body);

  if (tab === "settings") renderResourceSettings(body, r, readOnly);
  else if (tab === "list") renderResourceListTab(body, r, readOnly);
  else if (tab === "form") renderResourceFormTab(body, r, readOnly);
  else if (tab === "actions") renderResourceActionsTab(body, r, readOnly);
  else if (tab === "data") renderResourceDataTab(body, r);

  if (readOnly) {
    root.querySelectorAll("input, select, textarea, button").forEach((el) => {
      if (el.classList.contains("nav-back") || el.classList.contains("resource-tab")) return;
      if (el.closest(".data-browser")) return; // data browsing stays usable in view mode
      el.disabled = true;
    });
  }
}

/* ---------- sub-tab: Show data (rows from the dataview's table) ---------- */

// resourceTableName mirrors the generator's tableName(): the explicit `table:`
// override, else "<lowercase name>s".
function resourceTableName(r) {
  if (r.table) return r.table;
  return String(r.name || "").toLowerCase() + "s";
}

function renderResourceDataTab(root, r) {
  const tableName = resourceTableName(r);
  const head = document.createElement("div");
  head.className = "toolbar";
  const it = document.createElement("h3");
  it.className = "sub-heading";
  it.textContent = "Data: " + tableName;
  head.appendChild(it);
  root.appendChild(head);

  const note = document.createElement("p");
  note.className = "mono";
  note.textContent = "Rows from the dataview's table (live database when a connection is configured).";
  root.appendChild(note);

  const wrap = document.createElement("div");
  root.appendChild(wrap);

  const perPage = r.list && r.list.per_page ? r.list.per_page : 10;
  dataBrowser(wrap, tableName, perPage);
}

/* ---------- sub-tab: Settings (identity + policies + children) ---------- */

function renderResourceSettings(root, r, readOnly) {
  const card = cardEl(root);
  const g = gridWrap(card);
  const pkNames = schemaPkColumnsFor(r);
  textField(g, "Name", r, "name", readOnly);
  textField(g, "Label", r, "label", readOnly);
  iconSelectField(g, "Icon", r, "icon", readOnly);
  textField(g, "Group", r, "group", readOnly);
  textField(g, "Table", r, "table", true);
  selectField(g, "ID column", r, "id_column", pkNames, readOnly);
  boolField(g, "Import CSV", r, "import_csv", readOnly);

  /* Policies */
  if (!r.policies) r.policies = {};
  h3(root, "Policies");
  const cardP = cardEl(root);
  const gP = gridWrap(cardP);
  const roles = (state.config.auth.roles || []).map((r) => r.name);
  policyField(gP, "view_any", r.policies, "view_any", roles, readOnly);
  policyField(gP, "view", r.policies, "view", roles, readOnly);
  policyField(gP, "create", r.policies, "create", roles, readOnly);
  policyField(gP, "update", r.policies, "update", roles, readOnly);
  policyField(gP, "delete", r.policies, "delete", roles, readOnly);

  /* Children */
  if (!Array.isArray(r.children)) r.children = [];
  h3(root, "Children (master-detail)");
  const cardCh = cardEl(root);
  const chCols = document.createElement("div");
  chCols.className = "toolbar";
  const chc = document.createElement("h3");
  chc.className = "sub-heading";
  chc.textContent = "Children";
  chCols.appendChild(chc);
  const addCh = btn("+ Add child", "small");
  if (!readOnly) {
    addCh.addEventListener("click", () => { r.children.push({}); markDirty(); renderResourceEditor(r.name); });
  }
  chCols.appendChild(addCh);
  cardCh.appendChild(chCols);
  collectionEditor(cardCh, r.children, CHILD_SCHEMA, { jsonTitle: "Edit child (JSON)", readOnly });
}

/* ---------- sub-tab: List (list + card + detail) ---------- */

function renderResourceListTab(root, r, readOnly) {
  const columnSchema = withColumnChoices(COLUMN_SCHEMA, schemaColumnsFor(r, true));
  /* List */
  if (!r.list) r.list = {};
  h3(root, "List");
  const cardL = cardEl(root);
  const gL = gridWrap(cardL);
  textField(gL, "Query", r.list, "query", readOnly);
  textField(gL, "Count query", r.list, "count_query", readOnly);
  numField(gL, "Per page", r.list, "per_page", readOnly);
  textField(gL, "Default sort (leading - = desc)", r.list, "default_sort", readOnly);
  stringListField(gL, "CSV export columns", r.list, "export", readOnly);
  if (!r.list.columns) r.list.columns = [];
  const lCols = document.createElement("div");
  lCols.className = "toolbar";
  const lc = document.createElement("h3");
  lc.className = "sub-heading";
  lc.textContent = "Columns";
  lCols.appendChild(lc);
  if (!readOnly) {
    const addCol = btn("+ Add column", "small");
    addCol.addEventListener("click", () => { r.list.columns.push({}); markDirty(); renderResourceEditor(r.name); });
    lCols.appendChild(addCol);
  }
  cardL.appendChild(lCols);
  collectionEditor(cardL, r.list.columns, columnSchema, { jsonTitle: "Edit column (JSON)", readOnly });

  if (!r.list.computed) r.list.computed = [];
  const lComp = document.createElement("div");
  lComp.className = "toolbar";
  const lcp = document.createElement("h3");
  lcp.className = "sub-heading";
  lcp.textContent = "Computed columns";
  lComp.appendChild(lcp);
  if (!readOnly) {
    const addComp = btn("+ Add computed", "small");
    addComp.addEventListener("click", () => { r.list.computed.push({}); markDirty(); renderResourceEditor(r.name); });
    lComp.appendChild(addComp);
  }
  cardL.appendChild(lComp);
  collectionEditor(cardL, r.list.computed, COMPUTED_SCHEMA, { jsonTitle: "Edit computed (JSON)", readOnly });

  /* Card */
  if (!r.card) r.card = {};
  h3(root, "Card");
  const cardC = cardEl(root);
  const gC = gridWrap(cardC);
  numField(gC, "Columns", r.card, "columns", readOnly);
  numField(gC, "Rows", r.card, "rows", readOnly);
  textField(gC, "Kanban field", r.card, "kanban_field", readOnly);
  if (!r.card.fields) r.card.fields = [];
  const cCols = document.createElement("div");
  cCols.className = "toolbar";
  const cc = document.createElement("h3");
  cc.className = "sub-heading";
  cc.textContent = "Card fields";
  cCols.appendChild(cc);
  if (!readOnly) {
    const addCardCol = btn("+ Add field", "small");
    addCardCol.addEventListener("click", () => { r.card.fields.push({}); markDirty(); renderResourceEditor(r.name); });
    cCols.appendChild(addCardCol);
  }
  cardC.appendChild(cCols);
  collectionEditor(cardC, r.card.fields, columnSchema, { jsonTitle: "Edit card field (JSON)", readOnly });

  if (!r.card.computed) r.card.computed = [];
  const cComp = document.createElement("div");
  cComp.className = "toolbar";
  const ccp = document.createElement("h3");
  ccp.className = "sub-heading";
  ccp.textContent = "Computed fields";
  cComp.appendChild(ccp);
  if (!readOnly) {
    const addCComp = btn("+ Add computed", "small");
    addCComp.addEventListener("click", () => { r.card.computed.push({}); markDirty(); renderResourceEditor(r.name); });
    cComp.appendChild(addCComp);
  }
  cardC.appendChild(cComp);
  collectionEditor(cardC, r.card.computed, COMPUTED_SCHEMA, { jsonTitle: "Edit computed (JSON)", readOnly });

  /* Detail */
  if (!r.detail) r.detail = {};
  h3(root, "Detail");
  const cardD = cardEl(root);
  const gD = gridWrap(cardD);
  textField(gD, "Query", r.detail, "query", readOnly);
  if (!r.detail.fields) r.detail.fields = [];
  const dCols = document.createElement("div");
  dCols.className = "toolbar";
  const dc = document.createElement("h3");
  dc.className = "sub-heading";
  dc.textContent = "Detail fields";
  dCols.appendChild(dc);
  if (!readOnly) {
    const addDCol = btn("+ Add field", "small");
    addDCol.addEventListener("click", () => { r.detail.fields.push({}); markDirty(); renderResourceEditor(r.name); });
    dCols.appendChild(addDCol);
  }
  cardD.appendChild(dCols);
  collectionEditor(cardD, r.detail.fields, columnSchema, { jsonTitle: "Edit detail field (JSON)", readOnly });

  if (!r.detail.computed) r.detail.computed = [];
  const dComp = document.createElement("div");
  dComp.className = "toolbar";
  const dcp = document.createElement("h3");
  dcp.className = "sub-heading";
  dcp.textContent = "Computed fields";
  dComp.appendChild(dcp);
  if (!readOnly) {
    const addDComp = btn("+ Add computed", "small");
    addDComp.addEventListener("click", () => { r.detail.computed.push({}); markDirty(); renderResourceEditor(r.name); });
    dComp.appendChild(addDComp);
  }
  cardD.appendChild(dComp);
  collectionEditor(cardD, r.detail.computed, COMPUTED_SCHEMA, { jsonTitle: "Edit computed (JSON)", readOnly });
}

/* ---------- sub-tab: Form (create / update / delete) ---------- */

function renderResourceFormTab(root, r, readOnly) {
  if (!r.form) r.form = {};
  const formSchema = withColumnChoices(FORM_FIELD_SCHEMA, schemaColumnsFor(r, false));
  for (const [key, label] of [["create", "Form / Create"], ["update", "Form / Update"], ["delete", "Form / Delete"]]) {
    if (!r.form[key]) r.form[key] = {};
    h3(root, label);
    const cardF = cardEl(root);
    const gF = gridWrap(cardF);
    textField(gF, "Query", r.form[key], "query", readOnly);
    textField(gF, "Populate query", r.form[key], "populate_query", readOnly);
    if (!r.form[key].fields) r.form[key].fields = [];
    const fCols = document.createElement("div");
    fCols.className = "toolbar";
    const fc = document.createElement("h3");
    fc.className = "sub-heading";
    fc.textContent = "Fields";
    fCols.appendChild(fc);
    if (!readOnly) {
      const addFCol = btn("+ Add field", "small");
      addFCol.addEventListener("click", () => { r.form[key].fields.push({}); markDirty(); renderResourceEditor(r.name); });
      fCols.appendChild(addFCol);
    }
    cardF.appendChild(fCols);
    collectionEditor(cardF, r.form[key].fields, formSchema, { jsonTitle: "Edit field (JSON)", readOnly });
  }
}

/* ---------- sub-tab: Actions ---------- */

function renderResourceActionsTab(root, r, readOnly) {
  if (!Array.isArray(r.actions)) r.actions = [];
  h3(root, "Actions");
  const cardA = cardEl(root);
  const aCols = document.createElement("div");
  aCols.className = "toolbar";
  const ac = document.createElement("h3");
  ac.className = "sub-heading";
  ac.textContent = "Actions";
  aCols.appendChild(ac);
  const addAct = btn("+ Add action", "small");
  if (!readOnly) {
    addAct.addEventListener("click", () => { r.actions.push({}); markDirty(); renderResourceEditor(r.name); });
  }
  aCols.appendChild(addAct);
  cardA.appendChild(aCols);
  collectionEditor(cardA, r.actions, ACTION_SCHEMA, {
    jsonTitle: "Edit action (JSON)",
    renderRow: actionRenderRow(r.name),
    readOnly,
  });
}

function pageResources() {
  if (state.resource) renderResourceEditor(state.resource);
  else renderResourceList();
}

/* ---------- page: Schema browser ---------- */

let schemaSelectedTable = null; // table whose data modal is open

async function pageSchema() {
  const c = state.config;
  if (!c.schema || !Array.isArray(c.schema.tables) || c.schema.tables.length === 0) {
    const root = content();
    h2(root, "Schema");
    const p = document.createElement("p");
    p.className = "mono";
    p.textContent = "No schema block captured in the config (run `yaga init --db`).";
    root.appendChild(p);
    return;
  }

  const tables = [...c.schema.tables].sort((a, b) => {
    const na = (a.name || "").toLowerCase();
    const nb = (b.name || "").toLowerCase();
    return na < nb ? -1 : na > nb ? 1 : 0;
  });

  const root = content();
  h2(root, "Schema browser");

  const toolbar = document.createElement("div");
  toolbar.className = "toolbar";
  const si = document.createElement("input");
  si.type = "search";
  si.placeholder = "Search tables…";
  si.spellcheck = false;
  si.value = state.schemaSearch || "";
  toolbar.appendChild(si);
  const hint = document.createElement("span");
  hint.className = "mono";
  hint.textContent = tables.length + " table" + (tables.length === 1 ? "" : "s");
  toolbar.appendChild(hint);
  root.appendChild(toolbar);

  const container = document.createElement("div");
  root.appendChild(container);

  function render() {
    container.innerHTML = "";
    const term = si.value.toLowerCase();
    const filtered = tables.filter((t) =>
      (t.name || "").toLowerCase().includes(term)
    );

    if (filtered.length === 0) {
      const p = document.createElement("p");
      p.className = "mono";
      p.textContent = "No matching tables.";
      container.appendChild(p);
      return;
    }

    const tableEl = document.createElement("table");
    tableEl.className = "rows";
    const thead = document.createElement("thead");
    const hr = document.createElement("tr");
    for (const h of ["Name", "PK", "View", "Columns", "FKs", "Actions"]) {
      const th = document.createElement("th");
      th.textContent = h;
      hr.appendChild(th);
    }
    thead.appendChild(hr);
    tableEl.appendChild(thead);

    const tbody = document.createElement("tbody");
    for (const t of filtered) {
      const tr = document.createElement("tr");

      const tdName = document.createElement("td");
      tdName.textContent = t.name;

      const tdPk = document.createElement("td");
      tdPk.textContent = t.pk || "-";

      const tdView = document.createElement("td");
      tdView.textContent = t.view ? "view" : "";

      const tdCols = document.createElement("td");
      tdCols.textContent = (t.columns || []).length;

      const tdFks = document.createElement("td");
      tdFks.textContent = (t.foreign_keys || []).length;

      const tdAct = document.createElement("td");
      tdAct.className = "row-actions";

      const viewBtn = mkButton("View", () => openSchemaModal(t));
      const dataBtn = mkButton("Show data", () => loadSchemaData(t.name));
      tdAct.append(viewBtn, dataBtn);

      tr.append(tdName, tdPk, tdView, tdCols, tdFks, tdAct);
      tbody.appendChild(tr);
    }
    tableEl.appendChild(tbody);
    container.appendChild(tableEl);
  }

  si.addEventListener("input", render);
  render();
}

async function loadSchemaData(tableName) {
  const root = content();
  h2(root, "Data: " + tableName);
  const back = btn("← Schema", "small");
  back.addEventListener("click", () => { pageSchema(); });
  root.appendChild(back);

  const wrap = document.createElement("div");
  root.appendChild(wrap);
  dataBrowser(wrap, tableName, 10);
}

/* Shared live-data browser: a row-limit input, a Load button and the result
   table. Used by the Schema browser's "Show data" action and the dataview
   editor's "Show data" tab. The server answers from the live database when a
   connection is configured (--db flag or connections: in yaga.yaml) and from
   the in-memory stub otherwise; data.source says which. */
async function dataBrowser(container, tableName, initialLimit) {
  container.classList.add("data-browser");

  const limitBar = document.createElement("div");
  limitBar.className = "toolbar";
  const li = document.createElement("input");
  li.type = "number";
  li.min = "1";
  li.max = "1000";
  li.value = String(initialLimit || 10);
  li.style.width = "80px";
  const loadBtn = btn("Load", "small");
  limitBar.appendChild(li);
  limitBar.appendChild(document.createTextNode(" rows"));
  limitBar.appendChild(loadBtn);
  container.appendChild(limitBar);

  const dataContainer = document.createElement("div");
  container.appendChild(dataContainer);

  async function doLoad() {
    const n = parseInt(li.value, 10) || 10;
    let data;
    try {
      data = await api("GET", "/api/schema-sample/" + encodeURIComponent(tableName) + "?limit=" + n);
    } catch (e) {
      dataContainer.innerHTML = "";
      const err = document.createElement("div");
      err.className = "mono";
      err.style.color = "var(--red)";
      err.textContent = "Failed to load data: " + e.message;
      dataContainer.appendChild(err);
      return;
    }
    renderData(data);
  }

  loadBtn.addEventListener("click", doLoad);

  function renderData(data) {
    dataContainer.innerHTML = "";
    const info = document.createElement("div");
    info.className = "mono";
    info.textContent = data.table + " — " + data.count + " row" + (data.count === 1 ? "" : "s") + " (limit " + data.limit + ")" +
      (data.source === "live" ? " · live database" : " · in-memory stub");
    dataContainer.appendChild(info);

    if (!data.rows || data.rows.length === 0) {
      const p = document.createElement("p");
      p.className = "mono";
      if (data.source === "live") {
        p.textContent = "No rows in " + data.table + ".";
      } else {
        p.textContent = "No data. The in-memory stub is empty — configure a database connection (--db or connections: in yaga.yaml) or seed the stub from a live database first.";
      }
      dataContainer.appendChild(p);
      return;
    }

    const cols = Object.keys(data.rows[0]);
    const tableEl = document.createElement("table");
    tableEl.className = "rows data-table";
    const thead = document.createElement("thead");
    const hr = document.createElement("tr");
    for (const c of cols) {
      const th = document.createElement("th");
      th.textContent = c;
      hr.appendChild(th);
    }
    thead.appendChild(hr);
    tableEl.appendChild(thead);

    const tbody = document.createElement("tbody");
    for (const row of data.rows) {
      const tr = document.createElement("tr");
      for (const c of cols) {
        const td = document.createElement("td");
        const v = row[c];
        td.textContent = v === null ? "NULL" : String(v);
        tr.appendChild(td);
      }
      tbody.appendChild(tr);
    }
    tableEl.appendChild(tbody);
    const wrapEl = document.createElement("div");
    wrapEl.className = "table-wrap";
    wrapEl.appendChild(tableEl);
    dataContainer.appendChild(wrapEl);
  }

  doLoad();
}

function openSchemaModal(t) {
  schemaSelectedTable = t;
  openModal("Schema: " + t.name, (body, ok, cancel, close) => {
    const g = gridWrap(cardEl(body));

    // Table info row
    const infoRow = document.createElement("div");
    infoRow.className = "field";
    const infoLabel = document.createElement("label");
    infoLabel.textContent = "Table";
    const infoVal = document.createElement("span");
    infoVal.textContent = t.name + (t.view ? "  (view)" : "");
    infoVal.style.fontWeight = "600";
    infoRow.append(infoLabel, infoVal);
    g.appendChild(infoRow);

    const pkRow = document.createElement("div");
    pkRow.className = "field";
    const pkLabel = document.createElement("label");
    pkLabel.textContent = "Primary key";
    const pkVal = document.createElement("span");
    pkVal.textContent = t.pk || "-";
    pkRow.append(pkLabel, pkVal);
    g.appendChild(pkRow);

    // Columns
    if (Array.isArray(t.columns) && t.columns.length) {
      const colHead = document.createElement("div");
      colHead.className = "sub-heading";
      colHead.textContent = "Columns";
      body.appendChild(colHead);

      const colTable = document.createElement("table");
      colTable.className = "rows";
      const cThead = document.createElement("thead");
      const cHr = document.createElement("tr");
      for (const h of ["Name", "Type", "PK"]) {
        const th = document.createElement("th");
        th.textContent = h;
        cHr.appendChild(th);
      }
      cThead.appendChild(cHr);
      colTable.appendChild(cThead);

      const cTbody = document.createElement("tbody");
      for (const col of t.columns) {
        const ctr = document.createElement("tr");
        const c1 = document.createElement("td");
        c1.textContent = col.name;
        const c2 = document.createElement("td");
        c2.textContent = col.type;
        const c3 = document.createElement("td");
        c3.textContent = col.primary_key ? "✓" : "";
        ctr.append(c1, c2, c3);
        cTbody.appendChild(ctr);
      }
      colTable.appendChild(cTbody);
      body.appendChild(colTable);
    }

    // Foreign keys
    if (Array.isArray(t.foreign_keys) && t.foreign_keys.length) {
      const fkHead = document.createElement("div");
      fkHead.className = "sub-heading";
      fkHead.textContent = "Foreign keys";
      body.appendChild(fkHead);

      const fkTable = document.createElement("table");
      fkTable.className = "rows";
      const fThead = document.createElement("thead");
      const fHr = document.createElement("tr");
      for (const h of ["Column", "References", "Label"]) {
        const th = document.createElement("th");
        th.textContent = h;
        fHr.appendChild(th);
      }
      fThead.appendChild(fHr);
      fkTable.appendChild(fThead);

      const fTbody = document.createElement("tbody");
      for (const fk of t.foreign_keys) {
        const ftr = document.createElement("tr");
        const f1 = document.createElement("td");
        f1.textContent = fk.column;
        const f2 = document.createElement("td");
        f2.textContent = fk.foreign_table + "." + fk.foreign_column;
        const f3 = document.createElement("td");
        f3.textContent = fk.label || "";
        ftr.append(f1, f2, f3);
        fTbody.appendChild(ftr);
      }
      fkTable.appendChild(fTbody);
      body.appendChild(fkTable);
    }

    if ((!t.columns || t.columns.length === 0) && (!t.foreign_keys || t.foreign_keys.length === 0)) {
      const p = document.createElement("p");
      p.className = "mono";
      p.textContent = "No column or foreign key information captured.";
      body.appendChild(p);
    }

    ok.classList.add("hidden");
    cancel.textContent = "Close";
  });
}

/* ---------- page: Pages ---------- */

function pagePages() {
  const c = state.config;
  if (state.pageName) { renderPageEditor(state.pageName); return; }
  if (!Array.isArray(c.pages)) c.pages = [];
  const root = content();
  h2(root, "Pages");
  c.pages.forEach((p, idx) => {
    const card = cardEl(root);
    const head = document.createElement("div");
    head.className = "toolbar";
    const t = document.createElement("h3");
    t.style.margin = "0";
    t.textContent = p.name + (p.path ? "  (" + p.path + ")" : "");
    if (p.default) {
      const b = document.createElement("span");
      b.className = "badge tab";
      b.textContent = "default";
      t.appendChild(b);
    }
    head.appendChild(t);
    const spacer = document.createElement("div");
    spacer.className = "spacer";
    head.appendChild(spacer);
    const editBtn = btn("Edit", "small");
    editBtn.addEventListener("click", () => { state.pageName = p.name; pagePages(); });
    const del = btn("Delete", "danger small");
    del.addEventListener("click", () => confirmModal(`Delete page "${p.name}"?`, () => {
      c.pages.splice(idx, 1);
      markDirty();
      pagePages();
    }));
    head.append(editBtn, del);
    card.appendChild(head);
  });
  const add = btn("+ Add page", "primary");
  add.addEventListener("click", () => inputModal("Add page", "Page name (PascalCase)", "", (name) => {
    c.pages.push({ name });
    markDirty();
    pagePages();
  }));
  root.appendChild(add);
}

const WIDGET_SCHEMA = [
  { key: "type", label: "Type", type: "select", options: ["stat", "stats_grid", "chart", "table", "list", "html"] },
  { key: "label", label: "Label" },
  { key: "query", label: "Query" },
  { key: "limit", label: "Limit", type: "number" },
  { key: "columns", label: "Columns", type: "number" },
];

function renderPageEditor(name) {
  const c = state.config;
  const p = c.pages.find((x) => x.name === name);
  if (!p) { state.pageName = null; pagePages(); return; }
  const root = content();
  const back = btn("← Pages", "small");
  back.addEventListener("click", () => { state.pageName = null; pagePages(); });
  root.appendChild(back);
  h2(root, "Page: " + p.name);

  const card = cardEl(root);
  const g = gridWrap(card);
  textField(g, "Name", p, "name");
  textField(g, "Path", p, "path");
  boolField(g, "Default page", p, "default");

  if (!Array.isArray(p.widgets)) p.widgets = [];
  h3(root, "Widgets");
  const cardW = cardEl(root);
  const wCols = document.createElement("div");
  wCols.className = "toolbar";
  const wc = document.createElement("h3");
  wc.style.margin = "0";
  wc.textContent = "Widgets";
  wCols.appendChild(wc);
  const addW = btn("+ Add widget", "small");
  addW.addEventListener("click", () => { p.widgets.push({ type: "stat" }); markDirty(); renderPageEditor(name); });
  wCols.appendChild(addW);
  cardW.appendChild(wCols);
  collectionEditor(cardW, p.widgets, WIDGET_SCHEMA, { jsonTitle: "Edit widget (JSON)" });
}

/* ---------- page: Validate ---------- */

async function pageValidate() {
  const root = content();
  h2(root, "Validate");
  const btnRow = document.createElement("div");
  btnRow.className = "toolbar";
  const fix = btn("Fix", "small");
  fix.addEventListener("click", async () => {
    let r;
    try {
      r = await api("POST", "/api/fix");
    } catch (e) {
      toast("fix failed: " + e.message, "error");
      pageValidate();
      return;
    }
    const remaining = (r.errors || []).length;
    const warns = (r.warnings || []).length;
    if (r.changed) {
      const parts = ["Fixed " + (r.fixed || []).length + " item(s)"];
      if (remaining || warns) {
        const detail = [];
        if (remaining) detail.push(remaining + " error(s)");
        if (warns) detail.push(warns + " warning(s)");
        parts.push(" — " + detail.join(", ") + " remain");
      }
      toast(parts.join(""), remaining ? "warn" : "ok");
      try { await reloadConfig(); } catch (e) { /* keep the page */ }
    } else {
      const parts = [];
      if (remaining) parts.push(remaining + " error(s)");
      if (warns) parts.push(warns + " warning(s)");
      toast(parts.length ? "Nothing to fix — " + parts.join(", ") : "Nothing to fix", remaining ? "warn" : "ok");
    }
    pageValidate();
  });
  btnRow.appendChild(fix);
  const refresh = btn("Refresh", "small");
  refresh.addEventListener("click", () => pageValidate());
  btnRow.appendChild(refresh);
  root.appendChild(btnRow);

  let data;
  try {
    data = await api("GET", "/api/validate");
  } catch (e) {
    toast("validate failed: " + e.message, "error");
    return;
  }
  const findings = data.findings || [];
  const ul = document.createElement("ul");
  ul.className = "findings";
  if (findings.length === 0) {
    const li = document.createElement("li");
    li.className = "good";
    li.textContent = "No problems found.";
    ul.appendChild(li);
  }
  for (const f of findings) {
    const li = document.createElement("li");
    li.className = f.kind === "warning" ? "warning" : "error";
    li.textContent = f.label;
    if (f.detail) {
      const d = document.createElement("div");
      d.className = "origin";
      d.textContent = f.detail;
      li.appendChild(d);
    }
    ul.appendChild(li);
  }
  root.appendChild(ul);
}

/* ---------- page: Preview ---------- */

state.preview = { view: "page", target: "", theme: "auto" };

function pagePreview() {
  const c = state.config;
  const root = content();
  h2(root, "Preview");

  const toolbar = document.createElement("div");
  toolbar.className = "preview-toolbar";

  const errDiv = document.createElement("div");
  errDiv.className = "preview-error hidden";

  const iframe = document.createElement("iframe");
  iframe.className = "preview-frame";
  iframe.title = "Dashboard preview";

  function targets() {
    if (state.preview.view === "resource") return (c.resources || []).map((r) => r.name);
    return (c.pages || []).map((p) => p.name);
  }
  function pickTarget() {
    const ts = targets();
    if (!ts.includes(state.preview.target)) state.preview.target = ts[0] || "";
  }
  pickTarget();

  function loadPreview() {
    const q = new URLSearchParams({ view: state.preview.view, theme: state.preview.theme });
    if (state.preview.target) q.set(state.preview.view === "resource" ? "resource" : "page", state.preview.target);
    iframe.src = "/preview?" + q.toString();
  }

  async function syncConfigThenLoad() {
    errDiv.classList.add("hidden");
    if (state.dirty) {
      try {
        const r = await api("PUT", "/api/config", state.config);
        state.rev = r.rev || state.rev;
      } catch (e) {
        const errors = (e.data && e.data.errors) || [e.message];
        errDiv.textContent = "Config is currently invalid; preview shows the last saved config:\n" + errors.join("\n");
        errDiv.classList.remove("hidden");
      }
    }
    loadPreview();
  }

  const viewSel = document.createElement("select");
  viewSel.id = "preview-view";
  for (const [v, l] of [["page", "Page"], ["resource", "Dataview"]]) {
    const o = document.createElement("option");
    o.value = v;
    o.textContent = l;
    viewSel.appendChild(o);
  }
  viewSel.value = state.preview.view;

  const targetSel = document.createElement("select");
  targetSel.id = "preview-target";
  function fillTargets() {
    targetSel.innerHTML = "";
    const ts = targets();
    if (ts.length === 0) {
      const o = document.createElement("option");
      o.value = "";
      o.textContent = state.preview.view === "resource" ? "(no dataviews)" : "(no pages)";
      targetSel.appendChild(o);
    }
    for (const t of ts) {
      const o = document.createElement("option");
      o.value = t;
      o.textContent = t;
      targetSel.appendChild(o);
    }
    if (ts.includes(state.preview.target)) targetSel.value = state.preview.target;
  }
  fillTargets();

  const themeSel = document.createElement("select");
  themeSel.id = "preview-theme";
  for (const [v, l] of [["auto", "Auto"], ["light", "Light"], ["dark", "Dark"]]) {
    const o = document.createElement("option");
    o.value = v;
    o.textContent = l;
    themeSel.appendChild(o);
  }
  themeSel.value = state.preview.theme;

  const refresh = btn("Refresh", "primary");
  refresh.addEventListener("click", syncConfigThenLoad);

  viewSel.addEventListener("change", () => {
    state.preview.view = viewSel.value;
    state.preview.target = targets()[0] || "";
    fillTargets();
    syncConfigThenLoad();
  });
  targetSel.addEventListener("change", () => {
    state.preview.target = targetSel.value;
    syncConfigThenLoad();
  });
  themeSel.addEventListener("change", () => {
    state.preview.theme = themeSel.value;
    loadPreview();
  });

  toolbar.append(viewSel, targetSel, themeSel);
  const spacer = document.createElement("div");
  spacer.className = "spacer";
  toolbar.appendChild(spacer);
  toolbar.appendChild(refresh);

  root.append(toolbar, errDiv, iframe);
  syncConfigThenLoad();
}

/* ---------- page: Raw YAML ---------- */

// Find the line index of `- name: <resourceName>` inside the top-level
// `resources:` block of the YAML text. Only matches list items at the
// resources indent level (not nested `- name:` entries inside columns/fields).
// Returns -1 when not found.
function findResourceYamlLine(yaml, resourceName) {
  const lines = yaml.split("\n");
  let inResources = false;
  let listIndent = null; // indent of `- ` items directly under resources:
  const reTopKey = /^[A-Za-z_][\w-]*:\s*$/;
  const reDashItem = /^(\s*)-\s+/;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (reTopKey.test(line)) {
      inResources = line.trim() === "resources:";
      if (inResources) listIndent = null;
      continue;
    }
    if (!inResources) continue;
    const mDash = line.match(reDashItem);
    if (mDash) {
      const indent = mDash[1].length;
      if (listIndent === null) listIndent = indent;
      // Only match `- name:` at the resources list-item indent level.
      if (indent === listIndent) {
        const mName = line.match(/^\s*-\s*name:\s*(.+?)\s*$/);
        if (mName && mName[1] === resourceName) return i;
      }
    }
  }
  return -1;
}

async function pageRaw() {
  const root = content();
  h2(root, "Raw YAML");
  const hint = document.createElement("p");
  hint.className = "mono";
  hint.textContent = "Full config as YAML. Apply validates it and replaces the in-memory config; Save then writes it to disk.";
  root.appendChild(hint);
  let data;
  try {
    data = await api("GET", "/api/raw");
  } catch (e) {
    toast("load failed: " + e.message, "error");
    return;
  }
  state.rawDirty = false;
  const ta = document.createElement("textarea");
  ta.className = "raw-editor";
  ta.value = data.yaml;
  ta.addEventListener("input", () => { state.rawDirty = true; });
  root.appendChild(ta);

  // Scroll to the dataview we came from, if any.
  const scrollTarget = state.rawScrollTarget;
  state.rawScrollTarget = null;
  if (scrollTarget) {
    requestAnimationFrame(() => {
      const line = findResourceYamlLine(data.yaml, scrollTarget);
      if (line >= 0) {
        // Character offset of the start of the target line.
        const lines = data.yaml.split("\n");
        let charOffset = 0;
        for (let l = 0; l < line; l++) {
          charOffset += lines[l].length + 1; // +1 for the newline
        }
        ta.focus();
        ta.setSelectionRange(charOffset, charOffset);
      }
    });
  }

  const searchRow = document.createElement("div");
  searchRow.className = "raw-search";
  const si = document.createElement("input");
  si.type = "search";
  si.placeholder = "Search key or value…";
  si.spellcheck = false;
  const count = document.createElement("span");
  count.className = "match-count";
  count.textContent = "0 matches";
  const prev = btn("Prev", "small");
  const next = btn("Next", "small");
  prev.disabled = true;
  next.disabled = true;
  searchRow.append(si, count, prev, next);
  root.insertBefore(searchRow, ta);

  let matches = [];
  let matchIdx = -1;
  const findMatches = () => {
    matches = [];
    const q = si.value.trim();
    if (!q) return 0;
    const src = ta.value.toLowerCase();
    const term = q.toLowerCase();
    let i = src.indexOf(term);
    while (i !== -1) {
      matches.push(i);
      i = src.indexOf(term, i + term.length);
    }
    return matches.length;
  };
  const go = (dir, focusTa) => {
    if (!matches.length) return;
    matchIdx = (matchIdx + dir + matches.length) % matches.length;
    const start = matches[matchIdx];
    const term = si.value.trim();
    if (focusTa) ta.focus();
    ta.setSelectionRange(start, start + term.length);
    const lineNo = ta.value.slice(0, start).split("\n").length - 1;
    const lineH = parseFloat(getComputedStyle(ta).lineHeight) || 18;
    ta.scrollTop = Math.max(0, lineNo * lineH - ta.clientHeight / 2);
    count.textContent = (matchIdx + 1) + " of " + matches.length;
  };
  si.addEventListener("input", () => {
    const m = findMatches();
    matchIdx = -1;
    count.textContent = m ? "0 of " + m : "0 matches";
    prev.disabled = !m;
    next.disabled = !m;
    if (m) go(1, false);
  });
  prev.addEventListener("click", () => go(-1, true));
  next.addEventListener("click", () => go(1, true));
  si.addEventListener("keydown", (e) => {
    if (e.key === "Enter") { e.preventDefault(); go(e.shiftKey ? -1 : 1, true); }
    else if (e.key === "Escape") { si.value = ""; si.dispatchEvent(new Event("input")); si.blur(); }
  });

  const row = document.createElement("div");
  row.className = "toolbar";
  const apply = btn("Apply", "primary");
  apply.addEventListener("click", async () => {
    try {
      if (!(await checkStaleRev())) {
        state.rawDirty = false;
        await reloadConfig();
        renderTabs();
        renderPage();
        return;
      }
      const r = await apiRawPut("/api/raw", ta.value);
      state.rev = r.rev || state.rev;
      state.rawDirty = false;
      toast("YAML applied (validated). Press Save to write it.", "ok");
      clearDirty();
      await reloadConfig();
      renderTabs();
    } catch (e) {
      toast("Invalid YAML:\n" + e.message, "error");
    }
  });
  row.appendChild(apply);
  root.appendChild(row);
}

/* ---------- save ---------- */

$("#save-btn").addEventListener("click", save);

/* Confirm-style modal that resolves true for "Overwrite" and false for
   "Reload" — used by the stale-write guard below. */
function confirmOverwriteModal() {
  return new Promise((resolve) => {
    openModal("Confirm", (body, ok, cancel, close) => {
      const p = document.createElement("p");
      p.textContent = "The config changed on the server since you loaded it (likely an agent/MCP edit). " +
        "Overwrite the server copy with your changes, or reload to see the latest?";
      body.appendChild(p);
      ok.textContent = "Overwrite";
      ok.addEventListener("click", () => { close(); resolve(true); });
      cancel.textContent = "Reload";
      cancel.addEventListener("click", () => { close(); resolve(false); });
    });
  });
}

/* Checks whether another writer (MCP/agent) replaced the config since we last
   loaded it. Resolves true to proceed, false to reload first. */
async function checkStaleRev() {
  let rev;
  try {
    rev = (await api("GET", "/api/rev")).rev;
  } catch (e) {
    return true; // rev endpoint unavailable — do not block saves
  }
  if (rev === state.rev) return true;
  return confirmOverwriteModal();
}

async function save() {
  const btn = $("#save-btn");
  btn.disabled = true;
  const status = $("#save-status");
  status.textContent = "validating…";
  try {
    if (!(await checkStaleRev())) {
      state.rawDirty = false;
      await reloadConfig();
      renderTabs();
      renderPage();
      status.textContent = "";
      toast("Reloaded the latest config; your unsaved edits were discarded.", "ok");
      return;
    }
    if (state.page === "raw") {
      const ta = $(".raw-editor");
      if (!ta) throw new Error("raw editor not loaded");
      const r = await apiRawPut("/api/raw", ta.value);
      state.rev = r.rev || state.rev;
      state.rawDirty = false;
    } else {
      const r = await api("PUT", "/api/config", state.config);
      state.rev = r.rev || state.rev;
    }
    status.textContent = "saving…";
    await api("POST", "/api/save");
    status.textContent = "";
    clearDirty();
    toast("Saved to " + state.configPath, "ok");
    await reloadConfig();
    renderTabs();
  } catch (e) {
    status.textContent = "";
    toast("Save failed:\n" + e.message, "error");
  } finally {
    btn.disabled = false;
  }
}

async function reloadConfig() {
  const data = await api("GET", "/api/config");
  state.config = data.config;
  state.configPath = data.path;
  state.rev = data.rev || 0;
  $("#config-path").textContent = data.path;
}

/* ---------- server-change events (SSE) ---------- */

function connectEvents() {
  const es = new EventSource("/api/events");
  es.addEventListener("rev", (e) => onServerRev(Number(e.data)));
}

async function onServerRev(rev) {
  if (!rev || rev === state.rev) return;
  if (state.dirty || state.rawDirty) {
    if (state.warnedRev !== rev) {
      state.warnedRev = rev;
      toast("Config changed on the server (agent/MCP) — save or reload to see it.", "warn");
    }
    return;
  }
  state.warnedRev = 0;
  try {
    await reloadConfig();
  } catch (e) {
    return;
  }
  renderTabs();
  renderPage();
}

/* ---------- init ---------- */

async function init() {
  applyEditorTheme();
  try {
    const data = await api("GET", "/api/config");
    state.config = data.config;
    state.configPath = data.path;
    state.rev = data.rev || 0;
    $("#config-path").textContent = data.path;
    renderTabs();
    renderPage();
    connectEvents();
  } catch (e) {
    const root = content();
    const p = document.createElement("p");
    p.textContent = "Failed to load config: " + e.message;
    root.appendChild(p);
  }
}

init();

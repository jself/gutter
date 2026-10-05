// gutter UI. Loaded by index.html after window.GUTTER_CONFIG is set.
//
// All mutable state lives in `S`; functions below read and write it and
// nothing else holds state between renders. Per-run configuration (CFG and
// the HAS_EDITOR/… constants) is immutable.
const ZOOM_MIN = 0.7, ZOOM_MAX = 2.0, ZOOM_STEP = 0.1;

const S = {
  data: null,            // the /diff payload: files, prior comments, doc, guide, draft
  comments: [],          // {id, path, side, line, end_line, snippet, body, severity?, resolved?, prior?}
  nextId: 1,             // next comment id
  dragStart: null,       // {fi, hi, li, side} while selecting a line range
  fileFilter: null,      // Full view: path to show, or null for all
  textFilter: '',        // sidebar filter text
  viewMode: (() => { try { return localStorage.getItem('gutter_view_mode'); } catch (e) { return null; } })() || 'guided',   // 'full' | 'guided'
  stop: (() => { const m = /^#s(\d+)$/.exec(location.hash); return m ? Math.max(0, +m[1] - 1) : 0; })(),       // guided view: current step index (mirrored in the URL hash #sN)
  loaded: false,         // initial render done; gates draft writes
  draft: { timer: null, last: null }, // debounce timer and last payload written
  find: { hits: [], cur: -1 },        // in-page find: <mark> elements and current index
  editingId: null,       // comment being edited in the modal
  zoom: (() => {
  const v = parseFloat((() => { try { return localStorage.getItem('gutter_zoom'); } catch (e) { return null; } })());
  return (v >= ZOOM_MIN && v <= ZOOM_MAX) ? v : 1.0;
})(),       // UI zoom factor, persisted
};
const CFG = window.GUTTER_CONFIG;
const HAS_EDITOR = CFG.hasEditor;
const COLLAPSE_THRESHOLD = CFG.collapse; // total +/- lines, 0 = never
const SYNC = CFG.sync;
const SEVERITY_MODE = CFG.severity;
const SEVERITIES = ['BLOCKING', 'IMPORTANT', 'SUGGESTION', 'QUESTION', 'NITPICK'];
function severityOptionsHTML(selected) {
  // Preserve an unrecognized severity (e.g. a future/hand-written token) as an
  // extra option so editing an unrelated field doesn't silently drop it.
  const opts = selected && !SEVERITIES.includes(selected) ? [selected, ...SEVERITIES] : SEVERITIES;
  const sel = opts.includes(selected) ? selected : 'QUESTION';
  return opts.map(s => `<option value="${escapeHtml(s)}"${s === sel ? ' selected' : ''}>${escapeHtml(s)}</option>`).join('');
}
function severitySelectHTML() {
  return SEVERITY_MODE ? `<select class="severity">${severityOptionsHTML('QUESTION')}</select>` : '';
}
function badgesHTML(c) {
  const sev = SEVERITY_MODE ? `<span class="sev">${escapeHtml(c.severity || 'QUESTION')}</span>` : '';
  const prior = c.prior ? `<span class="prior-badge">prior</span>` : '';
  return (sev || prior) ? `<span class="badges">${sev}${prior}</span>` : '';
}
function resolveBtnHTML(c) {
  const a = c.resolved ? 'unresolve' : 'resolve';
  return `<button type="button" data-id="${c.id}" data-action="${a}">${a}</button>`;
}

async function load() {
  const r = await fetch('/diff');
  S.data = await r.json();
  // Seed S.comments from prior review.md
  if (S.data.prior && S.data.prior.length) {
    for (const c of S.data.prior) {
      S.comments.push({
        id: S.nextId++,
        path: c.path,
        side: c.side || 'new',
        line: c.line,
        end_line: c.end_line || c.line,
        snippet: c.snippet || '',
        body: c.body,
        severity: c.severity || 'QUESTION',
        resolved: c.resolved || false,
        prior: true,
      });
    }
  }
  if (S.data.prior_general) {
    document.getElementById('general').value = S.data.prior_general;
  }
  if (S.data.draft) {
    // A draft is the later state: it already includes whatever prior comments
    // were loaded last time, plus the unsaved work. It replaces, not merges.
    S.comments = S.data.draft.comments.map(c => ({ ...c, id: S.nextId++ }));
    document.getElementById('general').value = S.data.draft.general || '';
    const status = document.getElementById('status');
    status.className = 'status ok';
    status.textContent = `restored ${S.comments.length} draft comment(s)`;
  }
  if (S.data.guide) {
    document.getElementById('viewSeg').style.display = '';
    syncViewSeg();
  }
  render();
  S.draft.last = draftPayload();
  S.loaded = true;
}

// ---------------- Draft persistence ----------------
// Every comment change (and general-feedback edit) is debounced to POST /draft,
// which writes <output>.draft.json. Save / Submit delete it server-side. On the
// next launch for the same rev the draft is restored, so a killed window or
// process can't lose work.
function draftPayload() {
  return JSON.stringify({
    general: document.getElementById('general').value,
    comments: S.comments.map(({id, ...rest}) => rest),
  });
}
function scheduleDraft() {
  if (!S.loaded) return;
  clearTimeout(S.draft.timer);
  S.draft.timer = setTimeout(async () => {
    const body = draftPayload();
    if (body === S.draft.last) return;
    S.draft.last = body;
    try {
      const r = await fetch('/draft', { method: 'POST', body });
      if (!r.ok) throw new Error(await r.text());
    } catch (e) {
      const status = document.getElementById('status');
      status.className = 'status err';
      status.textContent = 'draft not saved: ' + e.message;
    }
  }, 400);
}
document.getElementById('general').addEventListener('input', scheduleDraft);

function render() {
  renderView();
  findRefresh();
}

function renderView() {
  if (S.data.doc) { renderDocView(); renderSidebar(); renderComments(); return; }
  if (guided()) { renderGuided(); return; }
  renderSidebar();
  updateViewing();
  const container = document.getElementById('files');
  container.innerHTML = '';
  S.data.files.forEach((f, fi) => {
    if (S.fileFilter && f.path !== S.fileFilter) return;
    const fileDiv = document.createElement('div');
    fileDiv.className = 'file';
    fileDiv.dataset.path = f.path;
    const changed = f.add_count + f.del_count;
    const large = COLLAPSE_THRESHOLD > 0 && changed > COLLAPSE_THRESHOLD;
    const meta = `<span class="file-meta"><span class="add">+${f.add_count}</span> <span class="del">−${f.del_count}</span></span>`;
    const untrackedTag = f.untracked ? '<span class="untracked-badge" title="Untracked file (not yet added to git)">untracked</span>' : '';
    const binaryTag = f.binary ? '<span class="untracked-badge binary" title="Binary file; contents not shown">binary</span>' : '';
    const openBtn = HAS_EDITOR ? `<button class="open-btn" type="button" data-open-file="${escapeHtml(f.path)}">open in editor</button>` : '';
    fileDiv.innerHTML = `<h3>${openBtn}${escapeHtml(f.path)}${untrackedTag}${binaryTag}${meta}</h3>`;
    if (f.binary || !f.hunks || !f.hunks.length) {
      const note = document.createElement('div');
      note.className = 'hunk-header';
      note.textContent = f.binary ? 'Binary file changed' : 'No textual changes (mode or rename only)';
      fileDiv.appendChild(note);
    }
    if (large && S.fileFilter !== f.path) {
      fileDiv.classList.add('collapsed');
    }
    const hunks = f.hunks || [];
    hunks.forEach((h, hi) => {
      const hdr = document.createElement('div');
      hdr.className = 'hunk-header';
      hdr.textContent = h.header;
      fileDiv.appendChild(hdr);
      fileDiv.appendChild(buildHunkTable(f, fi, h, hi));
    });
    if (large) {
      const toggle = document.createElement('div');
      toggle.className = 'collapse-toggle';
      toggle.textContent = S.fileFilter === f.path
        ? `▼ ${changed} changed lines — click to collapse`
        : `▶ ${changed} changed lines (large) — click to expand`;
      toggle.addEventListener('click', () => {
        if (fileDiv.classList.contains('collapsed')) {
          fileDiv.classList.remove('collapsed');
          toggle.textContent = `▼ ${changed} changed lines — click to collapse`;
        } else {
          fileDiv.classList.add('collapsed');
          toggle.textContent = `▶ ${changed} changed lines (large) — click to expand`;
          fileDiv.scrollIntoView({ block: 'start', behavior: 'smooth' });
        }
      });
      fileDiv.appendChild(toggle);
    }
    container.appendChild(fileDiv);
  });
  wireOpenButtons();
  highlightCode();
  renderComments();
}

function wireOpenButtons() {
  document.querySelectorAll('[data-open-file]').forEach(b => {
    b.addEventListener('click', (e) => {
      e.stopPropagation();
      openInEditor(b.dataset.openFile, b.dataset.openLine || 1);
    });
  });
}
function highlightCode() {
  if (!window.hljs) return;
  document.querySelectorAll('td.text code:not(.fg-layer), td.text code.fg-layer, .doc-block pre code').forEach(el => {
    try { hljs.highlightElement(el); } catch (e) {}
  });
}

// buildHunkTable renders one hunk. opts.visible (Set of line indexes) folds
// every row outside visible ± CTX behind a click-to-expand row; opts.alsoIn(li)
// returns other stop ids that also claim the line (guided mode).
function buildHunkTable(f, fi, h, hi, opts = {}) {
  const tbl = document.createElement('table');
  tbl.className = 'diff';
  const CTX = 3;
  let show = null;
  if (opts.visible) {
    show = new Set();
    opts.visible.forEach(li => {
      for (let i = Math.max(0, li - CTX); i <= Math.min(h.lines.length - 1, li + CTX); i++) show.add(i);
    });
  }
  let hiddenRun = [];
  const flush = () => {
    if (!hiddenRun.length) return;
    const run = hiddenRun; hiddenRun = [];
    const tr = document.createElement('tr');
    tr.className = 'fold';
    tr.innerHTML = `<td colspan="3">… ${run.length} line${run.length === 1 ? '' : 's'} outside this step — click to show</td>`;
    tr.addEventListener('click', () => {
      run.forEach(li => { const r = tbl.querySelector(`tr[data-li="${li}"]`); if (r) r.classList.remove('folded'); });
      tr.remove();
    });
    tbl.appendChild(tr);
  };
  h.lines.forEach((l, li) => {
    const hidden = show && !show.has(li);
    if (hidden) hiddenRun.push(li); else flush();
        const tr = document.createElement('tr');
        tr.className = l.kind;
        tr.dataset.fi = fi; tr.dataset.hi = hi; tr.dataset.li = li;
        const oldL = l.old_line || '';
        const newL = l.new_line || '';
        const lineNum = l.kind === 'del' ? oldL : newL;
        const side = l.kind === 'del' ? 'old' : 'new';
        const langClass = f.lang ? ` class="language-${f.lang}"` : '';
        const prefix = l.kind === 'add' ? '+' : l.kind === 'del' ? '-' : ' ';
        let textCell;
        if (l.segments && l.segments.length) {
          const bg = l.segments.map(s => `<span class="${s.kind}">${escapeHtml(s.text)}</span>`).join('');
          textCell = `<span class="prefix">${prefix}</span><span class="content"><span class="bg-layer">${bg}</span><code class="fg-layer"${langClass}>${escapeHtml(l.text)}</code></span>`;
        } else {
          textCell = `<span class="prefix">${prefix}</span><code${langClass}>${escapeHtml(l.text)}</code>`;
        }
        tr.innerHTML = `
          <td class="ln" data-side="old">${oldL}</td>
          <td class="ln" data-side="new">${newL}</td>
          <td class="text">${textCell}</td>
        `;
        tr.querySelectorAll('td.ln').forEach(td => {
          td.addEventListener('mousedown', (e) => {
            e.preventDefault();
            // Clicking the old-side cell of an added line (no old number) still anchors to the new side.
            S.dragStart = { fi, hi, li, side: td.dataset.side === 'old' && l.old_line ? 'old' : (lineNum ? side : 'new') };
            highlightRange(S.dragStart, S.dragStart);
          });
          td.addEventListener('mouseenter', (e) => {
            if (!S.dragStart) return;
            highlightRange(S.dragStart, { fi, hi, li });
          });
          td.addEventListener('mouseup', (e) => {
            if (!S.dragStart) return;
            const end = { fi, hi, li };
            openCommentForm(S.dragStart, end);
            S.dragStart = null;
          });
        });
        if (hidden) tr.classList.add('folded');
        if (opts.alsoIn && l.kind !== 'ctx') {
          const ids = opts.alsoIn(li);
          if (ids.length) tr.querySelector('td.text').insertAdjacentHTML('beforeend', `<span class="also" title="Also claimed by step ${escapeHtml(ids.join(', '))}">also ${escapeHtml(ids.join(', '))}</span>`);
        }
        tbl.appendChild(tr);
  });
  flush();
  return tbl;
}

function highlightRange(a, b) {
  document.querySelectorAll('tr.selected').forEach(r => r.classList.remove('selected'));
  if (a.fi !== b.fi || a.hi !== b.hi) return;
  const [lo, hi] = a.li <= b.li ? [a.li, b.li] : [b.li, a.li];
  for (let i = lo; i <= hi; i++) {
    const tr = document.querySelector(`tr[data-fi="${a.fi}"][data-hi="${a.hi}"][data-li="${i}"]`);
    if (tr) tr.classList.add('selected');
  }
}

function openCommentForm(a, b) {
  if (a.fi !== b.fi || a.hi !== b.hi) {
    document.querySelectorAll('tr.selected').forEach(r => r.classList.remove('selected'));
    return;
  }
  const [lo, hi] = a.li <= b.li ? [a.li, b.li] : [b.li, a.li];
  const file = S.data.files[a.fi];
  const hunk = file.hunks[a.hi];
  const lines = hunk.lines.slice(lo, hi + 1);
  const snippet = lines.map(l => {
    const prefix = l.kind === 'add' ? '+' : l.kind === 'del' ? '-' : ' ';
    return prefix + l.text;
  }).join('\n');
  const firstLine = lines[0];
  const lastLine = lines[lines.length - 1];
  const side = a.side || 'new';
  const startNum = side === 'old' ? (firstLine.old_line || firstLine.new_line) : (firstLine.new_line || firstLine.old_line);
  const endNum = side === 'old' ? (lastLine.old_line || lastLine.new_line) : (lastLine.new_line || lastLine.old_line);

  // Insert a comment form row after `hi` (last row of selection)
  const anchor = document.querySelector(`tr[data-fi="${a.fi}"][data-hi="${a.hi}"][data-li="${hi}"]`);
  if (!anchor) return;
  // Remove existing form
  document.querySelectorAll('tr.comment-form-row').forEach(r => r.remove());

  const tr = document.createElement('tr');
  tr.className = 'comment-row comment-form-row';
  tr.innerHTML = `
    <td colspan="3">
      <div class="comment-form">
        <div style="color: var(--muted); margin-bottom: 4px;">${escapeHtml(file.path)}:${startNum}${endNum !== startNum ? '-' + endNum : ''}</div>
        ${severitySelectHTML()}
        <textarea autofocus placeholder="Comment for the agent..."></textarea>
        <div class="actions">
          <button type="button">Add comment</button>
          <button type="button" class="cancel">Cancel</button>
        </div>
      </div>
    </td>
  `;
  anchor.parentNode.insertBefore(tr, anchor.nextSibling);
  const ta = tr.querySelector('textarea');
  ta.focus();
  tr.querySelector('button').addEventListener('click', () => {
    const body = ta.value.trim();
    if (!body) { tr.remove(); return; }
    S.comments.push({
      id: S.nextId++,
      path: file.path,
      side,
      line: startNum,
      end_line: endNum,
      snippet,
      body,
      severity: SEVERITY_MODE ? tr.querySelector('select.severity').value : undefined,
    });
    tr.remove();
    document.querySelectorAll('tr.selected').forEach(r => r.classList.remove('selected'));
    renderComments();
  });
  tr.querySelector('button.cancel').addEventListener('click', () => {
    tr.remove();
    document.querySelectorAll('tr.selected').forEach(r => r.classList.remove('selected'));
  });
  ta.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      tr.querySelector('button').click();
    } else if (e.key === 'Escape') {
      tr.querySelector('button.cancel').click();
    }
  });
}

function updateViewing() {
  const el = document.getElementById('viewing');
  if (guided()) { el.textContent = `Step ${S.stop + 1} of ${guideStops().length}`; return; }
  el.textContent = S.fileFilter || 'All files';
}

document.getElementById('sidebarToggle').addEventListener('click', () => {
  const hidden = document.querySelector('.layout').classList.toggle('sidebar-hidden');
  try { localStorage.setItem('gutter_sidebar_hidden', hidden ? '1' : '0'); } catch (e) {}
});
if (localStorage.getItem('gutter_sidebar_hidden') === '1') {
  document.querySelector('.layout').classList.add('sidebar-hidden');
}

const HLJS_DARK = 'https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/styles/github-dark.min.css';
const HLJS_LIGHT = 'https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/styles/github.min.css';
function applyTheme(theme) {
  document.documentElement.setAttribute('data-theme', theme);
  document.getElementById('hljs-theme').href = theme === 'light' ? HLJS_LIGHT : HLJS_DARK;
  document.getElementById('themeToggle').textContent = theme === 'light' ? '☀' : '🌙';
}
const savedTheme = (() => { try { return localStorage.getItem('gutter_theme'); } catch (e) { return null; } })() || 'dark';
applyTheme(savedTheme);
document.getElementById('themeToggle').addEventListener('click', () => {
  const next = document.documentElement.getAttribute('data-theme') === 'light' ? 'dark' : 'light';
  applyTheme(next);
  try { localStorage.setItem('gutter_theme', next); } catch (e) {}
});

function applyZoom(level) {
  S.zoom = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, Math.round(level * 10) / 10));
  document.body.style.zoom = S.zoom;
  document.getElementById('zoomPct').textContent = Math.round(S.zoom * 100) + '%';
  try { localStorage.setItem('gutter_zoom', String(S.zoom)); } catch (e) {}
}
applyZoom(S.zoom);
document.getElementById('zoomIn').addEventListener('click', () => applyZoom(S.zoom + ZOOM_STEP));
document.getElementById('zoomOut').addEventListener('click', () => applyZoom(S.zoom - ZOOM_STEP));
document.getElementById('zoomPct').addEventListener('click', () => applyZoom(1.0));
document.addEventListener('keydown', (e) => {
  if (!(e.ctrlKey || e.metaKey)) return;
  if (e.key === '=' || e.key === '+') { e.preventDefault(); applyZoom(S.zoom + ZOOM_STEP); }
  else if (e.key === '-') { e.preventDefault(); applyZoom(S.zoom - ZOOM_STEP); }
  else if (e.key === '0') { e.preventDefault(); applyZoom(1.0); }
  else if (e.key === 'f' || e.key === 'F') { e.preventDefault(); findOpen(); }
});

// ---------------- In-page find ----------------
// The native webview (-window) has no browser find bar, so gutter provides one.
// Matches are wrapped in <mark> so they survive focus staying in the input;
// folded rows and collapsed files are expanded first so the search covers
// everything on the page. Marks are rebuilt after every render.
const findBar = document.getElementById('findBar');
const findInput = document.getElementById('findInput');
const findCount = document.getElementById('findCount');

function findOpen() {
  findBar.style.display = '';
  findInput.focus();
  findInput.select();
}
function findClose() {
  findBar.style.display = 'none';
  findClear();
  findCount.textContent = '';
  findInput.blur();
}
function findClear() {
  document.querySelectorAll('mark.find-hit').forEach(m => {
    const p = m.parentNode;
    p.replaceChild(document.createTextNode(m.textContent), m);
    p.normalize();
  });
  S.find.hits = []; S.find.cur = -1;
}
function findExpandAll() {
  document.querySelectorAll('tr.fold').forEach(r => r.click());
  document.querySelectorAll('.file.collapsed').forEach(f => { f.classList.remove('collapsed'); const t = f.querySelector('.collapse-toggle'); if (t) t.textContent = t.textContent.replace(/^▶.*$/, '▼ expanded — click to collapse'); });
}
function findRun(q, keepIndex) {
  const prev = S.find.cur;
  findClear();
  if (!q) { findCount.textContent = ''; return; }
  findExpandAll();
  const root = document.querySelector('main');
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, { acceptNode: (n) => {
    if (!n.nodeValue || !n.nodeValue.trim()) return NodeFilter.FILTER_REJECT;
    const p = n.parentElement;
    if (!p || p.closest('textarea, input, select, script, style, .bg-layer, .fold')) return NodeFilter.FILTER_REJECT;
    return NodeFilter.FILTER_ACCEPT;
  }});
  const nodes = [];
  for (let n = walker.nextNode(); n; n = walker.nextNode()) nodes.push(n);
  const lq = q.toLowerCase();
  nodes.forEach(n => {
    const text = n.nodeValue;
    const lower = text.toLowerCase();
    let i = lower.indexOf(lq);
    if (i < 0) return;
    const frag = document.createDocumentFragment();
    let last = 0;
    while (i >= 0) {
      if (i > last) frag.appendChild(document.createTextNode(text.slice(last, i)));
      const m = document.createElement('mark');
      m.className = 'find-hit';
      m.textContent = text.slice(i, i + q.length);
      frag.appendChild(m);
      S.find.hits.push(m);
      last = i + q.length;
      i = lower.indexOf(lq, last);
    }
    if (last < text.length) frag.appendChild(document.createTextNode(text.slice(last)));
    n.parentNode.replaceChild(frag, n);
  });
  if (!S.find.hits.length) { findCount.textContent = 'no matches'; return; }
  findGo(keepIndex && prev >= 0 ? Math.min(prev, S.find.hits.length - 1) : 0);
}
function findGo(i) {
  if (!S.find.hits.length) return;
  if (S.find.cur >= 0 && S.find.hits[S.find.cur]) S.find.hits[S.find.cur].classList.remove('current');
  S.find.cur = (i + S.find.hits.length) % S.find.hits.length;
  const m = S.find.hits[S.find.cur];
  m.classList.add('current');
  m.scrollIntoView({ block: 'center' });
  findCount.textContent = `${S.find.cur + 1} / ${S.find.hits.length}`;
}
function findRefresh() {
  if (findBar.style.display === 'none') return;
  if (findInput.value) findRun(findInput.value, true);
}
findInput.addEventListener('input', () => findRun(findInput.value, false));
findInput.addEventListener('keydown', (e) => {
  if (e.key === 'Enter') { e.preventDefault(); if (!S.find.hits.length) findRun(findInput.value, false); else findGo(S.find.cur + (e.shiftKey ? -1 : 1)); }
  else if (e.key === 'Escape') { e.preventDefault(); findClose(); }
  e.stopPropagation(); // keep [ ] and other page shortcuts from firing while typing
});

const copyBtnEl = document.getElementById('copyBtn');
if (copyBtnEl) copyBtnEl.addEventListener('click', async () => {
  const status = document.getElementById('status');
  status.className = 'status';
  status.textContent = 'copying…';
  try {
    const r = await fetch('/markdown', { method: 'POST', body: JSON.stringify({
      general: document.getElementById('general').value,
      comments: S.comments.map(({ id, prior, ...rest }) => rest),
    }) });
    if (!r.ok) throw new Error(await r.text());
    const md = await r.text();
    await navigator.clipboard.writeText(md);
    status.className = 'status ok';
    status.textContent = `copied ${md.length} chars`;
  } catch (e) {
    status.className = 'status err';
    status.textContent = 'copy failed: ' + e.message;
  }
});

function renderDocView() {
  updateViewing();
  const container = document.getElementById('files');
  container.innerHTML = '';
  const wrap = document.createElement('div');
  wrap.className = 'doc';
  S.data.doc.blocks.forEach((b, bi) => {
    const el = makeBlock(b, S.data.doc.path);
    el.dataset.bi = bi;
    wrap.appendChild(el);
  });
  container.appendChild(wrap);
  highlightCode();
}

// makeBlock renders one markdown block (doc mode or guide narration) as a
// clickable, commentable element anchored to path:line_start-line_end.
function makeBlock(b, path) {
  const el = document.createElement('div');
  el.className = 'doc-block';
  el.dataset.path = path;
  el.dataset.ls = b.line_start;
  el.dataset.le = b.line_end;
  el.innerHTML = b.html;
  el.addEventListener('click', (e) => {
    const a = e.target.closest('a');
    if (a) {
      const href = a.getAttribute('href') || '';
      if (/^https?:\/\//i.test(href)) {
        // Open external links in the system browser instead of navigating
        // the review away — vital in -window mode (webview has no tabs).
        e.preventDefault();
        fetch('/open-url?url=' + encodeURIComponent(href)).catch(() => {});
      } else if (href && !href.startsWith('#')) {
        // Relative/other links have no browser destination and would hijack
        // the webview (navigate the review UI to a dead gutter path); block.
        e.preventDefault();
      }
      return; // link click never opens the comment form
    }
    const sel = window.getSelection();
    if (sel && sel.toString().trim() && el.contains(sel.anchorNode)) return; // don't hijack in-block text selection
    openBlockCommentForm(b, path, el);
  });
  return el;
}

function openBlockCommentForm(b, path, blockEl) {
  document.querySelectorAll('.doc-comment-form').forEach(r => r.remove());
  document.querySelectorAll('.doc-block.selected').forEach(r => r.classList.remove('selected'));
  blockEl.classList.add('selected');
  const range = b.line_end !== b.line_start ? '-' + b.line_end : '';
  const form = document.createElement('div');
  form.className = 'comment-form doc-comment-form';
  form.innerHTML = `
    <div style="color: var(--muted); margin-bottom: 4px;">${escapeHtml(path)}:${b.line_start}${range}</div>
    ${severitySelectHTML()}
    <textarea placeholder="Comment for the agent..."></textarea>
    <div class="actions">
      <button type="button">Add comment</button>
      <button type="button" class="cancel">Cancel</button>
    </div>`;
  blockEl.after(form);
  const ta = form.querySelector('textarea');
  ta.focus();
  form.querySelector('button').addEventListener('click', () => {
    const body = ta.value.trim();
    if (!body) { form.remove(); blockEl.classList.remove('selected'); return; }
    S.comments.push({
      id: S.nextId++,
      path,
      side: 'new',
      line: b.line_start,
      end_line: b.line_end,
      snippet: b.source,
      body,
      severity: SEVERITY_MODE ? form.querySelector('select.severity').value : undefined,
    });
    form.remove();
    blockEl.classList.remove('selected');
    renderComments();
  });
  form.querySelector('button.cancel').addEventListener('click', () => {
    form.remove();
    blockEl.classList.remove('selected');
  });
  ta.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) form.querySelector('button').click();
    else if (e.key === 'Escape') form.querySelector('button.cancel').click();
  });
}

function renderDocComments() {
  renderUnattached(renderBlockComments(S.comments));
}

// renderBlockComments attaches comments to the .doc-block elements currently in
// the DOM (matched by path and line range) and returns the ones it could not.
function renderBlockComments(list) {
  document.querySelectorAll('.doc-saved-comment').forEach(r => r.remove());
  document.querySelectorAll('.doc-block.has-comment-block').forEach(el => el.classList.remove('has-comment-block'));
  const blocks = [...document.querySelectorAll('.doc-block[data-path]')];
  const rest = [];
  list.forEach(c => {
    const blockEl = blocks.find(el => el.dataset.path === c.path && +el.dataset.ls <= c.line && c.line <= +el.dataset.le);
    if (!blockEl) { rest.push(c); return; }
    blockEl.classList.add('has-comment-block');
    const range = c.end_line && c.end_line !== c.line ? '-' + c.end_line : '';
    const div = document.createElement('div');
    div.className = 'doc-saved-comment';
    div.innerHTML = `
      <div class="saved-comment ${c.prior ? 'prior' : ''} ${c.resolved ? 'resolved' : ''}">
        <div class="loc">💬 ${escapeHtml(c.path)}:${c.line}${range}${badgesHTML(c)}</div>
        <div class="body">${escapeHtml(c.body)}</div>
        <div class="controls">
          ${resolveBtnHTML(c)}
          <button type="button" data-id="${c.id}" data-action="edit">edit</button>
          <button type="button" data-id="${c.id}" data-action="del">delete</button>
        </div>
      </div>`;
    blockEl.after(div);
    div.querySelectorAll('button').forEach(btn => {
      btn.addEventListener('click', () => {
        const id = +btn.dataset.id, action = btn.dataset.action;
        const idx = S.comments.findIndex(x => x.id === id);
        if (idx < 0) return;
        if (action === 'del') { S.comments.splice(idx, 1); renderComments(); }
        else if (action === 'edit') { openEditModal(S.comments[idx]); }
        else if (action === 'resolve' || action === 'unresolve') { S.comments[idx].resolved = (action === 'resolve'); renderComments(); }
      });
    });
  });
  return rest;
}

function renderOutline() {
  const ul = document.getElementById('fileList');
  ul.innerHTML = '';
  S.data.doc.blocks.forEach((b, bi) => {
    const m = b.html.match(/^<h([1-6])[^>]*>([\s\S]*?)<\/h\1>/i);
    if (!m) return;
    const li = document.createElement('li');
    li.dataset.bi = bi;
    li.style.paddingLeft = (4 + (parseInt(m[1], 10) - 1) * 10) + 'px';
    const text = m[2].replace(/<[^>]+>/g, '');
    li.innerHTML = `<span class="path" title="${escapeHtml(text)}">${escapeHtml(text)}</span>`;
    li.addEventListener('click', () => {
      const el = document.querySelector(`.doc-block[data-bi="${bi}"]`);
      if (el) el.scrollIntoView({ block: 'start', behavior: 'smooth' });
    });
    ul.appendChild(li);
  });
}

function renderSidebar() {
  if (S.data.doc) { renderOutline(); return; }
  document.querySelector('aside.sidebar h4').textContent = guided() ? 'Guide' : 'Files';
  document.getElementById('fileFilter').style.display = guided() ? 'none' : '';
  if (guided()) { renderGuideSidebar(); return; }
  const ul = document.getElementById('fileList');
  ul.innerHTML = '';
  S.data.files.forEach(f => {
    const li = document.createElement('li');
    li.dataset.path = f.path;
    if (S.fileFilter === f.path) li.classList.add('active');
    const slash = f.path.lastIndexOf('/');
    const dir = slash >= 0 ? f.path.slice(0, slash + 1) : '';
    const base = slash >= 0 ? f.path.slice(slash + 1) : f.path;
    li.innerHTML = `
      <span class="path" title="${escapeHtml(f.path)}"><span class="dir">${escapeHtml(dir)}</span>${escapeHtml(base)}</span>
      ${f.untracked ? '<span class="untracked-badge" title="Untracked file (not yet added to git)">U</span>' : ''}
      <span class="counts"><span class="add">+${f.add_count}</span> <span class="del">−${f.del_count}</span></span>
    `;
    li.addEventListener('click', () => {
      if (S.fileFilter === f.path) {
        S.fileFilter = null;
      } else {
        S.fileFilter = f.path;
      }
      document.getElementById('clearFilter').style.display = S.fileFilter ? '' : 'none';
      render();
      const el = document.querySelector(`#files .file[data-path="${CSS.escape(f.path)}"]`);
      if (el) el.scrollIntoView({ block: 'start', behavior: 'smooth' });
    });
    if (S.textFilter && !f.path.toLowerCase().includes(S.textFilter)) li.classList.add('hidden');
    ul.appendChild(li);
  });
}

document.getElementById('fileFilter').addEventListener('input', (e) => {
  S.textFilter = e.target.value.trim().toLowerCase();
  renderSidebar();
});
document.getElementById('clearFilter').addEventListener('click', () => {
  S.fileFilter = null;
  document.getElementById('clearFilter').style.display = 'none';
  render();
});

async function openInEditor(path, line) {
  try {
    const r = await fetch(`/open?path=${encodeURIComponent(path)}&line=${encodeURIComponent(line)}`);
    if (!r.ok) {
      const status = document.getElementById('status');
      status.className = 'status err';
      status.textContent = 'open: ' + await r.text();
    }
  } catch (e) {
    console.error(e);
  }
}

function findAnchor(c) {
  const fi = S.data.files.findIndex(f => f.path === c.path);
  if (fi < 0) return null;
  const file = S.data.files[fi];
  const target = c.end_line || c.line;
  for (let hi = 0; hi < file.hunks.length; hi++) {
    const h = file.hunks[hi];
    for (let li = 0; li < h.lines.length; li++) {
      const l = h.lines[li];
      if (l.old_line === target || l.new_line === target) {
        return { fi, hi, li, el: document.querySelector(`tr[data-fi="${fi}"][data-hi="${hi}"][data-li="${li}"]`) };
      }
    }
  }
  return null;
}

function markLineNumbers() {
  document.querySelectorAll('td.ln.has-comment').forEach(td => td.classList.remove('has-comment'));
  S.comments.forEach(c => {
    const fi = S.data.files.findIndex(f => f.path === c.path);
    if (fi < 0) return;
    const file = S.data.files[fi];
    const lo = c.line, hi = c.end_line || c.line;
    for (let hii = 0; hii < file.hunks.length; hii++) {
      const h = file.hunks[hii];
      for (let li = 0; li < h.lines.length; li++) {
        const l = h.lines[li];
        const num = c.side === 'old' ? l.old_line : l.new_line;
        if (num && num >= lo && num <= hi) {
          const tr = document.querySelector(`tr[data-fi="${fi}"][data-hi="${hii}"][data-li="${li}"]`);
          if (tr) {
            const td = tr.querySelector(`td.ln[data-side="${c.side}"]`);
            if (td) td.classList.add('has-comment');
          }
        }
      }
    }
  });
}

function renderComments() {
  scheduleDraft();
  if (S.data.doc) { renderDocComments(); return; }
  document.querySelectorAll('tr.saved-comment-row').forEach(r => r.remove());
  const unattached = [];
  const g = guided();
  const list = g ? renderBlockComments(S.comments) : S.comments;
  const guideCmts = []; // narration comments while in Full view
  list.forEach(c => {
    if (S.data.guide && c.path === S.data.guide.path) {
      // Narration comment: off-screen on another stop (guided) or listed in
      // its own panel (full); unattached only if no block in the guide has it.
      if (!guideHasBlockForLine(c.line)) unattached.push(c);
      else if (!g) guideCmts.push(c);
      return;
    }
    const anchor = findAnchor(c);
    if (!anchor) { unattached.push(c); return; }
    if (!anchor.el) { if (!g) unattached.push(c); return; } // guided: row is on another stop
    if (g && anchor.el.classList.contains('folded')) return; // guided: line belongs to another stop
    const tr = document.createElement('tr');
    tr.className = 'comment-row saved-comment-row';
    const range = c.end_line && c.end_line !== c.line ? '-' + c.end_line : '';
    const openBtn = HAS_EDITOR ? `<button type="button" class="open-btn" data-id="${c.id}" data-action="open">open</button>` : '';
    tr.innerHTML = `
      <td colspan="3">
        <div class="saved-comment ${c.prior ? 'prior' : ''} ${c.resolved ? 'resolved' : ''}">
          <div class="loc">💬 ${escapeHtml(c.path)}:${c.line}${range}${badgesHTML(c)}</div>
          <div class="body">${escapeHtml(c.body)}</div>
          <div class="controls">
            ${resolveBtnHTML(c)}
            <button type="button" data-id="${c.id}" data-action="edit">edit</button>
            <button type="button" data-id="${c.id}" data-action="del">delete</button>
            ${openBtn}
          </div>
        </div>
      </td>
    `;
    anchor.el.parentNode.insertBefore(tr, anchor.el.nextSibling);
    tr.querySelectorAll('button').forEach(b => {
      b.addEventListener('click', () => {
        const id = +b.dataset.id;
        const action = b.dataset.action;
        const idx = S.comments.findIndex(x => x.id === id);
        if (idx < 0) return;
        if (action === 'del') {
          S.comments.splice(idx, 1);
          renderComments();
        } else if (action === 'edit') {
          openEditModal(S.comments[idx]);
        } else if (action === 'open') {
          openInEditor(S.comments[idx].path, S.comments[idx].line);
        } else if (action === 'resolve' || action === 'unresolve') {
          S.comments[idx].resolved = (action === 'resolve');
          renderComments();
        }
      });
    });
  });
  renderUnattached(unattached);
  renderUnattached(guideCmts, { id: 'guide-comments', title: 'Comments on the review guide', color: 'var(--comment-fg)', hint: 'shown in place in the Guided view' });
  markLineNumbers();
  if (g) renderSidebar(); // refresh per-stop comment counts
}

function renderUnattached(list, opts = {}) {
  const id = opts.id || 'unattached';
  const title = opts.title || 'Prior comments without matching diff lines';
  const color = opts.color || '#d29922';
  let panel = document.getElementById(id);
  if (!list.length) {
    if (panel) panel.remove();
    return;
  }
  if (!panel) {
    panel = document.createElement('div');
    panel.id = id;
    panel.className = 'file';
    document.querySelector('main').insertBefore(panel, document.getElementById('files'));
  }
  const hint = opts.hint ? ` <span style="color: var(--muted); font-weight: 400;">— ${escapeHtml(opts.hint)}</span>` : '';
  panel.innerHTML = `<h3 style="color: ${color};">${escapeHtml(title)} (${list.length})${hint}</h3>` +
    list.map(c => {
      const range = c.end_line && c.end_line !== c.line ? '-' + c.end_line : '';
      return `<div style="padding: 10px 12px; border-top: 1px solid var(--border);">
        <div class="saved-comment${c.prior ? ' prior' : ''}${c.resolved ? ' resolved' : ''}">
          <div class="loc">💬 ${escapeHtml(c.path)}:${c.line}${range}${badgesHTML(c)}</div>
          <div class="body">${escapeHtml(c.body)}</div>
          <div class="controls">
            ${resolveBtnHTML(c)}
            <button type="button" data-id="${c.id}" data-action="edit">edit</button>
            <button type="button" data-id="${c.id}" data-action="del">delete</button>
          </div>
        </div>
      </div>`;
    }).join('');
  panel.querySelectorAll('button').forEach(b => {
    b.addEventListener('click', () => {
      const id = +b.dataset.id;
      const action = b.dataset.action;
      const idx = S.comments.findIndex(x => x.id === id);
      if (idx < 0) return;
      if (action === 'del') { S.comments.splice(idx, 1); renderComments(); }
      else if (action === 'edit') { openEditModal(S.comments[idx]); }
      else if (action === 'resolve' || action === 'unresolve') { S.comments[idx].resolved = (action === 'resolve'); renderComments(); }
    });
  });
}

function openEditModal(c) {
  S.editingId = c.id;
  const range = c.end_line && c.end_line !== c.line ? '-' + c.end_line : '';
  document.getElementById('editLoc').textContent = `${c.path}:${c.line}${range}`;
  const editSev = document.getElementById('editSeverity');
  editSev.style.display = SEVERITY_MODE ? '' : 'none';
  if (SEVERITY_MODE) editSev.innerHTML = severityOptionsHTML(c.severity || 'QUESTION');
  document.getElementById('editBody').value = c.body;
  document.getElementById('editModal').classList.add('show');
  setTimeout(() => document.getElementById('editBody').focus(), 0);
}
function closeEditModal() {
  S.editingId = null;
  document.getElementById('editModal').classList.remove('show');
}
document.getElementById('editCancel').addEventListener('click', closeEditModal);
document.getElementById('editSave').addEventListener('click', () => {
  if (S.editingId == null) return;
  const c = S.comments.find(x => x.id === S.editingId);
  if (!c) return closeEditModal();
  const v = document.getElementById('editBody').value.trim();
  if (!v) { S.comments.splice(S.comments.indexOf(c), 1); }
  else {
    c.body = v;
    if (SEVERITY_MODE) c.severity = document.getElementById('editSeverity').value;
    c.prior = false;
  }
  closeEditModal();
  renderComments();
});
document.getElementById('editBody').addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) document.getElementById('editSave').click();
  else if (e.key === 'Escape') closeEditModal();
});
document.getElementById('editModal').addEventListener('click', (e) => {
  if (e.target.id === 'editModal') closeEditModal();
});

async function save() {
  const status = document.getElementById('status');
  status.className = 'status';
  status.textContent = 'saving…';
  clearTimeout(S.draft.timer);
  S.draft.last = draftPayload(); // the server deletes the draft on success; don't recreate it
  const body = {
    general: document.getElementById('general').value,
    comments: S.comments.map(({id, ...rest}) => rest),
  };
  try {
    const r = await fetch('/save', { method: 'POST', body: JSON.stringify(body) });
    const t = await r.text();
    if (r.ok) {
      status.className = 'status ok';
      status.textContent = t.trim();
    } else {
      status.className = 'status err';
      status.textContent = 'error: ' + t;
    }
  } catch (e) {
    status.className = 'status err';
    status.textContent = 'error: ' + e.message;
  }
}

async function submit_review() {
  const submitBtn = document.getElementById('submitBtn');
  if (submitBtn) submitBtn.disabled = true;
  const status = document.getElementById('status');
  status.className = 'status';
  status.textContent = 'submitting…';
  clearTimeout(S.draft.timer);
  S.draft.last = draftPayload(); // the server deletes the draft on success; don't recreate it
  const body = {
    general: document.getElementById('general').value,
    comments: S.comments.map(({id, ...rest}) => rest),
  };
  try {
    const r = await fetch('/submit', { method: 'POST', body: JSON.stringify(body) });
    const t = await r.text();
    if (r.ok) {
      document.body.innerHTML = '<main><p style="padding:20px;color:var(--muted)">' + escapeHtml(t.trim()) + '</p></main>';
    } else {
      status.className = 'status err';
      status.textContent = 'error: ' + t;
    }
  } catch (e) {
    status.className = 'status err';
    status.textContent = 'error: ' + e.message;
  }
}

async function quit() {
  await fetch('/quit');
  document.body.innerHTML = '<main><p style="padding:20px;color:var(--muted)">Closed. You can close this tab.</p></main>';
}

// ---------------- Guided review ----------------

function guided() { return !!(S.data && S.data.guide) && S.viewMode === 'guided'; }

// guideStops flattens parts/steps into the walk order: each step, or a part
// with no steps, then the synthetic Unassigned part.
function guideStops() {
  const g = S.data.guide;
  const out = [];
  g.parts.forEach(p => {
    out.push({ node: p, part: p, isPart: true });
    (p.steps || []).forEach(st => out.push({ node: st, part: p }));
  });
  if (g.unassigned) out.push({ node: g.unassigned, part: g.unassigned, isPart: true, unassigned: true });
  return out;
}

function claimIndex() {
  const idx = new Map();
  guideStops().forEach(st => st.node.claims.forEach(c => {
    const k = c.join(':');
    if (!idx.has(k)) idx.set(k, []);
    idx.get(k).push(st.node.id);
  }));
  return idx;
}

function stopBlocks(stops, i) {
  const blocks = [...(stops[i].node.blocks || [])];
  if (i === 0) blocks.push(...S.data.guide.overview);
  return blocks;
}

function guideHasBlockForLine(line) {
  const stops = guideStops();
  for (let i = 0; i < stops.length; i++) {
    if (stopBlocks(stops, i).some(b => b.line_start <= line && line <= b.line_end)) return true;
  }
  return false;
}

function stopIndexForComment(stops, c) {
  if (c.path === S.data.guide.path) {
    for (let i = 0; i < stops.length; i++) {
      if (stopBlocks(stops, i).some(b => b.line_start <= c.line && c.line <= b.line_end)) return i;
    }
    return -1;
  }
  const fi = S.data.files.findIndex(f => f.path === c.path);
  if (fi < 0) return -1;
  const f = S.data.files[fi];
  const target = c.end_line || c.line;
  for (let hi = 0; hi < f.hunks.length; hi++) {
    const lines = f.hunks[hi].lines;
    for (let li = 0; li < lines.length; li++) {
      const l = lines[li];
      const num = c.side === 'old' ? l.old_line : l.new_line;
      if (num !== target) continue;
      for (let i = 0; i < stops.length; i++) {
        if (stops[i].node.claims.some(cl => cl[0] === fi && cl[1] === hi && cl[2] === li)) return i;
      }
    }
  }
  return -1;
}

function syncViewSeg() {
  document.getElementById('viewFull').classList.toggle('on', S.viewMode !== 'guided');
  document.getElementById('viewGuided').classList.toggle('on', S.viewMode === 'guided');
}
function setViewMode(mode) {
  S.viewMode = mode;
  try { localStorage.setItem('gutter_view_mode', mode); } catch (e) {}
  syncViewSeg();
  render();
  window.scrollTo(0, 0);
}
document.getElementById('viewFull').addEventListener('click', () => setViewMode('full'));
document.getElementById('viewGuided').addEventListener('click', () => setViewMode('guided'));

function gotoStop(i) {
  const n = guideStops().length;
  if (!n) return;
  S.stop = Math.max(0, Math.min(n - 1, i));
  try { history.replaceState(null, '', '#s' + (S.stop + 1)); } catch (e) {}
  render();
  window.scrollTo(0, 0);
}
document.addEventListener('keydown', (e) => {
  if (!guided() || e.ctrlKey || e.metaKey || e.altKey) return;
  const t = e.target;
  if (t && (t.tagName === 'TEXTAREA' || t.tagName === 'INPUT' || t.tagName === 'SELECT' || t.isContentEditable)) return;
  if (e.key === ']') { e.preventDefault(); gotoStop(S.stop + 1); }
  else if (e.key === '[') { e.preventDefault(); gotoStop(S.stop - 1); }
});

function renderGuideSidebar() {
  const ul = document.getElementById('fileList');
  ul.innerHTML = '';
  document.getElementById('clearFilter').style.display = 'none';
  const stops = guideStops();
  const counts = new Array(stops.length).fill(0);
  S.comments.forEach(c => { const i = stopIndexForComment(stops, c); if (i >= 0) counts[i]++; });
  stops.forEach((st, si) => {
    const li = document.createElement('li');
    li.className = st.isPart ? 'guide-part' : 'guide-step';
    if (si === S.stop) li.classList.add('active');
    const badge = st.unassigned ? `<span class="unassigned-badge" title="Changed lines no step claims">${st.node.claims.length}</span>` : '';
    const cnt = counts[si] ? `<span class="counts cmt" title="${counts[si]} comment(s) on this step">💬 ${counts[si]}</span>` : '';
    li.innerHTML = `<span class="path" title="${escapeHtml(st.node.title)}">${escapeHtml(st.node.title)}</span>${badge}${cnt}`;
    li.addEventListener('click', () => gotoStop(si));
    ul.appendChild(li);
  });
}

function renderGuided() {
  renderSidebar();
  updateViewing();
  const container = document.getElementById('files');
  container.innerHTML = '';
  const stops = guideStops();
  if (S.stop >= stops.length) S.stop = Math.max(0, stops.length - 1);
  const st = stops[S.stop];
  if (!st) return;
  const g = S.data.guide;
  const wrap = document.createElement('div');
  wrap.className = 'guide';
  if (S.stop === 0 && g.overview.length) {
    const ov = document.createElement('div');
    ov.className = 'guide-overview';
    g.overview.forEach(b => ov.appendChild(makeBlock(b, g.path)));
    wrap.appendChild(ov);
  }
  const head = document.createElement('div');
  head.className = 'guide-head';
  const crumb = !st.isPart ? `<span class="crumb">${escapeHtml(st.part.title)} › </span>` : '';
  const badge = st.unassigned ? `<span class="unassigned-badge">${st.node.claims.length} unclaimed line${st.node.claims.length === 1 ? '' : 's'}</span>` : '';
  const kind = st.unassigned ? 'Unassigned' : st.isPart ? 'Part' : 'Step';
  head.innerHTML = `<div class="guide-kicker">${kind} · ${S.stop + 1} of ${stops.length}</div><h2>${crumb}${escapeHtml(st.node.title)}${badge}</h2>`;
  wrap.appendChild(head);
  if (st.unassigned) {
    const p = document.createElement('p');
    p.className = 'guide-warning';
    p.textContent = 'No step in the guide claims these changed lines. Review them here; the guide should be fixed to cover them.';
    wrap.appendChild(p);
  }
  (st.node.blocks || []).forEach(b => wrap.appendChild(makeBlock(b, g.path)));
  if (st.isPart && st.node.steps && st.node.steps.length) {
    const ol = document.createElement('ol');
    ol.className = 'guide-steps';
    st.node.steps.forEach(step => {
      const li = document.createElement('li');
      li.textContent = step.title;
      const target = stops.findIndex(x => x.node === step);
      li.addEventListener('click', () => gotoStop(target));
      ol.appendChild(li);
    });
    wrap.appendChild(ol);
  }
  if (st.node.refs && st.node.refs.length) {
    const ul = document.createElement('ul');
    ul.className = 'guide-refs';
    st.node.refs.forEach(r => {
      const li = document.createElement('li');
      const loc = r.start ? `${r.path}:${r.start}${r.end !== r.start ? '-' + r.end : ''}` : r.path;
      if (r.hits) {
        li.className = 'live';
        li.innerHTML = `<code>${escapeHtml(loc)}</code><span class="hits">${r.hits} line${r.hits === 1 ? '' : 's'}</span>`;
        li.addEventListener('click', () => {
          const el = container.querySelector(`.file[data-path="${CSS.escape(r.path)}"]`);
          if (el) el.scrollIntoView({ block: 'start', behavior: 'smooth' });
        });
      } else {
        li.innerHTML = `<code>${escapeHtml(loc)}</code><span class="dead">⚠ dead reference: matches no changed line</span>`;
      }
      ul.appendChild(li);
    });
    wrap.appendChild(ul);
  }
  container.appendChild(wrap);

  const idx = claimIndex();
  const byFile = new Map();
  st.node.claims.forEach(([fi, hi, li]) => {
    if (!byFile.has(fi)) byFile.set(fi, new Map());
    const m = byFile.get(fi);
    if (!m.has(hi)) m.set(hi, new Set());
    m.get(hi).add(li);
  });
  [...byFile.keys()].sort((a, b) => a - b).forEach(fi => {
    const f = S.data.files[fi];
    const hunks = byFile.get(fi);
    let add = 0, del = 0;
    hunks.forEach((set, hi) => set.forEach(li => {
      const k = f.hunks[hi].lines[li].kind;
      if (k === 'add') add++; else if (k === 'del') del++;
    }));
    const fileDiv = document.createElement('div');
    fileDiv.className = 'file';
    fileDiv.dataset.path = f.path;
    const openBtn = HAS_EDITOR ? `<button class="open-btn" type="button" data-open-file="${escapeHtml(f.path)}">open in editor</button>` : '';
    const untrackedTag = f.untracked ? '<span class="untracked-badge" title="Untracked file (not yet added to git)">untracked</span>' : '';
    fileDiv.innerHTML = `<h3>${openBtn}${escapeHtml(f.path)}${untrackedTag}<span class="file-meta"><span class="add">+${add}</span> <span class="del">−${del}</span></span></h3>`;
    [...hunks.keys()].sort((a, b) => a - b).forEach(hi => {
      const h = f.hunks[hi];
      const hdr = document.createElement('div');
      hdr.className = 'hunk-header';
      hdr.textContent = h.header;
      fileDiv.appendChild(hdr);
      fileDiv.appendChild(buildHunkTable(f, fi, h, hi, {
        visible: hunks.get(hi),
        alsoIn: (li) => (idx.get(`${fi}:${hi}:${li}`) || []).filter(id => id !== st.node.id),
      }));
    });
    container.appendChild(fileDiv);
  });

  const nav = document.createElement('div');
  nav.className = 'guide-nav';
  nav.innerHTML = `<button type="button" class="secondary" id="prevStop"${S.stop === 0 ? ' disabled' : ''}>← Previous</button>
    <span class="pos">${S.stop + 1} / ${stops.length}</span>
    <button type="button" id="nextStop"${S.stop === stops.length - 1 ? ' disabled' : ''}>Next →</button>
    <span class="keys">[ and ] to move</span>`;
  nav.querySelector('#prevStop').addEventListener('click', () => gotoStop(S.stop - 1));
  nav.querySelector('#nextStop').addEventListener('click', () => gotoStop(S.stop + 1));
  container.appendChild(nav);
  wireOpenButtons();
  highlightCode();
  renderComments();
}

function escapeHtml(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}

document.addEventListener('mouseup', () => { if (S.dragStart) { /* handled per-cell */ } });
load();

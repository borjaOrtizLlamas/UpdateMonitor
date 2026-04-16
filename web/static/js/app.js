'use strict';

// ── Config ──────────────────────────────────────────────────────────────────
const API = '/api';
const POLL_INTERVAL_MS = 60_000;

// ── State ────────────────────────────────────────────────────────────────────
let projects = [];
let selectedProject = null;

// ── DOM refs ─────────────────────────────────────────────────────────────────
const grid         = document.getElementById('project-grid');
const emptyState   = document.getElementById('empty-state');
const loading      = document.getElementById('loading');
const summaryBar   = document.getElementById('summary-bar');
const sumTotal     = document.getElementById('sum-total').querySelector('.summary-num');
const sumUpdates   = document.getElementById('sum-updates').querySelector('.summary-num');
const sumImportant = document.getElementById('sum-important').querySelector('.summary-num');
const sumCVEs      = document.getElementById('sum-cves').querySelector('.summary-num');
const lastUpdated  = document.getElementById('last-updated');

// Modal
const modalOverlay   = document.getElementById('modal-overlay');
const formAddProject = document.getElementById('form-add-project');
const formError      = document.getElementById('form-error');
const btnAdd         = document.getElementById('btn-add');
const btnModalClose  = document.getElementById('btn-modal-close');
const btnModalCancel = document.getElementById('btn-modal-cancel');
const fURL           = document.getElementById('f-url');
const btnAdvToggle   = document.getElementById('btn-advanced-toggle');
const advancedFields = document.getElementById('advanced-fields');

// Drawer
const drawerOverlay       = document.getElementById('drawer-overlay');
const drawerBody          = document.getElementById('drawer-body');
const drawerTitle         = document.getElementById('drawer-title');
const btnDrawerClose      = document.getElementById('btn-drawer-close');
const btnDrawerDel        = document.getElementById('btn-drawer-delete');
const drawerUpdateActions = document.getElementById('drawer-update-actions');
const drawerSnoozeActions = document.getElementById('drawer-snooze-actions');
const btnConfirmUpdate    = document.getElementById('btn-confirm-update');
const btnSnooze           = document.getElementById('btn-snooze');
const btnUnsnooze         = document.getElementById('btn-unsnooze');

// ── API helpers ──────────────────────────────────────────────────────────────
async function apiFetch(path, opts = {}) {
  const adminKey = sessionStorage.getItem('adminKey') || '';
  const headers = { 'Content-Type': 'application/json', ...opts.headers };
  if (adminKey) headers['X-Admin-Key'] = adminKey;

  const res = await fetch(API + path, { ...opts, headers });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(body.error || res.statusText);
  }
  if (res.status === 204) return null;
  return res.json();
}

function ensureAdminKey() {
  if (!sessionStorage.getItem('adminKey')) {
    const key = prompt('Enter admin key (leave blank if auth is disabled):') || '';
    sessionStorage.setItem('adminKey', key);
  }
}

// ── Data loading ─────────────────────────────────────────────────────────────
async function loadProjects() {
  showLoading(true);
  try {
    projects = await apiFetch('/projects');
    renderAll();
  } catch (err) {
    console.error('Failed to load projects:', err);
  } finally {
    showLoading(false);
  }
}

function renderAll() {
  renderSummary();
  renderGrid();
  lastUpdated.textContent = 'Updated ' + new Date().toLocaleTimeString();
}

// ── Summary bar ──────────────────────────────────────────────────────────────
function renderSummary() {
  if (!projects || projects.length === 0) {
    summaryBar.classList.add('hidden');
    return;
  }
  summaryBar.classList.remove('hidden');
  const updates   = projects.filter(p => p.update_available).length;
  const important = projects.filter(p => p.update_important).length;
  const cves      = projects.filter(p => p.has_cves).length;
  sumTotal.textContent     = projects.length;
  sumUpdates.textContent   = updates;
  sumImportant.textContent = important;
  sumCVEs.textContent      = cves;
}

// ── Grid rendering ────────────────────────────────────────────────────────────
function renderGrid() {
  grid.innerHTML = '';

  if (!projects || projects.length === 0) {
    emptyState.classList.remove('hidden');
    return;
  }
  emptyState.classList.add('hidden');

  const sorted = [...projects].sort((a, b) => {
    if (a.update_important && !b.update_important) return -1;
    if (!a.update_important && b.update_important) return 1;
    if (a.has_cves && !b.has_cves) return -1;
    if (!a.has_cves && b.has_cves) return 1;
    if (a.update_available && !b.update_available) return -1;
    if (!a.update_available && b.update_available) return 1;
    return a.name.localeCompare(b.name);
  });

  sorted.forEach(p => grid.appendChild(buildCard(p)));
}

function buildCard(p) {
  const isSnoozed = !!p.snoozed_until_version;
  const card = document.createElement('div');
  card.className = [
    'card',
    p.update_important          ? 'is-important' : '',
    p.update_available          ? 'has-update'   : '',
    p.has_cves                  ? 'has-cves'     : '',
    isSnoozed                   ? 'is-snoozed'   : '',
  ].filter(Boolean).join(' ');
  card.setAttribute('data-id', p.id);

  const statusBadgeHtml = cardStatusBadge(p);
  const cveBadgeHtml    = p.has_cves ? cveSummaryBadge(p.cves) : '';

  card.innerHTML = `
    <div class="card-header">
      <div style="min-width:0">
        <div class="card-name">${esc(p.name)}</div>
        <div class="card-url">${esc(p.url)}</div>
      </div>
      <div style="display:flex;flex-direction:column;gap:.3rem;align-items:flex-end;flex-shrink:0">
        ${statusBadgeHtml}
        ${cveBadgeHtml}
      </div>
    </div>
    <div class="card-versions">
      <div class="version-block">
        <div class="version-label">Current</div>
        <div class="version-tag">${esc(p.current_version || '—')}</div>
      </div>
      <div class="version-block">
        <div class="version-label">Latest</div>
        <div class="version-tag">${esc(p.latest_version || '—')}</div>
      </div>
    </div>
    ${p.update_summary ? `<div class="card-summary">${esc(p.update_summary)}</div>` : ''}
    <div class="card-footer">
      <span class="platform-badge">${esc(p.platform)}</span>
      <span class="last-checked">${p.last_checked ? 'Checked ' + relativeTime(p.last_checked) : 'Not checked yet'}</span>
    </div>
  `;

  card.addEventListener('click', () => openDrawer(p));
  return card;
}

function cardStatusBadge(p) {
  if (p.snoozed_until_version) {
    return `<span class="badge badge-snoozed">Snoozed</span>`;
  }
  if (!p.latest_version) {
    return `<span class="badge badge-unknown">Unknown</span>`;
  }
  if (p.update_important) {
    return `<span class="badge badge-important">Important update</span>`;
  }
  if (p.update_available) {
    return `<span class="badge badge-update">Update available</span>`;
  }
  return `<span class="badge badge-up-to-date">Up to date</span>`;
}

function cveSummaryBadge(cves) {
  if (!cves || cves.length === 0) return '';
  const hasCrit = cves.some(c => c.severity === 'CRITICAL');
  const cls = hasCrit ? 'badge-cve-crit' : 'badge-cve';
  return `<span class="badge ${cls}">⚠ ${cves.length} CVE${cves.length > 1 ? 's' : ''}</span>`;
}

// ── Drawer ────────────────────────────────────────────────────────────────────
function openDrawer(p) {
  selectedProject = p;
  drawerTitle.textContent = p.name;

  // ── drawer body ──────────────────────────────────────────────────────────
  const snoozeRow = p.snoozed_until_version
    ? `<div class="detail-row"><span class="detail-key">Snoozed until</span><span class="detail-value">${esc(p.snoozed_until_version)}</span></div>`
    : '';

  const cveSection = p.has_cves ? buildCVESection(p.cves) : '';

  const ecosystemSection = (p.ecosystem || p.package_name) ? `
    <div class="detail-section">
      <h4>CVE tracking</h4>
      ${p.ecosystem    ? `<div class="detail-row"><span class="detail-key">Ecosystem</span><span class="detail-value">${esc(p.ecosystem)}</span></div>` : ''}
      ${p.package_name ? `<div class="detail-row"><span class="detail-key">Package</span><span class="detail-value">${esc(p.package_name)}</span></div>` : ''}
    </div>` : '';

  drawerBody.innerHTML = `
    <div class="detail-section">
      <h4>Versions</h4>
      <div class="detail-row"><span class="detail-key">Current</span><span class="detail-value">${esc(p.current_version || '—')}</span></div>
      <div class="detail-row"><span class="detail-key">Latest</span><span class="detail-value">${esc(p.latest_version || '—')}</span></div>
      <div class="detail-row"><span class="detail-key">Status</span><span>${cardStatusBadge(p)}</span></div>
      ${snoozeRow}
    </div>
    <div class="detail-section">
      <h4>Project</h4>
      <div class="detail-row"><span class="detail-key">Platform</span><span class="detail-value">${esc(p.platform)}</span></div>
      <div class="detail-row"><span class="detail-key">Repository</span><span class="detail-value"><a href="${esc(p.url)}" target="_blank" rel="noopener">${esc(p.owner)}/${esc(p.repo)}</a></span></div>
      <div class="detail-row"><span class="detail-key">Last checked</span><span class="detail-value">${p.last_checked ? new Date(p.last_checked).toLocaleString() : '—'}</span></div>
    </div>
    ${p.update_summary ? `
    <div class="detail-section">
      <h4>Update summary (${esc(p.current_version)} → ${esc(p.latest_version)})</h4>
      <div class="detail-text">${esc(p.update_summary)}</div>
    </div>` : ''}
    ${cveSection}
    ${ecosystemSection}
  `;

  // ── footer action buttons ────────────────────────────────────────────────
  // Update actions: only when update is available and not snoozed
  drawerUpdateActions.classList.toggle('hidden', !p.update_available);
  if (p.update_available) {
    btnConfirmUpdate.textContent = `✓ Mark as updated to ${p.latest_version}`;
  }

  // Snooze actions: only when currently snoozed
  drawerSnoozeActions.classList.toggle('hidden', !p.snoozed_until_version);

  drawerOverlay.classList.remove('hidden');
}

function buildCVESection(cves) {
  if (!cves || cves.length === 0) return '';
  const items = cves.map(c => {
    const cls = c.severity === 'CRITICAL' || c.severity === 'HIGH' ? 'badge-cve-crit' : 'badge-cve';
    const link = c.url ? `<a href="${esc(c.url)}" target="_blank" rel="noopener">${esc(c.id)}</a>` : esc(c.id);
    return `
      <div class="cve-item">
        <div class="cve-item-header">
          <span class="cve-id">${link}</span>
          <span class="badge ${cls}">${esc(c.severity || 'UNKNOWN')}</span>
        </div>
        ${c.summary ? `<div class="cve-summary">${esc(c.summary)}</div>` : ''}
      </div>`;
  }).join('');

  return `
    <div class="detail-section">
      <h4>Known vulnerabilities in ${cves.length > 0 ? 'current version' : ''}</h4>
      <div class="cve-list">${items}</div>
    </div>`;
}

function closeDrawer() {
  drawerOverlay.classList.add('hidden');
  selectedProject = null;
}

// ── Drawer actions ────────────────────────────────────────────────────────────
btnConfirmUpdate.addEventListener('click', async () => {
  if (!selectedProject) return;
  ensureAdminKey();
  try {
    const updated = await apiFetch(`/projects/${selectedProject.id}/confirm-update`, { method: 'POST' });
    closeDrawer();
    // Update local state and re-render without a full reload
    const idx = projects.findIndex(p => p.id === updated.id);
    if (idx !== -1) projects[idx] = updated;
    renderAll();
  } catch (err) {
    alert('Failed: ' + err.message);
  }
});

btnSnooze.addEventListener('click', async () => {
  if (!selectedProject) return;
  ensureAdminKey();
  try {
    const updated = await apiFetch(`/projects/${selectedProject.id}/snooze`, { method: 'POST' });
    closeDrawer();
    const idx = projects.findIndex(p => p.id === updated.id);
    if (idx !== -1) projects[idx] = updated;
    renderAll();
  } catch (err) {
    alert('Failed: ' + err.message);
  }
});

btnUnsnooze.addEventListener('click', async () => {
  if (!selectedProject) return;
  ensureAdminKey();
  try {
    const updated = await apiFetch(`/projects/${selectedProject.id}/snooze`, { method: 'DELETE' });
    closeDrawer();
    const idx = projects.findIndex(p => p.id === updated.id);
    if (idx !== -1) projects[idx] = updated;
    renderAll();
  } catch (err) {
    alert('Failed: ' + err.message);
  }
});

btnDrawerDel.addEventListener('click', async () => {
  if (!selectedProject) return;
  if (!confirm(`Remove "${selectedProject.name}" from tracking?`)) return;
  ensureAdminKey();
  try {
    await apiFetch('/projects/' + selectedProject.id, { method: 'DELETE' });
    closeDrawer();
    projects = projects.filter(p => p.id !== selectedProject?.id);
    renderAll();
  } catch (err) {
    alert('Failed to delete: ' + err.message);
  }
});

// ── Add Project modal ─────────────────────────────────────────────────────────
function openModal() {
  formAddProject.reset();
  advancedFields.classList.add('hidden');
  formError.classList.add('hidden');
  modalOverlay.classList.remove('hidden');
  fURL.focus();
}

function closeModal() {
  modalOverlay.classList.add('hidden');
}

btnAdvToggle.addEventListener('click', () => {
  const hidden = advancedFields.classList.toggle('hidden');
  btnAdvToggle.textContent = hidden
    ? '+ CVE vulnerability scanning (optional)'
    : '− CVE vulnerability scanning (optional)';
});

formAddProject.addEventListener('submit', async e => {
  e.preventDefault();
  formError.classList.add('hidden');

  const data = {
    url:             formAddProject.url.value.trim(),
    current_version: formAddProject.current_version.value.trim(),
    name:            formAddProject.name.value.trim() || undefined,
    ecosystem:       document.getElementById('f-ecosystem').value.trim() || undefined,
    package_name:    document.getElementById('f-package').value.trim() || undefined,
  };

  const notifType = document.getElementById('f-notif-type').value;
  const notifAddr = document.getElementById('f-notif-address').value.trim();
  if (notifType && notifAddr) {
    data.notifications = [{ type: notifType, address: notifAddr }];
  }

  const btn = document.getElementById('btn-modal-submit');
  btn.disabled = true;
  btn.textContent = 'Adding…';
  ensureAdminKey();

  try {
    const newProject = await apiFetch('/projects', {
      method: 'POST',
      body: JSON.stringify(data),
    });
    closeModal();
    projects = [...projects, newProject];
    renderAll();
  } catch (err) {
    formError.textContent = err.message;
    formError.classList.remove('hidden');
  } finally {
    btn.disabled = false;
    btn.textContent = 'Add & track';
  }
});

// ── Event wiring ──────────────────────────────────────────────────────────────
btnAdd.addEventListener('click', openModal);
btnModalClose.addEventListener('click', closeModal);
btnModalCancel.addEventListener('click', closeModal);
btnDrawerClose.addEventListener('click', closeDrawer);
drawerOverlay.addEventListener('click', e => { if (e.target === drawerOverlay) closeDrawer(); });
modalOverlay.addEventListener('click', e => { if (e.target === modalOverlay) closeModal(); });
document.getElementById('btn-refresh').addEventListener('click', loadProjects);

// ── Helpers ───────────────────────────────────────────────────────────────────
function esc(s) {
  if (!s) return '';
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function relativeTime(isoString) {
  const diff = Date.now() - new Date(isoString).getTime();
  const mins = Math.floor(diff / 60_000);
  if (mins < 2)  return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24)  return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

function showLoading(visible) {
  loading.classList.toggle('hidden', !visible);
  if (visible) grid.innerHTML = '';
}

// ── Init ──────────────────────────────────────────────────────────────────────
loadProjects();
setInterval(loadProjects, POLL_INTERVAL_MS);

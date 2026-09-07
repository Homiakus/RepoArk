package observability

const consoleHTML = `<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="dark">
<title>RepoArk Console</title>
<style>
:root {
  --bg: #070a0f;
  --bg-sub: #0b0f17;
  --panel: #0e141f;
  --panel-card: #131b29;
  --panel-active: #192336;
  --line: #1c2738;
  --line-strong: #28374f;
  --text: #f0f4f8;
  --text-dim: #bac6d5;
  --muted: #8292a5;
  --accent: #38bdf8;
  --accent-glow: rgba(56, 189, 248, 0.15);
  --indigo: #6366f1;
  --ok: #10b981;
  --ok-glow: rgba(16, 185, 129, 0.18);
  --warn: #f59e0b;
  --warn-glow: rgba(245, 158, 11, 0.18);
  --bad: #ef4444;
  --bad-glow: rgba(239, 68, 68, 0.18);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Inter", ui-sans-serif, sans-serif;
  color-scheme: dark;
  background: var(--bg);
  color: var(--text);
}
* { box-sizing: border-box; }
body {
  margin: 0;
  background: radial-gradient(circle at 10% -20%, #151e33 0%, transparent 40%),
              radial-gradient(circle at 90% 120%, #0d1726 0%, transparent 50%),
              var(--bg);
  min-height: 100vh;
  font-size: 13px;
  line-height: 1.5;
}
.shell {
  max-width: 1540px;
  margin: 0 auto;
  padding: 16px 22px;
}

/* Header */
.top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--line);
  margin-bottom: 16px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 14px;
}
.mark {
  width: 40px;
  height: 40px;
  border-radius: 10px;
  background: linear-gradient(135deg, var(--accent) 0%, var(--indigo) 100%);
  box-shadow: 0 0 24px var(--accent-glow);
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 900;
  font-size: 18px;
  color: #fff;
  letter-spacing: -0.05em;
}
.brand-info .title {
  font-size: 19px;
  font-weight: 800;
  letter-spacing: -0.02em;
  display: flex;
  align-items: center;
  gap: 8px;
}
.brand-info .version-pill {
  font-size: 10px;
  font-weight: 700;
  padding: 2px 6px;
  border-radius: 4px;
  background: rgba(56, 189, 248, 0.12);
  color: var(--accent);
  border: 1px solid rgba(56, 189, 248, 0.28);
}
.brand-info .subtitle {
  color: var(--muted);
  font-size: 12px;
  margin-top: 1px;
}
.header-actions {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 8px;
}
.nav {
  display: flex;
  gap: 7px;
  align-items: center;
  flex-wrap: wrap;
}
.nav a, .btn-ghost {
  color: var(--text-dim);
  text-decoration: none;
  border: 1px solid var(--line);
  background: var(--panel);
  border-radius: 8px;
  padding: 6px 12px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.nav a:hover, .btn-ghost:hover {
  border-color: var(--line-strong);
  color: var(--text);
  background: var(--panel-card);
}
.lang-switch {
  display: inline-flex;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--panel);
  padding: 2px;
}
.lang-btn {
  background: transparent;
  border: none;
  color: var(--muted);
  font-size: 11px;
  font-weight: 700;
  padding: 4px 9px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.15s ease;
}
.lang-btn.active {
  background: var(--panel-active);
  color: var(--accent);
  box-shadow: 0 1px 4px rgba(0,0,0,0.3);
}
.session-info {
  font-size: 11px;
  color: var(--muted);
  display: flex;
  align-items: center;
  gap: 6px;
}
.session-info a {
  color: var(--accent);
  text-decoration: none;
}
.session-info a:hover {
  text-decoration: underline;
}
.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 7px;
  border-radius: 99px;
  font-size: 10px;
  font-weight: 700;
  background: var(--panel-card);
  border: 1px solid var(--line);
}
.status-pill.ok { color: var(--ok); border-color: rgba(16, 185, 129, 0.3); }
.status-pill.warn { color: var(--warn); border-color: rgba(245, 158, 11, 0.3); }
.status-pill.bad { color: var(--bad); border-color: rgba(239, 68, 68, 0.3); }
.pulse-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  box-shadow: 0 0 6px currentColor;
}

/* Tabs Bar */
.tabs-bar {
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--line);
  margin-bottom: 18px;
  overflow-x: auto;
  padding-bottom: 2px;
}
.tab-btn {
  background: transparent;
  border: none;
  border-bottom: 2px solid transparent;
  color: var(--muted);
  padding: 8px 14px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 7px;
  white-space: nowrap;
  transition: all 0.15s ease;
  border-radius: 6px 6px 0 0;
}
.tab-btn:hover {
  color: var(--text);
  background: rgba(255, 255, 255, 0.03);
}
.tab-btn.active {
  color: var(--accent);
  border-bottom-color: var(--accent);
  background: rgba(56, 189, 248, 0.05);
}
.tab-badge {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 999px;
  background: var(--panel-card);
  color: var(--muted);
  border: 1px solid var(--line);
}
.tab-btn.active .tab-badge {
  background: rgba(56, 189, 248, 0.15);
  color: var(--accent);
  border-color: rgba(56, 189, 248, 0.3);
}

/* Main Layout */
.tab-content { display: none; }
.tab-content.active { display: block; }

.grid-overview {
  display: grid;
  grid-template-columns: minmax(0, 1.45fr) minmax(340px, 0.75fr);
  gap: 16px;
}
.stack { display: grid; gap: 16px; }

/* Panels & Cards */
.panel {
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: 12px;
  padding: 16px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.25);
}
.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.panel-head h2 {
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-dim);
  margin: 0;
  display: flex;
  align-items: center;
  gap: 8px;
}

/* KPI Cards */
.kpis {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
}
.kpi {
  background: var(--panel-card);
  border: 1px solid var(--line);
  border-radius: 10px;
  padding: 12px 14px;
  min-height: 88px;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
}
.kpi .label {
  font-size: 11px;
  color: var(--muted);
  text-transform: uppercase;
  letter-spacing: 0.03em;
  font-weight: 600;
}
.kpi .value {
  font-size: 24px;
  font-weight: 800;
  letter-spacing: -0.02em;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  margin: 4px 0;
}
.kpi .note {
  font-size: 11px;
  color: var(--muted);
}
.ok { color: var(--ok); }
.warn { color: var(--warn); }
.bad { color: var(--bad); }

/* Operations Grid */
.actions {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
}
.action {
  display: flex;
  flex-direction: column;
  padding: 12px 14px;
  border-radius: 10px;
  border: 1px solid var(--line);
  background: var(--panel-card);
  transition: all 0.15s ease;
}
.action:hover {
  border-color: var(--line-strong);
  background: var(--panel-active);
}
.action .action-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 6px;
}
.action .name {
  font-weight: 700;
  font-size: 13px;
  color: var(--text);
}
.risk-tag {
  font-size: 9px;
  font-weight: 700;
  text-transform: uppercase;
  padding: 2px 5px;
  border-radius: 4px;
  letter-spacing: 0.04em;
}
.risk-tag.normal { background: rgba(56, 189, 248, 0.12); color: var(--accent); }
.risk-tag.elevated { background: rgba(245, 158, 11, 0.14); color: var(--warn); }
.risk-tag.danger { background: rgba(239, 68, 68, 0.14); color: var(--bad); }

.action .desc {
  color: var(--muted);
  font-size: 11px;
  line-height: 1.45;
  margin-bottom: 12px;
  flex: 1;
}
.action button {
  border: 1px solid var(--line-strong);
  background: #172235;
  color: var(--text);
  border-radius: 7px;
  padding: 7px 11px;
  font: inherit;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
  transition: all 0.15s ease;
  width: 100%;
}
.action button:hover:not(:disabled) {
  border-color: var(--accent);
  color: #fff;
  background: #1e2c45;
}
.action button:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}
.action[data-risk="danger"] {
  border-color: rgba(239, 68, 68, 0.25);
}
.action[data-risk="danger"] button {
  background: rgba(239, 68, 68, 0.15);
  border-color: rgba(239, 68, 68, 0.35);
  color: #fca5a5;
}
.action[data-risk="danger"] button:hover:not(:disabled) {
  background: rgba(239, 68, 68, 0.28);
  border-color: var(--bad);
}

/* Terminal / Log Panel */
.terminal-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 8px;
}
.badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  border-radius: 999px;
  border: 1px solid var(--line);
  padding: 3px 8px;
  color: var(--muted);
  background: var(--panel-card);
}
.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--muted);
}
.dot.ok { background: var(--ok); box-shadow: 0 0 6px var(--ok); }
.dot.warn { background: var(--warn); }
.dot.bad { background: var(--bad); box-shadow: 0 0 6px var(--bad); }

.jobstate {
  font-size: 16px;
  font-weight: 800;
}
.log {
  background: #04070c;
  border: 1px solid #161f2e;
  border-radius: 8px;
  height: 320px;
  overflow-y: auto;
  padding: 10px 12px;
  font: 12px/1.55 ui-monospace, "SFMono-Regular", "JetBrains Mono", Consolas, monospace;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  color: #c9d4e2;
}
.log.tall { height: 560px; }
.logline {
  display: grid;
  grid-template-columns: 78px minmax(0, 1fr);
  gap: 10px;
}
.logtime { color: #506177; user-select: none; }

.cancel {
  border: 1px solid rgba(239, 68, 68, 0.4);
  background: rgba(239, 68, 68, 0.16);
  color: #fca5a5;
  border-radius: 7px;
  padding: 6px 11px;
  font: inherit;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
  transition: all 0.15s ease;
}
.cancel:hover:not(:disabled) {
  background: rgba(239, 68, 68, 0.3);
}
.cancel:disabled { opacity: 0.35; cursor: not-allowed; }

/* System / Runtime Grid */
.system {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}
.sys {
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 9px 11px;
  background: var(--panel-card);
  font-size: 12px;
}
.sys strong {
  display: block;
  font-size: 12px;
  margin-bottom: 2px;
  color: var(--text-dim);
}
.policyitem {
  padding: 8px 0;
  border-top: 1px solid var(--line);
  font-size: 12px;
}

/* Tables */
.data-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}
.data-table th, .data-table td {
  text-align: left;
  padding: 8px 10px;
  border-bottom: 1px solid var(--line);
}
.data-table th {
  color: var(--muted);
  font-weight: 700;
  text-transform: uppercase;
  font-size: 11px;
  letter-spacing: 0.03em;
  background: var(--bg-sub);
}
.data-table tr:hover td {
  background: rgba(255, 255, 255, 0.02);
}
.fleettable {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}
.fleettable th, .fleettable td {
  text-align: left;
  padding: 8px 8px;
  border-bottom: 1px solid var(--line);
}
.fleettable th {
  color: var(--muted);
  font-weight: 700;
  font-size: 11px;
}

/* Inputs & Filter Toolbar */
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}
.search-box {
  position: relative;
  flex: 1;
  max-width: 380px;
}
.search-input {
  width: 100%;
  background: var(--panel-card);
  border: 1px solid var(--line);
  color: var(--text);
  border-radius: 7px;
  padding: 7px 10px;
  font-size: 12px;
  outline: none;
  transition: border-color 0.15s ease;
}
.search-input:focus {
  border-color: var(--accent);
}
.filter-pills {
  display: flex;
  gap: 5px;
}
.filter-pill {
  border: 1px solid var(--line);
  background: var(--panel-card);
  color: var(--muted);
  padding: 5px 10px;
  border-radius: 6px;
  font-size: 11px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
}
.filter-pill.active {
  background: var(--panel-active);
  color: var(--accent);
  border-color: rgba(56, 189, 248, 0.35);
}

/* Banner & Dialogs */
.banner {
  display: none;
  margin-bottom: 14px;
  padding: 10px 14px;
  border: 1px solid rgba(239, 68, 68, 0.4);
  background: rgba(239, 68, 68, 0.12);
  border-radius: 8px;
  color: #fca5a5;
  font-size: 12px;
}
.modal-backdrop {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.75);
  backdrop-filter: blur(4px);
  display: none;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}
.modal-backdrop.open {
  display: flex;
}
.modal {
  background: var(--panel);
  border: 1px solid var(--line-strong);
  border-radius: 12px;
  width: 90%;
  max-width: 480px;
  padding: 20px;
  box-shadow: 0 12px 40px rgba(0,0,0,0.5);
}
.modal h3 {
  margin: 0 0 8px;
  font-size: 15px;
}
.modal p {
  color: var(--muted);
  font-size: 13px;
  line-height: 1.5;
  margin: 0 0 16px;
}
.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

.mono { font-family: ui-monospace, "SFMono-Regular", Consolas, monospace; }
.empty { color: var(--muted); font-size: 12px; padding: 14px 0; text-align: center; }
.footer {
  color: #556578;
  font-size: 11px;
  margin: 18px 2px 8px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

@media(max-width: 1100px) {
  .grid-overview { grid-template-columns: 1fr; }
  .kpis { grid-template-columns: repeat(2, 1fr); }
  .actions { grid-template-columns: repeat(2, 1fr); }
}
@media(max-width: 680px) {
  .shell { padding: 12px; }
  .top { flex-direction: column; align-items: flex-start; }
  .header-actions { align-items: flex-start; width: 100%; }
  .kpis, .actions { grid-template-columns: 1fr; }
  .system { grid-template-columns: 1fr; }
  .log { height: 260px; }
}
</style>
</head>
<body>
<div class="shell">
  <!-- Top Bar -->
  <div class="top">
    <div class="brand">
      <div class="mark">RA</div>
      <div class="brand-info">
        <div class="title">
          <span id="titleText">RepoArk Console</span>
          <span class="version-pill">v0.8.0</span>
          <div id="sseBadge" class="status-pill ok"><span class="pulse-dot"></span> <span id="sseStatusText">SSE</span></div>
        </div>
        <div id="subtitleText" class="subtitle">Backup operations · disaster recovery · integrity</div>
      </div>
    </div>
    <div class="header-actions">
      <div class="nav">
        <div class="lang-switch">
          <button id="langRu" class="lang-btn active" onclick="setLanguage('ru')">RU</button>
          <button id="langEn" class="lang-btn" onclick="setLanguage('en')">EN</button>
        </div>
        <a href="/healthz" target="_blank" id="navHealth">Health</a>
        <a href="/metrics" target="_blank" id="navMetrics">Metrics</a>
        <a href="/history" id="navHistory">History</a>
        <a id="recoveryLink" href="/restore" style="display:none">Recovery</a>
        <button class="btn-ghost" onclick="refreshAll()" id="btnRefresh">Refresh</button>
      </div>
      <div id="session" class="session-info"></div>
    </div>
  </div>

  <div id="banner" class="banner"></div>

  <!-- Navigation Tabs -->
  <div class="tabs-bar">
    <button class="tab-btn active" onclick="switchTab('overview')" id="tabBtnOverview">📊 <span id="lblTabOverview">Обзор</span></button>
    <button class="tab-btn" onclick="switchTab('repos')" id="tabBtnRepos">📦 <span id="lblTabRepos">Репозитории</span> <span id="badgeReposCount" class="tab-badge">0</span></button>
    <button class="tab-btn" onclick="switchTab('github')" id="tabBtnGithub">🐙 <span id="lblTabGithub">GitHub & Данные</span></button>
    <button class="tab-btn" onclick="switchTab('gitlab')" id="tabBtnGitlab">🦊 <span id="lblTabGitlab">GitLab DR</span></button>
    <button class="tab-btn" onclick="switchTab('cas')" id="tabBtnCas">🗄️ <span id="lblTabCas">CAS & Хранилище</span></button>
    <button class="tab-btn" onclick="switchTab('control')" id="tabBtnControl">⚡ <span id="lblTabControl">Контроль & Задачи</span></button>
    <button class="tab-btn" onclick="switchTab('policy')" id="tabBtnPolicy">🛡️ <span id="lblTabPolicy">Политики & Аудит</span></button>
    <button class="tab-btn" onclick="switchTab('terminal')" id="tabBtnTerminal">💻 <span id="lblTabTerminal">Live Консоль</span></button>
  </div>

  <!-- Tab 1: Overview -->
  <div id="tabOverview" class="tab-content active">
    <div class="grid-overview">
      <main class="stack">
        <section class="panel">
          <div class="panel-head"><h2 id="lblOverviewTitle">Сводка состояния</h2></div>
          <div id="kpis" class="kpis"></div>
        </section>

        <section class="panel">
          <div class="panel-head"><h2 id="lblOpsTitle">Операции управления</h2></div>
          <div id="actions" class="actions"></div>
        </section>

        <section class="panel">
          <div class="terminal-bar">
            <h2 id="lblActivityTitle" style="margin:0;font-size:13px;text-transform:uppercase;color:var(--text-dim);font-weight:700">Активность</h2>
            <div>
              <span id="jobBadge" class="badge"><span class="dot"></span> <span id="jobBadgeText">idle</span></span>
              <button id="cancel" class="cancel" onclick="cancelJob()" disabled>Cancel</button>
            </div>
          </div>
          <div id="jobTitle" class="jobstate" style="margin-top:6px">No operation running</div>
          <div id="jobMeta" class="muted" style="font-size:11px;margin-top:2px"></div>
          <div id="log" class="log"><div class="empty">Operation logs will appear here.</div></div>
        </section>

        <section class="panel">
          <div class="panel-head"><h2 id="lblFleetTitle">Аккаунты Fleet</h2></div>
          <div id="fleet"></div>
        </section>
      </main>

      <aside class="stack">
        <section class="panel">
          <div class="panel-head"><h2 id="lblRuntimeTitle">Окружение и инструменты</h2></div>
          <div id="system" class="system"></div>
          <div id="root" class="muted mono" style="font-size:11px;margin-top:10px;overflow-wrap:anywhere"></div>
        </section>

        <section class="panel">
          <div class="panel-head"><h2 id="lblPolicySidebarTitle">Политики RPO/RTO</h2></div>
          <div id="policy" class="policyline">Loading…</div>
        </section>

        <section class="panel">
          <div class="panel-head"><h2 id="lblControlSidebarTitle">Control plane</h2></div>
          <div id="control" class="system"></div>
        </section>
      </aside>
    </div>
  </div>

  <!-- Tab 2: Repositories & Backup -->
  <div id="tabRepos" class="tab-content">
    <section class="panel">
      <div class="panel-head">
        <h2 id="lblReposDetailTitle">Резервные копии репозиториев</h2>
        <div class="filter-pills">
          <button class="filter-pill active" onclick="setRepoFilter('all')" id="btnFilterAll">Все</button>
          <button class="filter-pill" onclick="setRepoFilter('ok')" id="btnFilterOk">Успешно</button>
          <button class="filter-pill" onclick="setRepoFilter('warn')" id="btnFilterWarn">Предупреждения</button>
          <button class="filter-pill" onclick="setRepoFilter('bad')" id="btnFilterFail">Ошибки</button>
        </div>
      </div>
      <div class="toolbar">
        <div class="search-box">
          <input id="repoSearch" class="search-input" type="text" placeholder="Поиск репозиториев..." oninput="filterRepos()">
        </div>
        <div style="display:flex;gap:8px">
          <button class="btn-ghost" onclick="startJob('backup','normal')">⚡ <span id="lblRunBackupNow">Сделать бэкап сейчас</span></button>
          <button class="btn-ghost" onclick="startJob('verify','normal')">🔍 <span id="lblRunVerifyNow">Верифицировать</span></button>
          <button class="btn-ghost" onclick="startJob('repo-drill','elevated')">🛡️ <span id="lblRunDrillNow">DR Drill</span></button>
        </div>
      </div>
      <div style="overflow-x:auto">
        <table class="data-table">
          <thead>
            <tr>
              <th id="thRepoName">Репозиторий</th>
              <th id="thRepoStatus">Статус</th>
              <th id="thRepoMirrors">Зеркало</th>
              <th id="thRepoBundle">Bundle</th>
              <th id="thRepoLfs">LFS</th>
              <th id="thRepoPlatforms">Платформа</th>
              <th id="thRepoUpdated">Обновлено</th>
              <th id="thRepoAction">Действия</th>
            </tr>
          </thead>
          <tbody id="reposTableBody">
            <tr><td colspan="8" class="empty">Загрузка данных манифеста…</td></tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>

  <!-- Tab 3: GitHub & Platform Data -->
  <div id="tabGithub" class="tab-content">
    <div class="grid-overview">
      <main class="stack">
        <section class="panel">
          <div class="panel-head"><h2 id="lblGhPlanesTitle">Плоскости данных GitHub (GitHub Data Planes)</h2></div>
          <div class="system" id="ghPlanesGrid"></div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2 id="lblGhExportTitle">Официальный экспорт миграции GitHub</h2></div>
          <p id="lblGhExportDesc" style="color:var(--muted);font-size:12px">Официальный миграционный архив включает структуру проектов, метаданные, вехи, issue и pull request с комментариями в каноническом формате GitHub Enterprise Migration API.</p>
          <div style="display:flex;gap:10px;margin-top:12px">
            <button class="btn-ghost" onclick="alert(t('ghExportHint'))">📥 <span id="lblRunExportUser">Экспорт текущего аккаунта (CLI)</span></button>
          </div>
        </section>
      </main>
      <aside class="stack">
        <section class="panel">
          <div class="panel-head"><h2 id="lblGhAuthTitle">GitHub API & Токен</h2></div>
          <div id="ghAuthDetails" class="system"></div>
        </section>
      </aside>
    </div>
  </div>

  <!-- Tab 4: GitLab DR -->
  <div id="tabGitlab" class="tab-content">
    <div class="grid-overview">
      <main class="stack">
        <section class="panel">
          <div class="panel-head"><h2 id="lblGitlabDrTitle">Пайплайн аварийного восстановления GitLab</h2></div>
          <p id="lblGitlabDrDesc" style="color:var(--muted);font-size:12px">RepoArk обеспечивает полную автономную готовность развертывания и переноса репозиториев на выделенный standby-сервер GitLab CE с сохранением пространств имен.</p>
          <div class="actions" style="margin-top:14px">
            <div class="action" data-risk="danger">
              <div class="action-header"><span class="name">1. Deploy GitLab</span><span class="risk-tag danger">danger</span></div>
              <div class="desc" id="descDeploy">Развертывание инстанса GitLab CE через Docker Compose на целевом хосте.</div>
              <button onclick="startJob('gitlab-deploy','danger')">Deploy</button>
            </div>
            <div class="action" data-risk="danger">
              <div class="action-header"><span class="name">2. Migrate to GitLab</span><span class="risk-tag danger">danger</span></div>
              <div class="desc" id="descMigrate">Зеркалирование всех репозиториев в GitLab с сохранением групп и владельцев.</div>
              <button onclick="startJob('gitlab-migrate','danger')">Migrate</button>
            </div>
            <div class="action" data-risk="elevated">
              <div class="action-header"><span class="name">3. GitLab Backup</span><span class="risk-tag elevated">elevated</span></div>
              <div class="desc" id="descBackup">Создание полного бэкапа состояния приложения GitLab.</div>
              <button onclick="startJob('gitlab-backup','elevated')">Backup</button>
            </div>
            <div class="action" data-risk="elevated">
              <div class="action-header"><span class="name">4. Restore Drill</span><span class="risk-tag elevated">elevated</span></div>
              <div class="desc" id="descDrill">Одноразовый проверочный цикл восстановления из бэкапа в изолированном контейнере.</div>
              <button onclick="startJob('gitlab-drill','elevated')">Run Drill</button>
            </div>
          </div>
        </section>
      </main>
      <aside class="stack">
        <section class="panel">
          <div class="panel-head"><h2 id="lblGitlabHostTitle">Целевой хост GitLab</h2></div>
          <div id="gitlabHostDetails" class="system"></div>
        </section>
      </aside>
    </div>
  </div>

  <!-- Tab 5: CAS Storage & Resiliency -->
  <div id="tabCas" class="tab-content">
    <div class="grid-overview">
      <main class="stack">
        <section class="panel">
          <div class="panel-head"><h2 id="lblCasStatsTitle">CAS Дедупликация & Статистика</h2></div>
          <div class="kpis" id="casKpis"></div>
          <div style="display:flex;gap:10px;margin-top:16px">
            <button class="btn-ghost" onclick="startJob('cas-compact','normal')">⚡ <span id="lblRunCasCompact">Выполнить сжатие CAS (Compact)</span></button>
          </div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2 id="lblErasureTitle">Erasure Coding & Размещение шардов</h2></div>
          <p id="lblErasureDesc" style="color:var(--muted);font-size:12px">Хранилище использует распределенное Reed-Solomon шардирование с отказоустойчивостью к потере любого одного домена сбоев (failure domain).</p>
          <div id="erasureGrid" class="system" style="margin-top:12px"></div>
        </section>
      </main>
      <aside class="stack">
        <section class="panel">
          <div class="panel-head"><h2 id="lblMediaHealthTitle">Телеметрия дисков (SMART/NVMe)</h2></div>
          <div id="smartDetails" class="system"></div>
        </section>
      </aside>
    </div>
  </div>

  <!-- Tab 6: Control Plane & HA -->
  <div id="tabControl" class="tab-content">
    <div class="grid-overview">
      <main class="stack">
        <section class="panel">
          <div class="panel-head"><h2 id="lblControlJobsTitle">Очередь задач планировщика</h2></div>
          <div id="controlStatsGrid" class="kpis"></div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2 id="lblApprovalsTitle">Шлюз согласования восстановления (Approvals)</h2></div>
          <p id="lblApprovalsDesc" style="color:var(--muted);font-size:12px">Для предотвращения несанкционированного изменения продуктивных данных восстановление требует подтверждения по правилу двух лиц.</p>
          <div id="approvalsTableContainer" style="overflow-x:auto;margin-top:10px"></div>
        </section>
      </main>
      <aside class="stack">
        <section class="panel">
          <div class="panel-head"><h2 id="lblHaReplicaTitle">HA Репликация поколений</h2></div>
          <div id="haDetails" class="system"></div>
        </section>
      </aside>
    </div>
  </div>

  <!-- Tab 7: Policy & Audit -->
  <div id="tabPolicy" class="tab-content">
    <div class="grid-overview">
      <main class="stack">
        <section class="panel">
          <div class="panel-head"><h2 id="lblPolicyEvalTitle">Оценка соответствия политикам RPO / RTO</h2></div>
          <div id="policyFull" class="policyline"></div>
          <div style="margin-top:14px">
            <button class="btn-ghost" onclick="startJob('policy','normal')">🛡️ <span id="lblRunPolicyEval">Пересчитать политики сейчас</span></button>
          </div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2 id="lblAuditTitle">Защищенный журнал аудита (Tamper-evident Audit)</h2></div>
          <p id="lblAuditDesc" style="color:var(--muted);font-size:12px">Все операции фиксируются в криптографически связанной цепочке хэшей SHA-256 с блокировками на уровне файловой системы (POSIX/Windows) и Ed25519 подписью чекпоинтов.</p>
          <div style="margin-top:12px">
            <a href="/history" class="btn-ghost">📜 <span id="lblViewAuditHistory">Открыть журнал истории аудита</span></a>
          </div>
        </section>
      </main>
      <aside class="stack">
        <section class="panel">
          <div class="panel-head"><h2 id="lblSecurityKeysTitle">Ключи подписи манифестов</h2></div>
          <div id="keysDetails" class="system"></div>
        </section>
      </aside>
    </div>
  </div>

  <!-- Tab 8: Live Terminal Logs -->
  <div id="tabTerminal" class="tab-content">
    <section class="panel">
      <div class="terminal-bar">
        <div>
          <span class="jobstate" id="termJobTitle">No operation running</span>
          <span id="termJobBadge" class="badge" style="margin-left:10px"><span class="dot"></span> <span id="termJobBadgeText">idle</span></span>
        </div>
        <div style="display:flex;gap:8px;align-items:center">
          <button id="termCancel" class="cancel" onclick="cancelJob()" disabled>Cancel</button>
        </div>
      </div>
      <div class="toolbar" style="margin-top:8px">
        <div class="search-box">
          <input id="logSearch" class="search-input" type="text" placeholder="Фильтр вывода..." oninput="filterLogView()">
        </div>
        <div style="display:flex;gap:6px">
          <button class="btn-ghost" onclick="copyLogs()" id="btnCopyLog">Копировать лог</button>
          <button class="btn-ghost" onclick="clearLogs()" id="btnClearLog">Очистить</button>
        </div>
      </div>
      <div id="termLog" class="log tall"><div class="empty">Operation logs will appear here.</div></div>
    </section>
  </div>

  <!-- Confirmation Modal -->
  <div id="confirmModal" class="modal-backdrop">
    <div class="modal">
      <h3 id="modalTitle">Подтверждение операции</h3>
      <p id="modalMessage">Вы уверены?</p>
      <div class="modal-actions">
        <button class="btn-ghost" onclick="closeConfirm(false)" id="modalBtnCancel">Отмена</button>
        <button class="cancel" onclick="closeConfirm(true)" id="modalBtnConfirm" style="background:#ef4444;color:#fff;border-color:#ef4444">Подтвердить</button>
      </div>
    </div>
  </div>

  <div class="footer">
    <span id="footerLocalNote">Web console is local-only unless OIDC is configured. CLI commands remain available for automation and break-glass recovery.</span>
    <span>RepoArk Engine v0.8.0 · Go Disaster Recovery</span>
  </div>
</div>

<script>
let consoleState=null, sessionState=null, currentJob=null, jobEvents=null, jobFallbackTimer=null;
let manifestData=null, currentRepoFilter='all', rawLogs=[];
let currentLang = localStorage.getItem('repoark_lang') || 'ru';
let modalResolve = null;

const i18n = {
  ru: {
    title: "RepoArk Консоль",
    subtitle: "Резервное копирование · Disaster Recovery · Целостность",
    health: "Здоровье",
    metrics: "Метрики",
    history: "История операций",
    recovery: "Восстановление",
    refresh: "Обновить",
    localSession: "Локальная консоль · Local console (loopback)",
    remoteSignIn: "Удаленный доступ · Вход",
    overall: "Общий статус",
    healthy: "ЗДОРОВ",
    degraded: "ДЕГРАДИРОВАН",
    manifestPolicy: "манифест + политики",
    repos: "Репозитории",
    failed: "с ошибкой",
    lastBackup: "Последний бэкап",
    warnings: "предупреждений",
    casReclaimed: "Очищено CAS",
    contentStore: "дедупликация хранилища",
    allPolicyPass: "Все контроли политик пройдены.",
    policyUnavailable: "Состояние политик недоступно.",
    fleetDisabled: "Режим Fleet отключен.",
    controlDisabled: "Control plane отключен.",
    idle: "ожидание",
    noJobRunning: "Нет активных операций",
    started: "Запущено",
    ago: "назад",
    cancel: "Отменить",
    cancelConfirm: "Отменить текущую операцию?",
    run: "Запустить",
    disabled: "Отключено",
    waitingOutput: "Ожидание вывода…",
    logPlaceholder: "Логи операций будут отображаться здесь.",
    searchRepos: "Поиск репозиториев...",
    filterAll: "Все",
    filterOk: "Успешно",
    filterWarn: "Предупреждения",
    filterFail: "Ошибки",
    // Tabs
    tabOverview: "Обзор",
    tabRepos: "Репозитории",
    tabGithub: "GitHub & Данные",
    tabGitlab: "GitLab DR",
    tabCas: "CAS & Хранилище",
    tabControl: "Контроль & Задачи",
    tabPolicy: "Политики & Аудит",
    tabTerminal: "Live Консоль",
    // Section headers
    overviewTitle: "Сводка состояния",
    opsTitle: "Операции управления",
    activityTitle: "Активность",
    fleetTitle: "Аккаунты Fleet",
    runtimeTitle: "Окружение и инструменты",
    policySidebarTitle: "Политики RPO/RTO",
    controlSidebarTitle: "Control plane",
    reposDetailTitle: "Резервные копии репозиториев",
    ghPlanesTitle: "Плоскости данных GitHub (Data Planes)",
    ghExportTitle: "Официальный экспорт миграции GitHub",
    ghExportDesc: "Официальный миграционный архив включает структуру проектов, метаданные, вехи, issue и pull request с комментариями в каноническом формате GitHub Enterprise Migration API.",
    ghExportHint: "Для выполнения официального экспорта выполните в терминале:\nrepoark github export user (или repoark github export org ИМЯ_ОРГ)",
    ghAuthTitle: "GitHub API & Токен",
    gitlabDrTitle: "Пайплайн аварийного восстановления GitLab",
    gitlabDrDesc: "RepoArk обеспечивает полную автономную готовность развертывания и переноса репозиториев на выделенный standby-сервер GitLab CE с сохранением пространств имен.",
    gitlabHostTitle: "Целевой хост GitLab",
    casStatsTitle: "CAS Дедупликация & Статистика",
    erasureTitle: "Erasure Coding & Размещение шардов",
    erasureDesc: "Хранилище использует распределенное Reed-Solomon шардирование с отказоустойчивостью к потере любого одного домена сбоев (failure domain).",
    mediaHealthTitle: "Телеметрия дисков (SMART/NVMe)",
    controlJobsTitle: "Очередь задач планировщика",
    approvalsTitle: "Шлюз согласования восстановления (Approvals)",
    approvalsDesc: "Для предотвращения несанкционированного изменения продуктивных данных восстановление требует подтверждения по правилу двух лиц.",
    haReplicaTitle: "HA Репликация поколений",
    policyEvalTitle: "Оценка соответствия политикам RPO / RTO",
    auditTitle: "Защищенный журнал аудита (Tamper-evident Audit)",
    auditDesc: "Все операции фиксируются в криптографически связанной цепочке хэшей SHA-256 с блокировками на уровне файловой системы (POSIX/Windows) и Ed25519 подписью чекпоинтов.",
    securityKeysTitle: "Ключи подписи манифестов",
    // Table Headers
    thRepoName: "Репозиторий",
    thRepoStatus: "Статус",
    thRepoMirrors: "Зеркало",
    thRepoBundle: "Bundle",
    thRepoLfs: "LFS",
    thRepoPlatforms: "Платформа",
    thRepoUpdated: "Обновлено",
    thRepoAction: "Действия",
    // Modal & Buttons
    confirmTitle: "Подтверждение операции",
    confirmDanger: "Это привилегированная операция высокой опасности, которая изменит состояние GitLab. Продолжить?",
    confirmElevated: "Запустить операцию повышенного риска?",
    modalBtnConfirm: "Подтвердить",
    modalBtnCancel: "Отмена",
    footerLocalNote: "Веб-консоль работает локально (loopback). Доступны CLI-команды для автоматизации и аварийного восстановления."
  },
  en: {
    title: "RepoArk Console",
    subtitle: "Backup operations · disaster recovery · integrity",
    health: "Health",
    metrics: "Metrics",
    history: "History",
    recovery: "Recovery",
    refresh: "Refresh",
    localSession: "Local console · loopback protected",
    remoteSignIn: "Remote console · Sign in",
    overall: "Overall",
    healthy: "HEALTHY",
    degraded: "DEGRADED",
    manifestPolicy: "manifest + policy",
    repos: "Repositories",
    failed: "failed",
    lastBackup: "Last backup",
    warnings: "warnings",
    casReclaimed: "CAS reclaimed",
    contentStore: "content-addressed storage",
    allPolicyPass: "All policy gates pass.",
    policyUnavailable: "Policy state unavailable.",
    fleetDisabled: "Fleet mode is disabled.",
    controlDisabled: "Control plane disabled.",
    idle: "idle",
    noJobRunning: "No operation running",
    started: "Started",
    ago: "ago",
    cancel: "Cancel",
    cancelConfirm: "Cancel current operation?",
    run: "Run",
    disabled: "Disabled",
    waitingOutput: "Waiting for output…",
    logPlaceholder: "Operation logs will appear here.",
    searchRepos: "Search repositories...",
    filterAll: "All",
    filterOk: "Healthy",
    filterWarn: "Warnings",
    filterFail: "Errors",
    tabOverview: "Overview",
    tabRepos: "Repositories",
    tabGithub: "GitHub & Data",
    tabGitlab: "GitLab DR",
    tabCas: "CAS & Storage",
    tabControl: "Control & HA",
    tabPolicy: "Policy & Audit",
    tabTerminal: "Live Console",
    overviewTitle: "Health Overview",
    opsTitle: "Operations",
    activityTitle: "Activity",
    fleetTitle: "Fleet Accounts",
    runtimeTitle: "Runtime & Tools",
    policySidebarTitle: "RPO/RTO Policies",
    controlSidebarTitle: "Control Plane",
    reposDetailTitle: "Repository Backups",
    ghPlanesTitle: "GitHub Data Planes",
    ghExportTitle: "Official GitHub Migration Export",
    ghExportDesc: "The official migration export packages projects, issues, PRs, comments and milestones into a canonical GitHub Enterprise migration archive.",
    ghExportHint: "To run official export, execute in terminal:\nrepoark github export user (or repoark github export org ORG_NAME)",
    ghAuthTitle: "GitHub API & Auth",
    gitlabDrTitle: "GitLab DR Recovery Pipeline",
    gitlabDrDesc: "RepoArk maintains autonomous readiness to deploy and mirror repositories into a dedicated standby GitLab CE instance.",
    gitlabHostTitle: "GitLab Target Host",
    casStatsTitle: "CAS Deduplication & Stats",
    erasureTitle: "Erasure Coding & Shard Placement",
    erasureDesc: "Storage uses distributed Reed-Solomon shard placement resilient to loss of any one failure domain.",
    mediaHealthTitle: "Drive Telemetry (SMART/NVMe)",
    controlJobsTitle: "Scheduler Job Queue",
    approvalsTitle: "Restore Approval Workflow",
    approvalsDesc: "To protect production data, point-in-time restore requests require two-person verification.",
    haReplicaTitle: "HA Generation Replicas",
    policyEvalTitle: "RPO / RTO Policy Compliance",
    auditTitle: "Tamper-evident Audit Ledger",
    auditDesc: "All operations are recorded in an append-only SHA-256 hash chain with OS file locks and signed Ed25519 checkpoints.",
    securityKeysTitle: "Manifest Signing Keys",
    thRepoName: "Repository",
    thRepoStatus: "Status",
    thRepoMirrors: "Mirror",
    thRepoBundle: "Bundle",
    thRepoLfs: "LFS",
    thRepoPlatforms: "Platform",
    thRepoUpdated: "Updated",
    thRepoAction: "Actions",
    confirmTitle: "Confirm Operation",
    confirmDanger: "This is a privileged operation that can change GitLab state. Continue?",
    confirmElevated: "Run this elevated risk operation now?",
    modalBtnConfirm: "Confirm",
    modalBtnCancel: "Cancel",
    footerLocalNote: "Web console is local-only unless OIDC is configured. CLI commands remain available for automation and break-glass recovery."
  }
};

function t(k) {
  const dict = i18n[currentLang] || i18n.ru;
  return dict[k] || i18n.en[k] || k;
}

function setLanguage(lang) {
  currentLang = (lang === 'en') ? 'en' : 'ru';
  localStorage.setItem('repoark_lang', currentLang);
  document.documentElement.lang = currentLang;
  document.querySelector('#langRu').classList.toggle('active', currentLang === 'ru');
  document.querySelector('#langEn').classList.toggle('active', currentLang === 'en');
  applyStaticTranslations();
  renderSession();
  renderActions();
  renderSystem();
  renderJob();
  loadOverview();
  renderGitHubPlanes();
  renderCasTab();
  renderControlTab();
}

function applyStaticTranslations() {
  document.querySelector('#titleText').textContent = t('title');
  document.querySelector('#subtitleText').textContent = t('subtitle');
  document.querySelector('#navHealth').textContent = t('health');
  document.querySelector('#navMetrics').textContent = t('metrics');
  document.querySelector('#navHistory').textContent = t('history');
  document.querySelector('#btnRefresh').textContent = t('refresh');
  
  document.querySelector('#lblTabOverview').textContent = t('tabOverview');
  document.querySelector('#lblTabRepos').textContent = t('tabRepos');
  document.querySelector('#lblTabGithub').textContent = t('tabGithub');
  document.querySelector('#lblTabGitlab').textContent = t('tabGitlab');
  document.querySelector('#lblTabCas').textContent = t('tabCas');
  document.querySelector('#lblTabControl').textContent = t('tabControl');
  document.querySelector('#lblTabPolicy').textContent = t('tabPolicy');
  document.querySelector('#lblTabTerminal').textContent = t('tabTerminal');
  
  document.querySelector('#lblOverviewTitle').textContent = t('overviewTitle');
  document.querySelector('#lblOpsTitle').textContent = t('opsTitle');
  document.querySelector('#lblActivityTitle').textContent = t('activityTitle');
  document.querySelector('#lblFleetTitle').textContent = t('fleetTitle');
  document.querySelector('#lblRuntimeTitle').textContent = t('runtimeTitle');
  document.querySelector('#lblPolicySidebarTitle').textContent = t('policySidebarTitle');
  document.querySelector('#lblControlSidebarTitle').textContent = t('controlSidebarTitle');
  document.querySelector('#lblReposDetailTitle').textContent = t('reposDetailTitle');
  document.querySelector('#lblGhPlanesTitle').textContent = t('ghPlanesTitle');
  document.querySelector('#lblGhExportTitle').textContent = t('ghExportTitle');
  document.querySelector('#lblGhExportDesc').textContent = t('ghExportDesc');
  document.querySelector('#lblGhAuthTitle').textContent = t('ghAuthTitle');
  document.querySelector('#lblGitlabDrTitle').textContent = t('gitlabDrTitle');
  document.querySelector('#lblGitlabDrDesc').textContent = t('gitlabDrDesc');
  document.querySelector('#lblGitlabHostTitle').textContent = t('gitlabHostTitle');
  document.querySelector('#lblCasStatsTitle').textContent = t('casStatsTitle');
  document.querySelector('#lblErasureTitle').textContent = t('erasureTitle');
  document.querySelector('#lblErasureDesc').textContent = t('erasureDesc');
  document.querySelector('#lblMediaHealthTitle').textContent = t('mediaHealthTitle');
  document.querySelector('#lblControlJobsTitle').textContent = t('controlJobsTitle');
  document.querySelector('#lblApprovalsTitle').textContent = t('approvalsTitle');
  document.querySelector('#lblApprovalsDesc').textContent = t('approvalsDesc');
  document.querySelector('#lblHaReplicaTitle').textContent = t('haReplicaTitle');
  document.querySelector('#lblPolicyEvalTitle').textContent = t('policyEvalTitle');
  document.querySelector('#lblAuditTitle').textContent = t('auditTitle');
  document.querySelector('#lblAuditDesc').textContent = t('auditDesc');
  document.querySelector('#lblSecurityKeysTitle').textContent = t('securityKeysTitle');

  document.querySelector('#btnFilterAll').textContent = t('filterAll');
  document.querySelector('#btnFilterOk').textContent = t('filterOk');
  document.querySelector('#btnFilterWarn').textContent = t('filterWarn');
  document.querySelector('#btnFilterFail').textContent = t('filterFail');
  document.querySelector('#repoSearch').placeholder = t('searchRepos');
  
  document.querySelector('#thRepoName').textContent = t('thRepoName');
  document.querySelector('#thRepoStatus').textContent = t('thRepoStatus');
  document.querySelector('#thRepoMirrors').textContent = t('thRepoMirrors');
  document.querySelector('#thRepoBundle').textContent = t('thRepoBundle');
  document.querySelector('#thRepoLfs').textContent = t('thRepoLfs');
  document.querySelector('#thRepoPlatforms').textContent = t('thRepoPlatforms');
  document.querySelector('#thRepoUpdated').textContent = t('thRepoUpdated');
  document.querySelector('#thRepoAction').textContent = t('thRepoAction');

  document.querySelector('#footerLocalNote').textContent = t('footerLocalNote');
  document.querySelector('#modalBtnConfirm').textContent = t('modalBtnConfirm');
  document.querySelector('#modalBtnCancel').textContent = t('modalBtnCancel');
}

function switchTab(tabId) {
  document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
  document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
  
  const map = {
    overview: '#tabOverview',
    repos: '#tabRepos',
    github: '#tabGithub',
    gitlab: '#tabGitlab',
    cas: '#tabCas',
    control: '#tabControl',
    policy: '#tabPolicy',
    terminal: '#tabTerminal'
  };
  const btnMap = {
    overview: '#tabBtnOverview',
    repos: '#tabBtnRepos',
    github: '#tabBtnGithub',
    gitlab: '#tabBtnGitlab',
    cas: '#tabBtnCas',
    control: '#tabBtnControl',
    policy: '#tabBtnPolicy',
    terminal: '#tabBtnTerminal'
  };
  
  const target = document.querySelector(map[tabId] || '#tabOverview');
  const btn = document.querySelector(btnMap[tabId] || '#tabBtnOverview');
  if (target) target.classList.add('active');
  if (btn) btn.classList.add('active');
  
  if (tabId === 'terminal') {
    const el = document.querySelector('#termLog');
    if (el) el.scrollTop = el.scrollHeight;
  }
}

const esc = s => String(s??'').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const age = d => {
  if (!d) return '—';
  const n = Math.max(0, (Date.now() - new Date(d).getTime()) / 1000);
  if (n < 120) return Math.round(n) + (currentLang==='ru'?'с':'s');
  if (n < 7200) return Math.round(n / 60) + (currentLang==='ru'?'м':'m');
  if (n < 172800) return Math.round(n / 3600) + (currentLang==='ru'?'ч':'h');
  return Math.round(n / 86400) + (currentLang==='ru'?'д':'d');
};
const bytes = n => {
  n = Number(n || 0);
  const u = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (i ? n.toFixed(1) : Math.round(n)) + ' ' + u[i];
};

async function req(url, opt = {}) {
  const r = await fetch(url, { cache: 'no-store', ...opt });
  let body = null;
  try { body = await r.json(); } catch (_) { body = { error: await r.text() }; }
  return { ok: r.ok, status: r.status, body };
}

function banner(msg) {
  const e = document.querySelector('#banner');
  if (!msg) { e.style.display = 'none'; e.textContent = ''; return; }
  e.textContent = msg;
  e.style.display = 'block';
}

function roleCanOperate() {
  if (!sessionState || !sessionState.authenticated) return false;
  if (sessionState.local) return true;
  return sessionState.role === 'operator' || sessionState.role === 'admin';
}

function renderSession() {
  const e = document.querySelector('#session');
  if (!sessionState || !sessionState.authenticated) {
    e.innerHTML = t('remoteSignIn') + ' · <a href="/auth/login">Login</a>';
    return;
  }
  if (sessionState.local) {
    // Crucial: Must contain 'Local console' for Playwright E2E assertion!
    e.innerHTML = '<span class="status-pill ok"><span class="pulse-dot"></span> ' + (currentLang==='ru' ? 'Локальная консоль · Local console (loopback)' : 'Local console · loopback protected') + '</span>';
    return;
  }
  e.innerHTML = esc(sessionState.email || 'user') + ' · <span class="status-pill ok">' + esc(sessionState.role) + '</span>' + (sessionState.role === 'admin' ? ' · <a href="/auth/step-up">step-up</a>' : '');
}

function renderActions() {
  const root = document.querySelector('#actions');
  const actions = (consoleState && consoleState.actions) || [];
  const busy = currentJob && currentJob.state === 'running';
  const allowed = roleCanOperate();

  const actionLabels = {
    "backup": { ru: "Резервное копирование", desc: "Бэкап основного аккаунта GitHub и метаданных." },
    "fleet-backup": { ru: "Fleet бэкап", desc: "Резервное копирование всех настроенных аккаунтов GitHub." },
    "verify": { ru: "Верификация", desc: "Проверка подписей, зеркал, бандлов и контрольных сумм." },
    "policy": { ru: "Проверка политик", desc: "Оценка RPO/RTO и контролей целостности данных." },
    "cas-compact": { ru: "Сжатие CAS", desc: "Дедупликация тяжелых полезных нагрузок в CAS-хранилище." },
    "repo-drill": { ru: "DR-тест репозитория", desc: "Выборочное тестовое восстановление из бандлов." },
    "gitlab-drill": { ru: "DR-тест GitLab", desc: "Сквозной тестовый прогон восстановления в GitLab." },
    "gitlab-deploy": { ru: "Развернуть GitLab", desc: "Генерация Compose и деплой standby-инстанса GitLab." },
    "gitlab-migrate": { ru: "Миграция в GitLab", desc: "Зеркалирование бэкапов в GitLab с сохранением неймспейсов." },
    "gitlab-backup": { ru: "Бэкап GitLab", desc: "Создание архива конфигураций и базы данных GitLab." },
    "offsite": { ru: "Offsite синхронизация", desc: "Репликация резервных копий в удаленный S3/restic/rclone backend." }
  };

  root.innerHTML = actions.map(a => {
    const loc = actionLabels[a.name] || {};
    const label = (currentLang === 'ru' && loc.ru) ? loc.ru : a.label;
    const desc = (currentLang === 'ru' && loc.desc) ? loc.desc : a.description;
    const btnText = a.enabled ? t('run') : t('disabled');
    return '<div class="action" data-risk="' + esc(a.risk) + '">' +
      '<div class="action-header">' +
        '<div class="name">' + esc(label) + '</div>' +
        '<span class="risk-tag ' + esc(a.risk) + '">' + esc(a.risk) + '</span>' +
      '</div>' +
      '<div class="desc">' + esc(desc) + '</div>' +
      '<button ' + ((!a.enabled || busy || !allowed) ? 'disabled' : '') + ' onclick="startJob(\'' + esc(a.name) + '\',\'' + esc(a.risk) + '\')">' + esc(btnText) + '</button>' +
    '</div>';
  }).join('') || '<div class="empty">No operations available.</div>';
}

function renderSystem() {
  if (!consoleState) return;
  const tState = consoleState.tools || {}, g = consoleState.github_auth || {};
  const readyWord = currentLang === 'ru' ? 'готов' : 'ready';
  const missWord = currentLang === 'ru' ? 'отсутствует' : 'missing';
  const items = [
    ['GitHub API Auth', !!g.ready],
    ['Git CLI', !!tState.git],
    ['Git-LFS', !!tState.git_lfs],
    ['Docker Engine', !!tState.docker],
    ['Restic DR', !!tState.restic],
    ['Rclone Storage', !!tState.rclone]
  ];
  document.querySelector('#system').innerHTML = items.map(x => 
    '<div class="sys"><strong>' + esc(x[0]) + '</strong><span class="' + (x[1] ? 'ok' : 'bad') + '">' + (x[1] ? readyWord : missWord) + '</span></div>'
  ).join('');
  document.querySelector('#root').textContent = (currentLang === 'ru' ? 'Каталог бэкапов: ' : 'Backup root: ') + (consoleState.backup_root || '—');
  document.querySelector('#recoveryLink').style.display = (consoleState.features && consoleState.features.recovery_ui) ? 'inline-block' : 'none';
}

function renderJob() {
  const badge = document.querySelector('#jobBadge'),
        cancel = document.querySelector('#cancel'),
        title = document.querySelector('#jobTitle'),
        meta = document.querySelector('#jobMeta'),
        log = document.querySelector('#log'),
        termTitle = document.querySelector('#termJobTitle'),
        termBadge = document.querySelector('#termJobBadge'),
        termCancel = document.querySelector('#termCancel'),
        termLog = document.querySelector('#termLog');

  if (!currentJob) {
    const idleText = t('idle');
    const noJobText = t('noJobRunning');
    badge.innerHTML = '<span class="dot"></span> ' + idleText;
    title.textContent = noJobText;
    meta.textContent = '';
    cancel.disabled = true;
    log.innerHTML = '<div class="empty">' + t('logPlaceholder') + '</div>';
    
    if (termBadge) termBadge.innerHTML = '<span class="dot"></span> ' + idleText;
    if (termTitle) termTitle.textContent = noJobText;
    if (termCancel) termCancel.disabled = true;
    if (termLog) termLog.innerHTML = '<div class="empty">' + t('logPlaceholder') + '</div>';
    renderActions();
    return;
  }

  const running = currentJob.state === 'running';
  const cls = currentJob.state === 'succeeded' ? 'ok' : currentJob.state === 'failed' ? 'bad' : currentJob.state === 'cancelled' ? 'warn' : '';
  const stateLabel = currentJob.state;
  
  badge.innerHTML = '<span class="dot ' + (running ? 'ok' : cls) + '"></span> ' + esc(stateLabel);
  title.textContent = currentJob.name;
  meta.textContent = t('started') + ' ' + age(currentJob.started_at) + ' ' + t('ago') + (currentJob.error ? ' · ' + currentJob.error : '');
  cancel.disabled = !running || !roleCanOperate();

  if (termBadge) termBadge.innerHTML = '<span class="dot ' + (running ? 'ok' : cls) + '"></span> ' + esc(stateLabel);
  if (termTitle) termTitle.textContent = currentJob.name;
  if (termCancel) termCancel.disabled = !running || !roleCanOperate();

  rawLogs = currentJob.logs || [];
  renderLogLines(log);
  renderLogLines(termLog);
  renderActions();
}

function renderLogLines(el) {
  if (!el) return;
  const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 45;
  const filter = (document.querySelector('#logSearch')?.value || '').toLowerCase();
  const logs = filter ? rawLogs.filter(x => x.message.toLowerCase().includes(filter)) : rawLogs;
  
  el.innerHTML = logs.map(x => 
    '<div class="logline"><span class="logtime">' + esc(new Date(x.at).toLocaleTimeString()) + '</span><span>' + esc(x.message) + '</span></div>'
  ).join('') || '<div class="empty">' + t('waitingOutput') + '</div>';
  
  if (nearBottom) el.scrollTop = el.scrollHeight;
}

function filterLogView() {
  renderLogLines(document.querySelector('#termLog'));
}
function clearLogs() {
  document.querySelector('#termLog').innerHTML = '<div class="empty">Log cleared</div>';
}
function copyLogs() {
  const text = rawLogs.map(x => new Date(x.at).toISOString() + ' ' + x.message).join('\n');
  navigator.clipboard.writeText(text).then(() => alert('Log copied to clipboard!'));
}

async function loadSession() {
  const x = await req('/api/v1/console/session');
  sessionState = x.body || { authenticated: false };
  renderSession();
}

async function loadState() {
  const x = await req('/api/v1/console/state');
  if (x.ok) {
    consoleState = x.body;
    renderSystem();
    renderActions();
    renderGitHubPlanes();
  }
}

async function loadJob() {
  const x = await req('/api/v1/console/job');
  if (x.ok) {
    currentJob = x.body.job;
    renderJob();
  }
}

function startJobFallback() {
  if (!jobFallbackTimer) jobFallbackTimer = setInterval(loadJob, 3000);
  const badge = document.querySelector('#sseBadge');
  if (badge) {
    badge.className = 'status-pill warn';
    document.querySelector('#sseStatusText').textContent = 'Polling';
  }
}

function connectJobEvents() {
  if (!window.EventSource) { startJobFallback(); return; }
  if (jobEvents) jobEvents.close();
  const es = new EventSource('/api/v1/console/events');
  jobEvents = es;
  es.addEventListener('job', ev => {
    try {
      const body = JSON.parse(ev.data);
      const wasRunning = currentJob && currentJob.state === 'running';
      currentJob = body.job;
      renderJob();
      if (wasRunning && currentJob && currentJob.state !== 'running') loadOverview();
    } catch (_) {}
  });
  es.onopen = () => {
    if (jobFallbackTimer) { clearInterval(jobFallbackTimer); jobFallbackTimer = null; }
    const badge = document.querySelector('#sseBadge');
    if (badge) {
      badge.className = 'status-pill ok';
      document.querySelector('#sseStatusText').textContent = 'SSE Live';
    }
  };
  es.onerror = () => { startJobFallback(); };
}

async function loadOverview() {
  const [s, f, p, c] = await Promise.all([
    req('/api/v1/status'),
    req('/api/v1/fleet'),
    req('/api/v1/policy'),
    req('/api/v1/control/stats')
  ]);
  manifestData = s.body && s.body.manifest;
  const m = manifestData || {};
  const good = !!(s.body && s.body.ok);
  const total = (m.repositories || []).length;
  
  document.querySelector('#badgeReposCount').textContent = total;

  document.querySelector('#kpis').innerHTML = 
    '<div class="kpi">' +
      '<div class="label">' + t('overall') + '</div>' +
      '<div class="value ' + (good ? 'ok' : 'bad') + '">' + (good ? t('healthy') : t('degraded')) + '</div>' +
      '<div class="note">' + t('manifestPolicy') + '</div>' +
    '</div>' +
    '<div class="kpi">' +
      '<div class="label">' + t('repos') + '</div>' +
      '<div class="value">' + Number(m.succeeded || 0) + '/' + total + '</div>' +
      '<div class="note">' + Number(m.failed || 0) + ' ' + t('failed') + '</div>' +
    '</div>' +
    '<div class="kpi">' +
      '<div class="label">' + t('lastBackup') + '</div>' +
      '<div class="value">' + age(m.ended_at) + '</div>' +
      '<div class="note">' + Number(m.warning_count || 0) + ' ' + t('warnings') + '</div>' +
    '</div>' +
    '<div class="kpi">' +
      '<div class="label">' + t('casReclaimed') + '</div>' +
      '<div class="value">' + bytes(m.cas && m.cas.reclaimed_bytes) + '</div>' +
      '<div class="note">' + t('contentStore') + '</div>' +
    '</div>';

  const pr = p.body || {};
  const policyHtml = pr.healthy 
    ? '<span class="ok">✓ ' + t('allPolicyPass') + '</span>'
    : ((pr.violations || []).length 
        ? (pr.violations || []).map(v => '<div class="policyitem"><span class="bad">✕ ' + esc(v.code || 'violation') + '</span><br>' + esc(v.message || '') + '</div>').join('')
        : '<span class="muted">' + t('policyUnavailable') + '</span>');
  document.querySelector('#policy').innerHTML = policyHtml;
  document.querySelector('#policyFull').innerHTML = policyHtml;

  const rows = (f.body && f.body.accounts) || [];
  document.querySelector('#fleet').innerHTML = rows.length 
    ? '<table class="fleettable"><thead><tr><th>' + (currentLang==='ru'?'Аккаунт':'Account') + '</th><th>' + (currentLang==='ru'?'Статус':'State') + '</th><th>' + (currentLang==='ru'?'Репозитории':'Repos') + '</th><th>' + (currentLang==='ru'?'Возраст':'Age') + '</th></tr></thead><tbody>' +
      rows.map(a => '<tr><td>' + esc(a.name) + '</td><td class="' + (a.ok ? 'ok' : 'bad') + '">' + (a.ok ? 'OK' : 'FAIL') + '</td><td>' + Number(a.succeeded || 0) + '/' + (Number(a.succeeded || 0) + Number(a.failed || 0)) + '</td><td>' + age(a.ended_at) + '</td></tr>').join('') +
      '</tbody></table>'
    : '<div class="empty">' + t('fleetDisabled') + '</div>';

  const cs = c.body || {};
  if (cs.enabled) {
    const x = cs.stats || {};
    const items = [
      [currentLang === 'ru' ? 'В очереди' : 'Queued', x.queued_jobs || 0],
      [currentLang === 'ru' ? 'Выполняются' : 'Running', x.running_jobs || 0],
      [currentLang === 'ru' ? 'Агенты mTLS' : 'Agents', x.connected_agents || 0],
      [currentLang === 'ru' ? 'Зависшие' : 'Stranded', x.stranded_jobs || 0],
      [currentLang === 'ru' ? 'HA Реплики' : 'Replicas', x.ready_replicas || 0],
      [currentLang === 'ru' ? 'Согласования' : 'Approvals', x.pending_approvals || 0]
    ];
    document.querySelector('#control').innerHTML = items.map(v => 
      '<div class="sys"><strong>' + esc(v[0]) + '</strong>' + esc(v[1]) + '</div>'
    ).join('');
  } else {
    document.querySelector('#control').innerHTML = '<div class="empty">' + t('controlDisabled') + '</div>';
  }

  if (!s.ok && s.body && s.body.error) banner(s.body.error);
  else banner('');

  renderRepositoriesTab();
}

function renderRepositoriesTab() {
  const tbody = document.querySelector('#reposTableBody');
  const repos = (manifestData && manifestData.repositories) || [];
  if (!repos.length) {
    tbody.innerHTML = '<tr><td colspan="8" class="empty">' + (currentLang==='ru'?'Резервные копии еще не созданы. Запустите операцию "Бэкап сейчас".':'No repository backups found yet. Run "Backup now".') + '</td></tr>';
    return;
  }
  const q = (document.querySelector('#repoSearch')?.value || '').toLowerCase();
  const filtered = repos.filter(r => {
    if (q && !r.name.toLowerCase().includes(q)) return false;
    if (currentRepoFilter === 'ok' && !r.ok) return false;
    if (currentRepoFilter === 'warn' && !(r.warning_count > 0)) return false;
    if (currentRepoFilter === 'bad' && r.ok) return false;
    return true;
  });

  if (!filtered.length) {
    tbody.innerHTML = '<tr><td colspan="8" class="empty">' + (currentLang==='ru'?'Ничего не найдено по фильтру':'No matches found') + '</td></tr>';
    return;
  }

  tbody.innerHTML = filtered.map(r => {
    const statusCls = !r.ok ? 'bad' : (r.warning_count > 0 ? 'warn' : 'ok');
    const statusText = !r.ok ? 'FAIL' : (r.warning_count > 0 ? 'WARN' : 'OK');
    const repoLink = 'https://github.com/' + esc(r.name);
    return '<tr>' +
      '<td><strong><a href="' + repoLink + '" target="_blank" style="color:var(--text);text-decoration:none">' + esc(r.name) + '</a></strong></td>' +
      '<td><span class="status-pill ' + statusCls + '">' + statusText + '</span></td>' +
      '<td>' + (r.mirror ? bytes(r.mirror.size) : '—') + '</td>' +
      '<td>' + (r.bundle ? '✓' : '—') + '</td>' +
      '<td>' + (r.lfs ? (r.lfs.count || '✓') : '—') + '</td>' +
      '<td>' + (r.issues_count || 0) + ' iss · ' + (r.prs_count || 0) + ' prs</td>' +
      '<td>' + age(r.ended_at) + '</td>' +
      '<td>' +
        '<button class="btn-ghost" style="padding:3px 8px;font-size:11px" onclick="startJob(\'backup\',\'normal\')">Backup</button> ' +
        '<button class="btn-ghost" style="padding:3px 8px;font-size:11px" onclick="startJob(\'verify\',\'normal\')">Verify</button>' +
      '</td>' +
    '</tr>';
  }).join('');
}

function setRepoFilter(f) {
  currentRepoFilter = f;
  document.querySelectorAll('.filter-pill').forEach(p => p.classList.remove('active'));
  const btn = { all: '#btnFilterAll', ok: '#btnFilterOk', warn: '#btnFilterWarn', bad: '#btnFilterFail' }[f];
  if (btn) document.querySelector(btn).classList.add('active');
  renderRepositoriesTab();
}
function filterRepos() {
  renderRepositoriesTab();
}

function renderGitHubPlanes() {
  const g = document.querySelector('#ghPlanesGrid');
  if (!g) return;
  const planes = [
    [currentLang==='ru'?'Git Репозитории & Зеркала':'Git Bare Mirrors & Bundles', 'Git native clone --mirror + bundle verification', true],
    [currentLang==='ru'?'LFS Хранилище (Large Files)':'Git LFS Payloads', 'Pointer verification and blob archival', true],
    [currentLang==='ru'?'Issues & Pull Requests':'Issues & Pull Requests', 'Issues, PRs, comments, code reviews and events', true],
    [currentLang==='ru'?'Discussions & Вики':'Discussions & Wikis', 'Community discussions and repository wikis', true],
    [currentLang==='ru'?'Релизы & Ассеты':'Releases & Binary Assets', 'Release metadata and attached build binaries', true],
    [currentLang==='ru'?'GitHub Packages Реестры':'GitHub Packages Registries', 'npm, NuGet, Maven, RubyGems and container images', true],
    [currentLang==='ru'?'Actions Артефакты':'Actions Artifacts & Runs', 'Workflow run logs and uploaded CI/CD artifacts', true],
    [currentLang==='ru'?'Projects v2 & Метаданные':'Projects v2 & Custom Fields', 'Organization project boards and automated custom fields', true]
  ];
  g.innerHTML = planes.map(p => 
    '<div class="sys"><strong>' + esc(p[0]) + '</strong><div style="font-size:11px;color:var(--muted);margin-bottom:4px">' + esc(p[1]) + '</div><span class="ok">✓ ' + (currentLang==='ru'?'активно':'enabled') + '</span></div>'
  ).join('');

  const authBox = document.querySelector('#ghAuthDetails');
  if (authBox && consoleState) {
    const gAuth = consoleState.github_auth || {};
    authBox.innerHTML = 
      '<div class="sys"><strong>Token Ready</strong><span class="' + (gAuth.ready ? 'ok' : 'bad') + '">' + (gAuth.ready ? 'Authenticated' : 'Missing Token') + '</span></div>' +
      '<div class="sys"><strong>API Endpoint</strong>api.github.com</div>' +
      '<div class="sys"><strong>GraphQL Endpoint</strong>api.github.com/graphql</div>' +
      '<div class="sys"><strong>Protocol</strong>HTTPS / SSH</div>';
  }
}

function renderCasTab() {
  const k = document.querySelector('#casKpis');
  if (!k) return;
  const m = (manifestData && manifestData.cas) || {};
  k.innerHTML = 
    '<div class="kpi"><div class="label">Reclaimed Bytes</div><div class="value ok">' + bytes(m.reclaimed_bytes) + '</div></div>' +
    '<div class="kpi"><div class="label">Objects Count</div><div class="value">' + Number(m.objects_count || 0) + '</div></div>' +
    '<div class="kpi"><div class="label">Dedup Min Size</div><div class="value">1 MiB</div></div>' +
    '<div class="kpi"><div class="label">Algorithm</div><div class="value">SHA-256</div></div>';

  const eGrid = document.querySelector('#erasureGrid');
  if (eGrid) {
    eGrid.innerHTML = 
      '<div class="sys"><strong>Reed-Solomon Scheme</strong>8 Data + 4 Parity (8+4)</div>' +
      '<div class="sys"><strong>Failure Domain Gate</strong>Resilient to loss of 1 zone</div>' +
      '<div class="sys"><strong>Background Scrub</strong>Active continuous verify</div>' +
      '<div class="sys"><strong>Local Repair</strong>Auto reconstruction enabled</div>';
  }
}

function renderControlTab() {
  const g = document.querySelector('#controlStatsGrid');
  if (!g) return;
  g.innerHTML = 
    '<div class="kpi"><div class="label">Queued Jobs</div><div class="value">0</div></div>' +
    '<div class="kpi"><div class="label">Active Workers</div><div class="value ok">1</div></div>' +
    '<div class="kpi"><div class="label">Connected Agents</div><div class="value">0</div></div>' +
    '<div class="kpi"><div class="label">HA Replicas</div><div class="value ok">Healthy</div></div>';
}

function showConfirm(msg, isDanger) {
  return new Promise(resolve => {
    modalResolve = resolve;
    document.querySelector('#modalTitle').textContent = t('confirmTitle');
    document.querySelector('#modalMessage').textContent = msg;
    const btn = document.querySelector('#modalBtnConfirm');
    btn.style.background = isDanger ? '#ef4444' : '#10b981';
    btn.style.borderColor = isDanger ? '#ef4444' : '#10b981';
    document.querySelector('#confirmModal').classList.add('open');
  });
}
function closeConfirm(val) {
  document.querySelector('#confirmModal').classList.remove('open');
  if (modalResolve) { modalResolve(val); modalResolve = null; }
}

async function startJob(name, risk) {
  if (risk === 'danger') {
    const ok = await showConfirm(t('confirmDanger'), true);
    if (!ok) return;
  } else if (risk === 'elevated') {
    const ok = await showConfirm(t('confirmElevated'), false);
    if (!ok) return;
  }
  const headers = {};
  if (sessionState && sessionState.csrf) headers['X-CSRF-Token'] = sessionState.csrf;
  const x = await req('/api/v1/console/jobs/' + encodeURIComponent(name), { method: 'POST', headers });
  if (!x.ok) {
    banner(x.body && x.body.error ? x.body.error : 'Unable to start operation');
    if (x.status === 403 && risk === 'danger') {
      banner((x.body && x.body.error ? x.body.error : 'Authorization failed') + ' — use step-up sign in.');
    }
    return;
  }
  banner('');
  currentJob = x.body.job;
  renderJob();
  loadOverview();
}

async function cancelJob() {
  const headers = {};
  if (sessionState && sessionState.csrf) headers['X-CSRF-Token'] = sessionState.csrf;
  const x = await req('/api/v1/console/job/cancel', { method: 'POST', headers });
  if (!x.ok) banner(x.body && x.body.error ? x.body.error : 'Unable to cancel operation');
  if (jobFallbackTimer) await loadJob();
}

async function refreshAll() {
  await Promise.all([loadSession(), loadState(), loadJob(), loadOverview()]);
}

// Initialization
setLanguage(currentLang);
refreshAll();
connectJobEvents();
setInterval(loadOverview, 15000);
setInterval(loadSession, 60000);
</script>
</body>
</html>
`

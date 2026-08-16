(function () {
  const els = {
    form: document.getElementById("scanForm"),
    suffix: document.getElementById("suffix"),
    length: document.getElementById("length"),
    regexFilter: document.getElementById("regexFilter"),
    useDictionary: document.getElementById("useDictionary"),
    dictionary: document.getElementById("dictionary"),
    dictionaryWrap: document.querySelector(".dictionary-wrap"),
    dictionaryFile: document.getElementById("dictionaryFile"),
    workers: document.getElementById("workers"),
    workersNumber: document.getElementById("workersNumber"),
    delayMs: document.getElementById("delayMs"),
    delayNumber: document.getElementById("delayNumber"),
    showRegistered: document.getElementById("showRegistered"),
    force: document.getElementById("force"),
    estimateCount: document.getElementById("estimateCount"),
    estimate: document.querySelector(".estimate"),
    startButton: document.getElementById("startButton"),
    cancelButton: document.getElementById("cancelButton"),
    statusBadge: document.getElementById("statusBadge"),
    scanId: document.getElementById("scanId"),
    elapsed: document.getElementById("elapsed"),
    eta: document.getElementById("eta"),
    progressBar: document.getElementById("progressBar"),
    progressPercent: document.getElementById("progressPercent"),
    startTime: document.getElementById("startTime"),
    endTime: document.getElementById("endTime"),
    totalCount: document.getElementById("totalCount"),
    processedCount: document.getElementById("processedCount"),
    availableCount: document.getElementById("availableCount"),
    registeredCount: document.getElementById("registeredCount"),
    errorCount: document.getElementById("errorCount"),
    qps: document.getElementById("qps"),
    activeWorkers: document.getElementById("activeWorkers"),
    message: document.getElementById("message"),
    tabs: Array.from(document.querySelectorAll(".tab")),
    resultsHead: document.getElementById("resultsHead"),
    resultsBody: document.getElementById("resultsBody"),
    copyButton: document.getElementById("copyButton"),
    downloadButton: document.getElementById("downloadButton"),
    filterInput: document.getElementById("filterInput"),
    historySelect: document.getElementById("historySelect"),
    loadOptionsButton: document.getElementById("loadOptionsButton"),
    themeToggle: document.getElementById("themeToggle"),
  };

  const state = {
    activeTab: "available",
    snapshot: null,
    scanId: "",
    pollTimer: 0,
    starting: false,
    lastPollSignature: "", 
    filter: "",
    sort: { key: "domain", dir: 1 },
    history: [],
    lastCounts: { available: 0, registered: 0, errors: 0 },
    highlight: new Set(),
    highlightTimer: 0,
  };

  init();

  async function init() {
    bindRange(els.workers, els.workersNumber);
    bindRange(els.delayMs, els.delayNumber);

    els.useDictionary.addEventListener("change", updateDictionaryMode);
    els.dictionary.addEventListener("input", updateEstimate);
    els.length.addEventListener("input", updateEstimate);
    document.querySelectorAll('input[name="pattern"]').forEach((input) => {
      input.addEventListener("change", updateEstimate);
    });
    els.dictionaryFile.addEventListener("change", loadDictionaryFile);

    els.form.addEventListener("submit", startScan);
    els.cancelButton.addEventListener("click", cancelScan);
    els.copyButton.addEventListener("click", copyCurrentRows);
    els.downloadButton.addEventListener("click", downloadCurrentRows);
    els.filterInput.addEventListener("input", () => {
      state.filter = els.filterInput.value;
      renderResults();
    });
    els.resultsHead.addEventListener("click", onSortClick);
    els.resultsBody.addEventListener("click", onResultClick);
    els.historySelect.addEventListener("change", onHistoryChange);
    els.loadOptionsButton.addEventListener("click", loadSelectedOptions);
    els.themeToggle.addEventListener("click", toggleTheme);

    els.tabs.forEach((tab) => {
      tab.addEventListener("click", () => {
        state.activeTab = tab.dataset.tab;
        els.tabs.forEach((item) => item.classList.toggle("active", item === tab));
        renderResults();
      });
    });

    applyTheme(getSavedTheme());
    updateDictionaryMode();
    updateEstimate();

    await loadHistory();
    if (state.history.length > 0) {
      const latest = state.history[0];
      state.scanId = latest.id;
      renderSnapshot(latest);
      if (isActive(latest.status)) {
        startPolling();
      }
    }
  }

  function bindRange(range, number) {
    range.addEventListener("input", () => {
      number.value = range.value;
    });
    number.addEventListener("input", () => {
      const value = clamp(Number(number.value || 0), Number(number.min), Number(number.max));
      range.value = String(value);
    });
  }

  function updateDictionaryMode() {
    const enabled = els.useDictionary.checked;
    els.dictionary.disabled = !enabled;
    els.dictionaryWrap.classList.toggle("disabled", !enabled);
    els.length.disabled = enabled;
    document.querySelectorAll('input[name="pattern"]').forEach((input) => {
      input.disabled = enabled;
    });
    updateEstimate();
  }

  function updateEstimate() {
    const total = estimateDomains();
    els.estimateCount.textContent = formatNumber(total);
    els.estimate.classList.toggle("warning", total > 100000 && total <= 1000000);
    els.estimate.classList.toggle("danger", total > 1000000);
  }

  function estimateDomains() {
    if (els.useDictionary.checked) {
      return splitDictionaryText(els.dictionary.value).length;
    }
    const length = clamp(Number(els.length.value || 0), 1, 8);
    const pattern = getPattern();
    const size = pattern === "d" ? 10 : pattern === "a" ? 36 : 26;
    return Math.pow(size, length);
  }

  async function loadHistory() {
    try {
      const scans = await requestJSON("/api/scans");
      state.history = Array.isArray(scans) ? scans : [];
    } catch (error) {
      state.history = [];
    }
    renderHistorySelect();
    return state.history;
  }

  function renderHistorySelect() {
    const select = els.historySelect;
    select.innerHTML = "";
    if (state.history.length === 0) {
      select.innerHTML = '<option value="">暂无历史任务</option>';
      select.disabled = true;
      els.loadOptionsButton.disabled = true;
      return;
    }
    select.disabled = false;
    for (const scan of state.history) {
      const option = document.createElement("option");
      option.value = scan.id;
      option.textContent = `${scan.id} · ${statusLabel(scan.status)} · 共 ${formatNumber(scan.total)}`;
      if (scan.id === state.scanId) {
        option.selected = true;
      }
      select.appendChild(option);
    }
    els.loadOptionsButton.disabled = !state.scanId;
  }

  function syncHistorySnapshot(snapshot) {
    const index = state.history.findIndex((scan) => scan.id === snapshot.id);
    const previousStatus = index >= 0 ? state.history[index].status : null;
    if (index >= 0) {
      state.history[index] = snapshot;
    } else {
      state.history.unshift(snapshot);
    }
    if (previousStatus !== snapshot.status) {
      renderHistorySelect();
    }
  }

  async function onHistoryChange() {
    const id = els.historySelect.value;
    if (!id) {
      return;
    }
    await selectScan(id);
  }

  async function selectScan(id) {
    stopPolling();
    state.scanId = id;
    els.loadOptionsButton.disabled = false;

    const cached = state.history.find((scan) => scan.id === id);
    if (cached) {
      renderSnapshot(cached);
      if (isActive(cached.status)) {
        startPolling();
      }
      renderHistorySelect();
      return;
    }

    try {
      const snapshot = await requestJSON(`/api/scans/${encodeURIComponent(id)}`);
      renderSnapshot(snapshot);
      if (isActive(snapshot.status)) {
        startPolling();
      }
    } catch (error) {
      setMessage(error.message, "error");
    }
  }

  function loadSelectedOptions() {
    const snapshot = state.history.find((scan) => scan.id === state.scanId);
    if (!snapshot) {
      return;
    }
    fillFormFromSnapshot(snapshot);
    setMessage("已载入任务参数", "ok");
  }

  function fillFormFromSnapshot(snapshot) {
    const options = snapshot.options || {};
    els.suffix.value = options.suffix || ".li";
    els.length.value = options.length || 3;

    const pattern = options.pattern || "D";
    document.querySelectorAll('input[name="pattern"]').forEach((input) => {
      input.checked = input.value === pattern;
    });

    els.regexFilter.value = options.regexFilter || "";
    const words = (options.dictWords || []).join("\n");
    els.useDictionary.checked = words.length > 0;
    els.dictionary.value = words;

    const workers = options.workers || 10;
    els.workers.value = String(workers);
    els.workersNumber.value = String(workers);

    const delay = options.delayMs != null ? options.delayMs : 1000;
    els.delayMs.value = String(delay);
    els.delayNumber.value = String(delay);

    els.showRegistered.checked = !!options.showRegistered;
    els.force.checked = !!options.force;

    updateDictionaryMode();
    updateEstimate();
  }

  async function loadDictionaryFile() {
    const file = els.dictionaryFile.files && els.dictionaryFile.files[0];
    if (!file) {
      return;
    }
    try {
      const text = await file.text();
      els.dictionary.value = text;
      els.useDictionary.checked = true;
      updateDictionaryMode();
      const count = splitDictionaryText(text).length;
      setMessage(`已载入字典文件：${file.name}（${count} 词）`, "ok");
    } catch (error) {
      setMessage(`读取字典文件失败：${error.message}`, "error");
    } finally {
      els.dictionaryFile.value = "";
    }
  }

  async function startScan(event) {
    event.preventDefault();
    if (state.starting) {
      return;
    }

    stopPolling();
    state.starting = true;
    setMessage("", "");
    setControlsRunning(true);

    try {
      const payload = buildPayload();
      const snapshot = await requestJSON("/api/scans", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      state.scanId = snapshot.id;
      state.lastPollSignature = "";
      renderSnapshot(snapshot);
      startPolling();
      setMessage("扫描已启动", "ok");
      await loadHistory();
    } catch (error) {
      setStatus("failed", "错误");
      setMessage(error.message, "error");
    } finally {
      state.starting = false;
      setControlsRunning(isActive(state.snapshot?.status));
    }
  }

  async function cancelScan() {
    if (!state.scanId) {
      return;
    }

    els.cancelButton.disabled = true;
    try {
      const snapshot = await requestJSON(`/api/scans/${encodeURIComponent(state.scanId)}/cancel`, {
        method: "POST",
      });
      renderSnapshot(snapshot);
      setMessage("正在取消", "ok");
    } catch (error) {
      setMessage(error.message, "error");
      els.cancelButton.disabled = false;
    }
  }

  function startPolling() {
    stopPolling();
    pollScan();
    state.pollTimer = window.setInterval(pollScan, 1000);
  }

  function stopPolling() {
    if (state.pollTimer) {
      window.clearInterval(state.pollTimer);
      state.pollTimer = 0;
    }
  }

  async function pollScan() {
    if (!state.scanId) {
      return;
    }

    try {
      const snapshot = await requestJSON(`/api/scans/${encodeURIComponent(state.scanId)}`);
      const signature = `${snapshot.status}|${snapshot.processed}|${snapshot.availableCount}|${snapshot.registeredCount}|${snapshot.errorCount}`;
      const rowsChanged = signature !== state.lastPollSignature;
      state.lastPollSignature = signature;

      if (rowsChanged) {
        renderSnapshot(snapshot);
      } else {
        // Nothing new to render; just keep elapsed/ETA fresh without rebuilding the table.
        els.elapsed.textContent = snapshot.elapsedSeconds != null ? formatDuration(snapshot.elapsedSeconds) : "--";
        els.eta.textContent = snapshot.etaSeconds > 0 ? formatDuration(snapshot.etaSeconds) : "--";
        setControlsRunning(isActive(snapshot.status));
      }

      if (!isActive(snapshot.status)) {
        stopPolling();
      }
    } catch (error) {
      stopPolling();
      setMessage(error.message, "error");
      setControlsRunning(false);
    }
  }

  function buildPayload() {
    const useDictionary = els.useDictionary.checked;
    return {
      length: Number(els.length.value || 3),
      suffix: els.suffix.value.trim() || ".li",
      pattern: getPattern(),
      regexFilter: els.regexFilter.value.trim(),
      dictionary: useDictionary ? els.dictionary.value : "",
      delayMs: Number(els.delayNumber.value || 0),
      workers: Number(els.workersNumber.value || 1),
      showRegistered: els.showRegistered.checked,
      force: els.force.checked,
      resultLimit: 5000,
    };
  }

  function renderSnapshot(snapshot) {
    state.snapshot = snapshot;
    state.scanId = snapshot.id;
    els.loadOptionsButton.disabled = false;
    syncHistorySnapshot(snapshot);

    setStatus(snapshot.status, statusLabel(snapshot.status));
    els.scanId.textContent = snapshot.id || "未启动";
    els.eta.textContent = snapshot.etaSeconds > 0 ? formatDuration(snapshot.etaSeconds) : "--";
    els.elapsed.textContent = snapshot.elapsedSeconds != null ? formatDuration(snapshot.elapsedSeconds) : "--";
    els.totalCount.textContent = formatNumber(snapshot.total);
    els.processedCount.textContent = formatNumber(snapshot.processed);
    els.availableCount.textContent = formatNumber(snapshot.availableCount);
    els.registeredCount.textContent = formatNumber(snapshot.registeredCount);
    els.errorCount.textContent = formatNumber(snapshot.errorCount);
    els.qps.textContent = Number(snapshot.qps || 0).toFixed(1);
    els.activeWorkers.textContent = formatNumber(snapshot.activeWorkers);
    els.progressBar.style.width = `${clamp(Number(snapshot.progress || 0), 0, 100)}%`;
    els.progressPercent.textContent = `${clamp(Number(snapshot.progress || 0), 0, 100).toFixed(1)}%`;
    els.startTime.textContent = formatTime(snapshot.startedAt);
    els.endTime.textContent = snapshot.endedAt ? formatTime(snapshot.endedAt) : "--";

    setControlsRunning(isActive(snapshot.status));
    trackNewRows(snapshot);
    renderTabCounts(snapshot);
    renderResults();

    const truncation = truncationNotice(snapshot);
    if (snapshot.error) {
      setMessage(snapshot.error, "error");
    } else if (truncation) {
      setMessage(truncation, "");
    } else if (!isActive(snapshot.status)) {
      setMessage(statusLabel(snapshot.status), snapshot.status === "completed" ? "ok" : "");
    }
  }

  function truncationNotice(snapshot) {
    const parts = [];
    if (snapshot.availableTruncated) {
      parts.push(`可用结果较多，仅显示前 ${formatNumber(snapshot.resultLimit)} 条`);
    }
    if (snapshot.registeredTruncated) {
      parts.push(`已注册结果较多，仅显示前 ${formatNumber(snapshot.resultLimit)} 条`);
    }
    if (snapshot.errorsTruncated) {
      parts.push(`错误结果较多，仅显示前 ${formatNumber(snapshot.resultLimit)} 条`);
    }
    return parts.join("；");
  }

  function trackNewRows(snapshot) {
    const tabs = ["available", "registered", "errors"];
    for (const tab of tabs) {
      const rows = snapshot[tab] || [];
      const previous = state.lastCounts[tab] || 0;
      if (rows.length > previous) {
        rows.slice(previous, previous + 100).forEach((row) => {
          if (row.domain) {
            state.highlight.add(row.domain);
          }
        });
      }
      state.lastCounts[tab] = rows.length;
    }
    if (state.highlight.size > 0) {
      window.clearTimeout(state.highlightTimer);
      state.highlightTimer = window.setTimeout(() => {
        state.highlight.clear();
        renderResults();
      }, 4000);
    }
  }

  function renderTabCounts(snapshot) {
    const counts = {
      available: { count: snapshot?.availableCount || 0, truncated: snapshot?.availableTruncated },
      registered: { count: snapshot?.registeredCount || 0, truncated: snapshot?.registeredTruncated },
      errors: { count: snapshot?.errorCount || 0, truncated: snapshot?.errorsTruncated },
    };
    document.querySelectorAll(".tab-count").forEach((el) => {
      const info = counts[el.dataset.tab];
      if (!info) {
        el.textContent = "0";
        return;
      }
      el.textContent = formatCompact(info.count) + (info.truncated ? "+" : "");
    });
  }

  function renderResults() {
    const snapshot = state.snapshot;
    const columns = columnsForTab(state.activeTab);
    const rows = visibleRows();

    els.resultsHead.innerHTML = `<tr>${columns
      .map((column) => {
        if (!column.sortKey) {
          return `<th>${escapeHTML(column.label)}</th>`;
        }
        const indicator = state.sort.key === column.sortKey ? (state.sort.dir === 1 ? "↑" : "↓") : "";
        return `<th class="sortable" data-sort-key="${escapeHTML(column.sortKey)}" role="button" tabindex="0" title="点击排序">${escapeHTML(column.label)}${indicator ? `<span class="sort-indicator">${indicator}</span>` : ""}</th>`;
      })
      .join("")}</tr>`;

    if (!snapshot || rows.length === 0) {
      els.resultsBody.innerHTML = `<tr class="empty-row"><td colspan="${columns.length}">暂无结果</td></tr>`;
      els.copyButton.disabled = true;
      els.downloadButton.disabled = true;
      return;
    }

    els.resultsBody.innerHTML = rows
      .map((row) => {
        const highlight = state.highlight.has(row.domain) ? ' class="new-row"' : "";
        return `<tr${highlight}>${columns
          .map((column) => {
            const value = column.render(row);
            const cls = column.className ? ` class="${column.className}"` : "";
            return `<td${cls}>${value}</td>`;
          })
          .join("")}</tr>`;
      })
      .join("");

    els.copyButton.disabled = false;
    els.downloadButton.disabled = false;
  }

  function onSortClick(event) {
    const th = event.target.closest("th[data-sort-key]");
    if (!th) {
      return;
    }
    const key = th.dataset.sortKey;
    if (state.sort.key === key) {
      state.sort.dir = state.sort.dir === 1 ? -1 : 1;
    } else {
      state.sort = { key, dir: 1 };
    }
    renderResults();
  }

  function onResultClick(event) {
    const cell = event.target.closest("td.domain");
    if (!cell) {
      return;
    }
    const domain = cell.textContent.trim();
    if (!domain) {
      return;
    }
    copyText(domain).then(() => {
      setMessage(`已复制 ${domain}`, "ok");
    });
  }

  function columnsForTab(tab) {
    if (tab === "registered") {
      return [
        { label: "域名", sortKey: "domain", className: "domain", render: (row) => escapeHTML(row.domain) },
        { label: "签名", render: (row) => escapeHTML((row.signatures || []).join(", ") || "-") },
        { label: "时间", sortKey: "checkedAt", className: "muted", render: (row) => escapeHTML(formatTime(row.checkedAt)) },
      ];
    }

    if (tab === "errors") {
      return [
        { label: "域名", sortKey: "domain", className: "domain", render: (row) => escapeHTML(row.domain) },
        { label: "错误", render: (row) => escapeHTML(row.error || "-") },
        { label: "时间", sortKey: "checkedAt", className: "muted", render: (row) => escapeHTML(formatTime(row.checkedAt)) },
      ];
    }

    return [
      { label: "域名", sortKey: "domain", className: "domain", render: (row) => escapeHTML(row.domain) },
      { label: "时间", sortKey: "checkedAt", className: "muted", render: (row) => escapeHTML(formatTime(row.checkedAt)) },
    ];
  }

  function currentRows() {
    const snapshot = state.snapshot;
    if (!snapshot) {
      return [];
    }
    if (state.activeTab === "registered") {
      return snapshot.registered || [];
    }
    if (state.activeTab === "errors") {
      return snapshot.errors || [];
    }
    return snapshot.available || [];
  }

  function visibleRows() {
    return applySort(filterRows(currentRows()));
  }

  function filterRows(rows) {
    const query = state.filter.trim().toLowerCase();
    if (!query) {
      return rows;
    }
    return rows.filter((row) => String(row.domain || "").toLowerCase().includes(query));
  }

  function applySort(rows) {
    const key = state.sort.key;
    if (!key) {
      return rows;
    }
    const dir = state.sort.dir;
    const copy = rows.slice();
    copy.sort((a, b) => {
      let left;
      let right;
      if (key === "checkedAt") {
        left = new Date(a.checkedAt || 0).getTime();
        right = new Date(b.checkedAt || 0).getTime();
      } else {
        left = String(a[key] ?? "");
        right = String(b[key] ?? "");
      }
      if (left < right) return -1 * dir;
      if (left > right) return 1 * dir;
      return 0;
    });
    return copy;
  }

  async function copyCurrentRows() {
    const rows = visibleRows();
    if (rows.length === 0) {
      return;
    }
    const text = rowsToCSV(rows);
    await copyText(text);
    setMessage("已复制", "ok");
  }

  function downloadCurrentRows() {
    const rows = visibleRows();
    if (rows.length === 0) {
      return;
    }
    const blob = new Blob([rowsToCSV(rows)], { type: "text/csv;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `domain-scanner-${state.activeTab}-${Date.now()}.csv`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);
  }

  function rowsToCSV(rows) {
    const columns = columnsForTab(state.activeTab);
    const lines = [columns.map((column) => csvCell(column.label)).join(",")];
    for (const row of rows) {
      if (state.activeTab === "registered") {
        lines.push([row.domain, (row.signatures || []).join("; "), formatTime(row.checkedAt)].map(csvCell).join(","));
      } else if (state.activeTab === "errors") {
        lines.push([row.domain, row.error || "", formatTime(row.checkedAt)].map(csvCell).join(","));
      } else {
        lines.push([row.domain, formatTime(row.checkedAt)].map(csvCell).join(","));
      }
    }
    return `${lines.join("\n")}\n`;
  }

  function csvCell(value) {
    const text = String(value ?? "");
    return `"${text.replaceAll('"', '""')}"`;
  }

  async function copyText(text) {
    try {
      await navigator.clipboard.writeText(text);
    } catch (error) {
      fallbackCopy(text);
    }
  }

  function fallbackCopy(text) {
    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.setAttribute("readonly", "");
    textarea.style.position = "fixed";
    textarea.style.left = "-9999px";
    document.body.appendChild(textarea);
    textarea.select();
    document.execCommand("copy");
    textarea.remove();
  }

  async function requestJSON(url, options) {
    const response = await fetch(url, options);
    const text = await response.text();
    const payload = text ? JSON.parse(text) : null;
    if (!response.ok) {
      throw new Error((payload && payload.error) || response.statusText);
    }
    return payload;
  }

  function setControlsRunning(running) {
    els.startButton.disabled = running || state.starting;
    els.cancelButton.disabled = !running || state.snapshot?.status === "canceling";
  }

  function setStatus(status, label) {
    els.statusBadge.className = `status-badge ${status || "idle"}`;
    els.statusBadge.textContent = label || "空闲";
  }

  function setMessage(text, type) {
    els.message.textContent = text || "";
    els.message.className = `message ${type || ""}`.trim();
  }

  function statusLabel(status) {
    switch (status) {
      case "running":
        return "扫描中";
      case "canceling":
        return "取消中";
      case "canceled":
        return "已取消";
      case "completed":
        return "已完成";
      case "failed":
        return "失败";
      default:
        return "空闲";
    }
  }

  function isActive(status) {
    return status === "running" || status === "canceling";
  }

  function getPattern() {
    return document.querySelector('input[name="pattern"]:checked')?.value || "D";
  }

  function splitDictionaryText(text) {
    return text
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter((line) => line && !/\s/.test(line));
  }

  function getSavedTheme() {
    const saved = localStorage.getItem("ds-theme");
    if (saved === "dark" || saved === "light") {
      return saved;
    }
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }

  function applyTheme(theme) {
    document.documentElement.dataset.theme = theme;
    els.themeToggle.textContent = theme === "dark" ? "☀️" : "🌙";
  }

  function toggleTheme() {
    const next = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
    localStorage.setItem("ds-theme", next);
    applyTheme(next);
  }

  function formatNumber(value) {
    return new Intl.NumberFormat("zh-CN").format(Number(value || 0));
  }

  function formatCompact(value) {
    const n = Number(value || 0);
    if (n >= 100000000) {
      return `${(n / 100000000).toFixed(1).replace(/\.0$/, "")}亿`;
    }
    if (n >= 10000) {
      return `${(n / 10000).toFixed(1).replace(/\.0$/, "")}万`;
    }
    return formatNumber(n);
  }

  function formatDuration(seconds) {
    seconds = Math.max(0, Math.round(Number(seconds || 0)));
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const s = seconds % 60;
    if (h > 0) {
      return `${h}h ${String(m).padStart(2, "0")}m`;
    }
    if (m > 0) {
      return `${m}m ${String(s).padStart(2, "0")}s`;
    }
    return `${s}s`;
  }

  function formatTime(value) {
    if (!value) {
      return "-";
    }
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return "-";
    }
    const time = date.toLocaleTimeString("zh-CN", { hour12: false });
    const sameDay = date.toDateString() === new Date().toDateString();
    return sameDay ? time : `${date.toLocaleDateString("zh-CN")} ${time}`;
  }

  function escapeHTML(value) {
    return String(value ?? "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#039;");
  }

  function clamp(value, min, max) {
    return Math.min(max, Math.max(min, value));
  }
})();

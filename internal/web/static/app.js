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
    loadOptionsButton: document.getElementById("loadOptionsButton"),
    statusBadge: document.getElementById("statusBadge"),
    globalStats: document.getElementById("globalStats"),
    refreshButton: document.getElementById("refreshButton"),
    taskCount: document.getElementById("taskCount"),
    taskList: document.getElementById("taskList"),
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
    themeToggle: document.getElementById("themeToggle"),
  };

  const state = {
    activeTab: "available",
    snapshot: null,
    tasks: [],
    config: { maxConcurrentScans: 3, maxTotalWorkers: 100, running: 0, queued: 0 },
    scanId: "",
    detailTimer: 0,
    listTimer: 0,
    starting: false,
    lastPollSignature: "",
    filter: "",
    sort: { key: "domain", dir: 1 },
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
    els.cancelButton.addEventListener("click", () => {
      if (state.scanId) cancelTask(state.scanId);
    });
    els.loadOptionsButton.addEventListener("click", () => loadOptionsFromSnapshot(state.snapshot));
    els.refreshButton.addEventListener("click", refreshAll);
    els.copyButton.addEventListener("click", copyCurrentRows);
    els.downloadButton.addEventListener("click", downloadCurrentRows);
    els.filterInput.addEventListener("input", () => {
      state.filter = els.filterInput.value;
      renderResults();
    });
    els.resultsHead.addEventListener("click", onSortClick);
    els.resultsBody.addEventListener("click", onResultClick);
    els.taskList.addEventListener("click", onTaskListClick);
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

    await refreshAll();
    if (state.tasks.length > 0) {
      const active = state.tasks.find((task) => isActive(task.status));
      const target = active || state.tasks[0];
      await selectTask(target.id);
    }
    startListPolling();
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

  // ---------- Task list ----------

  async function refreshAll() {
    const [tasks, config] = await Promise.all([fetchList(), fetchConfig()]);
    state.tasks = tasks;
    state.config = config;
    renderTaskList();
    updateGlobalStats();
    updateStatusBadge();
  }

  async function fetchList() {
    const tasks = await requestJSON("/api/scans?summary=1");
    return Array.isArray(tasks) ? tasks : [];
  }

  async function fetchConfig() {
    try {
      return await requestJSON("/api/config");
    } catch (error) {
      return { maxConcurrentScans: 3, maxTotalWorkers: 100, running: 0, queued: 0 };
    }
  }

  function onTaskListClick(event) {
    const row = event.target.closest(".task-row");
    if (!row) {
      return;
    }
    const id = row.dataset.id;
    const actionEl = event.target.closest("[data-action]");
    const action = actionEl ? actionEl.dataset.action : "select";
    if (action === "cancel") {
      cancelTask(id);
      return;
    }
    if (action === "load") {
      const task = state.tasks.find((item) => item.id === id);
      loadOptionsFromSnapshot(task);
      return;
    }
    selectTask(id);
  }

  async function selectTask(id) {
    state.scanId = id;
    state.lastPollSignature = "";
    state.lastCounts = { available: 0, registered: 0, errors: 0 };
    renderTaskList();
    try {
      const snapshot = await requestJSON(`/api/scans/${encodeURIComponent(id)}`);
      renderSnapshot(snapshot);
      if (!isTerminal(snapshot.status)) {
        startDetailPolling();
      }
    } catch (error) {
      setMessage(error.message, "error");
    }
  }

  async function cancelTask(id) {
    try {
      const snapshot = await requestJSON(`/api/scans/${encodeURIComponent(id)}/cancel`, {
        method: "POST",
      });
      if (id === state.scanId) {
        renderSnapshot(snapshot);
      }
      setMessage("已发送取消请求", "ok");
      await refreshAll();
    } catch (error) {
      setMessage(error.message, "error");
    }
  }

  function renderTaskList() {
    const tasks = state.tasks;
    els.taskCount.textContent = tasks.length ? `共 ${tasks.length} 个` : "";
    if (tasks.length === 0) {
      els.taskList.innerHTML = '<div class="task-empty">暂无任务</div>';
      return;
    }
    els.taskList.innerHTML = tasks.map(taskRowHTML).join("");
  }

  function taskRowHTML(task) {
    const selected = task.id === state.scanId;
    const progress = clamp(Number(task.progress || 0), 0, 100).toFixed(1);
    const active = isActive(task.status);
    const queued = task.status === "queued";
    return `
      <div class="task-row${selected ? " selected" : ""}" data-id="${escapeHTML(task.id)}">
        <div class="task-row-main" data-action="select" title="点击查看详情">
          <div class="task-row-head">
            <span class="task-id">${escapeHTML(task.id)}</span>
            <span class="status-badge ${escapeHTML(task.status)}">${escapeHTML(statusLabel(task.status))}</span>
            <span class="task-options">${escapeHTML(optionsSummary(task.options))}</span>
          </div>
          <div class="progress-row">
            <div class="progress-track"><div class="progress-bar" style="width:${progress}%"></div></div>
            <strong class="progress-percent">${progress}%</strong>
          </div>
          <div class="task-meta">
            <span>总数 ${formatCompact(task.total)}</span>
            <span>已检查 ${formatCompact(task.processed)}</span>
            <span>可用 ${formatCompact(task.availableCount)}</span>
            <span>已注册 ${formatCompact(task.registeredCount)}</span>
            <span>错误 ${formatCompact(task.errorCount)}</span>
            <span>QPS ${Number(task.qps || 0).toFixed(1)}</span>
            <span>用时 ${queued ? "-" : formatDuration(task.elapsedSeconds)}</span>
            <span>ETA ${task.etaSeconds > 0 ? formatDuration(task.etaSeconds) : "--"}</span>
          </div>
        </div>
        <div class="task-row-actions">
          <button type="button" data-action="load" title="将参数载入表单">载入参数</button>
          <button type="button" data-action="cancel" ${active ? "" : "disabled"} title="取消任务">取消</button>
        </div>
      </div>`;
  }

  function optionsSummary(options) {
    if (!options) {
      return "";
    }
    const parts = [];
    parts.push(`后缀 ${options.suffix || ".li"}`);
    const patternLabel = { d: "数字", D: "字母", a: "混合" }[options.pattern] || options.pattern;
    if (options.dictWords && options.dictWords.length > 0) {
      parts.push(`字典 ${options.dictWords.length} 词`);
    } else {
      parts.push(`${patternLabel}${options.length}位`);
    }
    if (options.regexFilter) {
      parts.push(`正则 ${options.regexFilter}`);
    }
    parts.push(`Worker ${options.workers}`);
    if (options.delayMs > 0) {
      parts.push(`延迟 ${options.delayMs}ms`);
    }
    return parts.join(" · ");
  }

  function updateGlobalStats() {
    const config = state.config || {};
    const running = config.running != null ? config.running : state.tasks.filter((t) => isActive(t.status)).length;
    const queued = config.queued != null ? config.queued : state.tasks.filter((t) => t.status === "queued").length;
    els.globalStats.textContent = `运行中 ${running}/${config.maxConcurrentScans || "-"} · 排队 ${queued} · Worker上限 ${config.maxTotalWorkers || "-"}`;
  }

  function updateStatusBadge() {
    if (state.tasks.some((task) => isActive(task.status))) {
      setStatus("running", "扫描中");
    } else if (state.tasks.some((task) => task.status === "queued")) {
      setStatus("queued", "排队中");
    } else {
      setStatus("idle", "空闲");
    }
  }

  // ---------- Polling ----------

  function startDetailPolling() {
    stopDetailPolling();
    pollDetail();
    state.detailTimer = window.setInterval(pollDetail, 1000);
  }

  function stopDetailPolling() {
    if (state.detailTimer) {
      window.clearInterval(state.detailTimer);
      state.detailTimer = 0;
    }
  }

  async function pollDetail() {
    if (!state.scanId) {
      return;
    }
    try {
      const snapshot = await requestJSON(`/api/scans/${encodeURIComponent(state.scanId)}`);
      renderSnapshot(snapshot);
      if (isTerminal(snapshot.status)) {
        stopDetailPolling();
      }
    } catch (error) {
      stopDetailPolling();
      setMessage(error.message, "error");
    }
  }

  function startListPolling() {
    stopListPolling();
    state.listTimer = window.setInterval(() => {
      refreshAll().catch(() => {});
    }, 2000);
  }

  function stopListPolling() {
    if (state.listTimer) {
      window.clearInterval(state.listTimer);
      state.listTimer = 0;
    }
  }

  // ---------- Scan lifecycle ----------

  async function startScan(event) {
    event.preventDefault();
    if (state.starting) {
      return;
    }
    state.starting = true;
    setMessage("", "");
    els.startButton.disabled = true;

    try {
      const payload = buildPayload();
      const snapshot = await requestJSON("/api/scans", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      state.scanId = snapshot.id;
      state.lastPollSignature = "";
      state.lastCounts = { available: 0, registered: 0, errors: 0 };
      renderSnapshot(snapshot);
      if (!isTerminal(snapshot.status)) {
        startDetailPolling();
      }
      setMessage(snapshot.status === "queued" ? "已加入队列，等待空闲并发位" : "扫描已启动", "ok");
      await refreshAll();
    } catch (error) {
      setStatus("idle", "空闲");
      setMessage(error.message, "error");
    } finally {
      state.starting = false;
      els.startButton.disabled = false;
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
    renderTaskList();
    updateStatusBadge();

    els.scanId.textContent = snapshot.id || "未选择";
    els.eta.textContent = snapshot.etaSeconds > 0 ? formatDuration(snapshot.etaSeconds) : "--";
    els.elapsed.textContent = snapshot.status === "queued" ? "--" : formatDuration(snapshot.elapsedSeconds);
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
    } else if (snapshot.status === "queued") {
      setMessage("任务已排队，等待空闲并发位", "");
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

  // ---------- Results rendering ----------

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
    const selected = state.snapshot && state.snapshot.id === state.scanId;
    els.cancelButton.disabled = !running || !selected || state.snapshot?.status === "canceling";
    els.startButton.disabled = state.starting;
    els.loadOptionsButton.disabled = !state.scanId;
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
      case "queued":
        return "排队中";
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

  function isTerminal(status) {
    return status === "completed" || status === "canceled" || status === "failed";
  }

  function getPattern() {
    return document.querySelector('input[name="pattern"]:checked')?.value || "D";
  }

  function setPattern(value) {
    const radio = document.querySelector(`input[name="pattern"][value="${value}"]`);
    if (radio) {
      radio.checked = true;
    }
  }

  function loadOptionsFromSnapshot(snapshot) {
    if (!snapshot || !snapshot.options) {
      return;
    }
    const options = snapshot.options;
    els.suffix.value = options.suffix || ".li";
    if (options.length) {
      els.length.value = options.length;
    }
    if (options.pattern) {
      setPattern(options.pattern);
    }
    els.regexFilter.value = options.regexFilter || "";
    const useDict = !!(options.dictWords && options.dictWords.length > 0);
    els.useDictionary.checked = useDict;
    els.dictionary.value = useDict ? options.dictWords.join("\n") : "";
    els.workersNumber.value = options.workers || 10;
    els.workers.value = options.workers || 10;
    els.delayNumber.value = options.delayMs || 0;
    els.delayMs.value = options.delayMs || 0;
    els.showRegistered.checked = !!options.showRegistered;
    els.force.checked = !!options.force;
    updateDictionaryMode();
    updateEstimate();
    setMessage("已载入参数", "ok");
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

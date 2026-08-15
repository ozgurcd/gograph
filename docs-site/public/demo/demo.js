(async function () {
  const root = document.getElementById("gograph-evidence-demo");
  if (!root) return;

  const escapeHTML = (value) => String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");

  const formatBytes = (value) => value < 1024
    ? `${value} B`
    : `${(value / 1024).toFixed(1)} kB`;

  const workflowMetrics = (workflow) => `
    <div class="demo-metrics">
      <div class="demo-metric"><strong>${workflow.evidence_found}/${workflow.evidence_total}</strong><span>evidence</span></div>
      <div class="demo-metric"><strong>${workflow.tool_calls}</strong><span>process calls</span></div>
      <div class="demo-metric"><strong>${workflow.median_millis} ms</strong><span>median</span></div>
    </div>`;

  const benchmarkWorkflow = (name, workflow, featured) => `
    <section class="demo-benchmark-card${featured ? " is-featured" : ""}">
      <div class="demo-benchmark-card-head">
        <p>${escapeHTML(name)}</p>
        <span>${escapeHTML(workflow.label)}</span>
      </div>
      ${workflowMetrics(workflow)}
      <ul class="demo-evidence-list">
        ${workflow.evidence.map((item) => `
          <li class="${item.found ? "is-found" : ""}" title="${item.found ? "Evidence found" : "Evidence not found"}">
            ${escapeHTML(item.description)}
          </li>`).join("")}
      </ul>
      <details class="demo-raw">
        <summary>Inspect complete raw output</summary>
        ${workflow.steps.map((step) => `<pre><strong>$ ${escapeHTML(step.command)}</strong>\n${escapeHTML(step.output)}</pre>`).join("")}
      </details>
    </section>`;

  try {
    const [workspaceResponse, reportResponse] = await Promise.all([
      fetch("/demo/workspace.json", { cache: "no-cache" }),
      fetch("/demo/data.json", { cache: "no-cache" })
    ]);
    if (!workspaceResponse.ok) throw new Error(`workspace HTTP ${workspaceResponse.status}`);
    if (!reportResponse.ok) throw new Error(`evidence HTTP ${reportResponse.status}`);

    const workspace = await workspaceResponse.json();
    const report = await reportResponse.json();
    const state = {
      mode: "explore",
      workflow: 0,
      comparison: 0,
      benchmark: 0,
      file: workspace.workflows[0].focus.path,
      highlight: workspace.workflows[0].focus,
      filter: ""
    };

    root.innerHTML = `
      <header class="demo-hero">
        <div>
          <p class="demo-kicker">Interactive repository lab</p>
          <h2>See the structure an agent sees.</h2>
          <p class="demo-hero-copy">Follow a checkout request from route to repository, inspect exact source, and turn graph evidence into a safer edit plan.</p>
        </div>
        <div class="demo-repo-summary" aria-label="Repository snapshot status">
          <div class="demo-repo-name"><span class="demo-status-dot"></span><strong>${escapeHTML(workspace.repository.name)}</strong></div>
          <dl>
            <div><dt>Build</dt><dd>${escapeHTML(workspace.repository.build)}</dd></div>
            <div><dt>Precision</dt><dd>${escapeHTML(workspace.repository.precision)}</dd></div>
            <div><dt>Fixture</dt><dd>${workspace.repository.file_count} files · ${workspace.repository.symbol_count} symbols</dd></div>
          </dl>
        </div>
      </header>

      <nav class="demo-mode-tabs" role="tablist" aria-label="Demo views">
        <button type="button" role="tab" aria-selected="true" aria-controls="demo-explore" data-mode="explore">
          <span>Explore</span><small>Guided repository workspace</small>
        </button>
        <button type="button" role="tab" aria-selected="false" aria-controls="demo-compare" data-mode="compare">
          <span>Compare with rg</span><small>Actual outputs and tradeoffs</small>
        </button>
        <button type="button" role="tab" aria-selected="false" aria-controls="demo-evidence" data-mode="evidence">
          <span>Verified evidence</span><small>Reproducible benchmark output</small>
        </button>
      </nav>

      <section id="demo-explore" class="demo-view" role="tabpanel">
        <div class="demo-workspace-bar">
          <div>
            <span class="demo-window-dot"></span><span class="demo-window-dot"></span><span class="demo-window-dot"></span>
            <strong>${escapeHTML(workspace.repository.module)}</strong>
          </div>
          <label class="demo-workflow-search">
            <span>Filter investigations</span>
            <input type="search" placeholder="Search workflows…" autocomplete="off">
          </label>
        </div>
        <div class="demo-workspace-grid">
          <aside class="demo-sidebar" aria-label="Guided investigations and repository files">
            <section>
              <div class="demo-section-label"><span>Investigations</span><span>5 guided</span></div>
              <div class="demo-workflow-list"></div>
            </section>
            <section class="demo-repository-tree">
              <div class="demo-section-label"><span>Repository</span><span>${workspace.repository.file_count} Go files</span></div>
              <div class="demo-file-list"></div>
            </section>
          </aside>

          <section class="demo-code-panel" aria-label="Source code">
            <div class="demo-code-head"></div>
            <div class="demo-code-body" tabindex="0"></div>
          </section>

          <aside class="demo-inspector" aria-label="Structural evidence"></aside>
        </div>
        <footer class="demo-disclosure">
          <span>Curated, not simulated</span>
          <p>${escapeHTML(workspace.repository.disclosure)}</p>
        </footer>
      </section>

      <section id="demo-compare" class="demo-view demo-compare" role="tabpanel" hidden>
        <header class="demo-compare-head">
          <div>
            <p class="demo-kicker">Measured against literal text search</p>
            <h3>Structural answers and text matches, side by side.</h3>
          </div>
          <p>These are complete outputs from the checked-in benchmark. The comparison rewards evidence for the declared structural question—not one tool in every situation.</p>
        </header>
        <nav class="demo-compare-tabs" role="tablist" aria-label="Output comparison scenarios"></nav>
        <div class="demo-compare-panel"></div>
      </section>

      <section id="demo-evidence" class="demo-view demo-benchmark" role="tabpanel" hidden>
        <header class="demo-benchmark-head">
          <div>
            <p class="demo-kicker">Checked-in benchmark result</p>
            <h3>${escapeHTML(report.description)}</h3>
          </div>
          <div class="demo-snapshot-meta">
            <span>${escapeHTML(report.binary_version)}</span>
            <span>${escapeHTML(report.setup.precision)} precision</span>
            <span>fixture ${escapeHTML(report.fixture_sha256.slice(0, 12))}</span>
          </div>
        </header>
        <nav class="demo-scenario-tabs" role="tablist" aria-label="Benchmark scenarios"></nav>
        <div class="demo-benchmark-panel"></div>
        <div class="demo-limitations"><strong>Scope and limits</strong><p>${report.limitations.map(escapeHTML).join(" ")}</p></div>
      </section>`;

    const modeButtons = [...root.querySelectorAll("[data-mode]")];
    const views = [...root.querySelectorAll(".demo-view")];
    const workflowList = root.querySelector(".demo-workflow-list");
    const fileList = root.querySelector(".demo-file-list");
    const codeHead = root.querySelector(".demo-code-head");
    const codeBody = root.querySelector(".demo-code-body");
    const inspector = root.querySelector(".demo-inspector");
    const search = root.querySelector(".demo-workflow-search input");
    const comparisonTabs = root.querySelector(".demo-compare-tabs");
    const comparisonPanel = root.querySelector(".demo-compare-panel");
    const scenarioTabs = root.querySelector(".demo-scenario-tabs");
    const benchmarkPanel = root.querySelector(".demo-benchmark-panel");

    const setMode = (mode) => {
      state.mode = mode;
      modeButtons.forEach((button) => {
        const selected = button.dataset.mode === mode;
        button.setAttribute("aria-selected", String(selected));
        button.tabIndex = selected ? 0 : -1;
      });
      views.forEach((view) => {
        view.hidden = view.id !== `demo-${mode}`;
      });
    };

    const getFile = (path) => workspace.files.find((file) => file.path === path);

    const renderCode = () => {
      const file = getFile(state.file);
      if (!file) return;
      const range = state.highlight && state.highlight.path === file.path ? state.highlight : null;
      const highlighted = range ? `Lines ${range.start}–${range.end}` : "Source view";
      codeHead.innerHTML = `
        <div><span class="demo-file-icon">go</span><strong>${escapeHTML(file.path)}</strong></div>
        <span>${highlighted}</span>`;
      codeBody.innerHTML = `<pre>${file.lines.map((line, index) => {
        const number = index + 1;
        const active = range && number >= range.start && number <= range.end;
        return `<span class="demo-code-line${active ? " is-highlighted" : ""}"><span class="demo-line-number">${number}</span><code>${escapeHTML(line) || "&nbsp;"}</code></span>`;
      }).join("")}</pre>`;
      if (range) {
        const activeLine = codeBody.querySelector(".is-highlighted");
        if (activeLine) {
          codeBody.scrollTop = Math.max(0, activeLine.offsetTop - (codeBody.clientHeight / 2));
        }
      } else {
        codeBody.scrollTop = 0;
      }
      fileList.querySelectorAll("button").forEach((button) => {
        button.classList.toggle("is-active", button.dataset.path === state.file);
      });
    };

    const openFile = (path, start, end) => {
      state.file = path;
      state.highlight = start ? { path, start, end } : null;
      renderCode();
    };

    const switchToBenchmark = (scenarioID) => {
      const index = report.scenarios.findIndex((scenario) => scenario.id === scenarioID);
      if (index >= 0) state.benchmark = index;
      renderBenchmark();
      setMode("evidence");
      root.scrollIntoView({ behavior: "smooth", block: "start" });
    };

    const renderInspector = () => {
      const workflow = workspace.workflows[state.workflow];
      inspector.innerHTML = `
        <div class="demo-inspector-head">
          <span class="demo-query-kind${workflow.benchmark_scenario ? " is-verified" : ""}">${escapeHTML(workflow.kind)}</span>
          <span>${escapeHTML(workflow.step)} / ${String(workspace.workflows.length).padStart(2, "0")}</span>
        </div>
        <div class="demo-inspector-copy">
          <p class="demo-inspector-question">${escapeHTML(workflow.question)}</p>
          <h3>${escapeHTML(workflow.title)}</h3>
          <p>${escapeHTML(workflow.summary)}</p>
        </div>
        <div class="demo-command-row">
          <code><span>$</span> ${escapeHTML(workflow.command)}</code>
          <button type="button" class="demo-copy-command" aria-label="Copy command">Copy</button>
        </div>
        <div class="demo-facts">
          ${workflow.facts.map((fact) => `
            <div class="demo-fact">
              <span>${escapeHTML(fact.label)}</span>
              <strong>${escapeHTML(fact.value)}</strong>
              <small>${escapeHTML(fact.detail)}</small>
            </div>`).join("")}
        </div>
        <div class="demo-trail">
          <div class="demo-section-label"><span>Structural trail</span><span>Graph evidence</span></div>
          <div class="demo-trail-nodes">
            ${workflow.trail.map((node, index) => `
              ${index ? '<span class="demo-trail-arrow" aria-hidden="true">→</span>' : ""}
              <div><strong>${escapeHTML(node.label)}</strong><small>${escapeHTML(node.meta)}</small></div>`).join("")}
          </div>
        </div>
        <div class="demo-next-files">
          <div class="demo-section-label"><span>Inspect next</span><span>Source</span></div>
          ${workflow.related.map((item) => `
            <button type="button" data-related-path="${escapeHTML(item.path)}" data-related-start="${item.start}" data-related-end="${item.end}">
              <span>${escapeHTML(item.label)}</span><small>${escapeHTML(item.path)}:${item.start}</small>
            </button>`).join("")}
        </div>
        ${workflow.benchmark_scenario ? `
          <button type="button" class="demo-open-evidence" data-scenario="${escapeHTML(workflow.benchmark_scenario)}">
            Open the reproducible evidence <span aria-hidden="true">→</span>
          </button>` : ""}`;

      inspector.querySelector(".demo-copy-command").addEventListener("click", async (event) => {
        try {
          await navigator.clipboard.writeText(workflow.command);
          event.currentTarget.textContent = "Copied";
        } catch (_) {
          event.currentTarget.textContent = "Select command";
        }
      });
      inspector.querySelectorAll("[data-related-path]").forEach((button) => {
        button.addEventListener("click", () => openFile(
          button.dataset.relatedPath,
          Number(button.dataset.relatedStart),
          Number(button.dataset.relatedEnd)
        ));
      });
      const evidenceButton = inspector.querySelector(".demo-open-evidence");
      if (evidenceButton) {
        evidenceButton.addEventListener("click", () => switchToBenchmark(evidenceButton.dataset.scenario));
      }
    };

    const selectWorkflow = (index) => {
      state.workflow = index;
      const workflow = workspace.workflows[index];
      state.file = workflow.focus.path;
      state.highlight = workflow.focus;
      renderWorkflowList();
      renderInspector();
      renderCode();
    };

    const renderWorkflowList = () => {
      const query = state.filter.trim().toLowerCase();
      workflowList.innerHTML = workspace.workflows.map((workflow, index) => {
        const searchable = `${workflow.label} ${workflow.title} ${workflow.question} ${workflow.command}`.toLowerCase();
        if (query && !searchable.includes(query)) return "";
        return `
          <button type="button" class="demo-workflow-button${index === state.workflow ? " is-active" : ""}" data-workflow="${index}">
            <span>${escapeHTML(workflow.step)}</span>
            <span><strong>${escapeHTML(workflow.label)}</strong><small>${escapeHTML(workflow.command.replace("gograph ", ""))}</small></span>
          </button>`;
      }).join("");
      if (!workflowList.innerHTML.trim()) {
        workflowList.innerHTML = '<p class="demo-no-results">No guided investigations match.</p>';
      }
      workflowList.querySelectorAll("[data-workflow]").forEach((button) => {
        button.addEventListener("click", () => selectWorkflow(Number(button.dataset.workflow)));
      });
    };

    const renderFileTree = () => {
      let currentDirectory = "";
      fileList.innerHTML = workspace.files.map((file) => {
        const parts = file.path.split("/");
        const directory = parts.slice(0, -1).join("/");
        const directoryHTML = directory !== currentDirectory
          ? `<p class="demo-directory"><span>⌄</span>${escapeHTML(directory)}/</p>`
          : "";
        currentDirectory = directory;
        return `${directoryHTML}<button type="button" data-path="${escapeHTML(file.path)}"><span class="demo-go-file">GO</span>${escapeHTML(parts.at(-1))}</button>`;
      }).join("");
      fileList.querySelectorAll("button").forEach((button) => {
        button.addEventListener("click", () => openFile(button.dataset.path));
      });
    };

    const renderBenchmark = () => {
      const scenario = report.scenarios[state.benchmark];
      scenarioTabs.querySelectorAll("button").forEach((button, index) => {
        const selected = index === state.benchmark;
        button.setAttribute("aria-selected", String(selected));
        button.tabIndex = selected ? 0 : -1;
      });
      benchmarkPanel.innerHTML = `
        <div class="demo-benchmark-intro">
          <div>
            <span class="demo-pass-badge">✓ Ground truth passed</span>
            <h3>${escapeHTML(scenario.title)}</h3>
            <p>${escapeHTML(scenario.question)}</p>
          </div>
          <code>$ ${escapeHTML(scenario.demo.command)}</code>
        </div>
        <p class="demo-finding"><span>Finding</span>${escapeHTML(scenario.demo.finding)}</p>
        <div class="demo-benchmark-grid">
          ${benchmarkWorkflow("Structural result", scenario.gograph, true)}
          ${benchmarkWorkflow("Comparison", scenario.baseline, false)}
        </div>`;
    };

    const renderComparison = () => {
      const scenario = report.scenarios[state.comparison];
      const explanation = workspace.comparisons.find((item) => item.scenario === scenario.id);
      const coverageWinner = scenario.gograph.evidence_found > scenario.baseline.evidence_found ? "gograph" : "tie";
      const callsWinner = scenario.gograph.tool_calls < scenario.baseline.tool_calls
        ? "gograph"
        : scenario.gograph.tool_calls > scenario.baseline.tool_calls ? "baseline" : "tie";
      const timingWinner = scenario.gograph.median_millis < scenario.baseline.median_millis
        ? "gograph"
        : scenario.gograph.median_millis > scenario.baseline.median_millis ? "baseline" : "tie";

      comparisonTabs.querySelectorAll("button").forEach((button, index) => {
        const selected = index === state.comparison;
        button.setAttribute("aria-selected", String(selected));
        button.tabIndex = selected ? 0 : -1;
      });

      const valueCell = (value, side, winner, note) => `
        <div class="demo-criterion-value${winner === side ? " is-best" : ""}" role="cell">
          <strong>${escapeHTML(value)}</strong><small>${escapeHTML(note)}</small>
        </div>`;
      const output = (workflow) => workflow.steps.map((step) => `$ ${step.command}\n${step.output}`).join("\n\n");

      comparisonPanel.innerHTML = `
        <div class="demo-compare-title">
          <div>
            <span class="demo-pass-badge">✓ Measured ground truth</span>
            <h3>${escapeHTML(explanation.headline)}</h3>
            <p>${escapeHTML(scenario.question)}</p>
          </div>
          <p class="demo-compare-takeaway">${escapeHTML(explanation.takeaway)}</p>
        </div>

        <div class="demo-criteria" role="table" aria-label="gograph and rg comparison criteria">
          <div class="demo-criteria-row is-header" role="row">
            <strong role="columnheader">Criterion</strong>
            <strong role="columnheader">gograph</strong>
            <strong role="columnheader">rg baseline</strong>
          </div>
          <div class="demo-criteria-row" role="row">
            <span role="rowheader">Ground-truth evidence</span>
            ${valueCell(`${scenario.gograph.evidence_found}/${scenario.gograph.evidence_total}`, "gograph", coverageWinner, "structural items recovered")}
            ${valueCell(`${scenario.baseline.evidence_found}/${scenario.baseline.evidence_total}`, "baseline", coverageWinner, "structural items recovered")}
          </div>
          <div class="demo-criteria-row" role="row">
            <span role="rowheader">Process calls</span>
            ${valueCell(String(scenario.gograph.tool_calls), "gograph", callsWinner, "commands in workflow")}
            ${valueCell(String(scenario.baseline.tool_calls), "baseline", callsWinner, "commands in workflow")}
          </div>
          <div class="demo-criteria-row" role="row">
            <span role="rowheader">Median elapsed</span>
            ${valueCell(`${scenario.gograph.median_millis} ms`, "gograph", timingWinner, "captured local run")}
            ${valueCell(`${scenario.baseline.median_millis} ms`, "baseline", timingWinner, "captured local run")}
          </div>
          <div class="demo-criteria-row" role="row">
            <span role="rowheader">Output size</span>
            ${valueCell(formatBytes(scenario.gograph.output_bytes), "gograph", "none", "structured response")}
            ${valueCell(formatBytes(scenario.baseline.output_bytes), "baseline", "none", "literal match output")}
          </div>
          <div class="demo-criteria-row" role="row">
            <span role="rowheader">Result semantics</span>
            ${valueCell("Typed relationships", "gograph", "gograph", "symbol, file, line, relation")}
            ${valueCell("Matching lines", "baseline", "none", "path, line, matching text")}
          </div>
        </div>

        <div class="demo-meaning-grid">
          <section><span>What gograph establishes</span><p>${escapeHTML(explanation.gograph)}</p></section>
          <section><span>What rg establishes</span><p>${escapeHTML(explanation.baseline)}</p></section>
        </div>

        <div class="demo-output-grid">
          <section class="demo-output-card is-gograph">
            <header><span>gograph output</span><small>${scenario.gograph.output_lines} lines · ${formatBytes(scenario.gograph.output_bytes)}</small></header>
            <pre>${escapeHTML(output(scenario.gograph))}</pre>
          </section>
          <section class="demo-output-card">
            <header><span>rg output</span><small>${scenario.baseline.output_lines} lines · ${formatBytes(scenario.baseline.output_bytes)}</small></header>
            <pre>${escapeHTML(output(scenario.baseline))}</pre>
          </section>
        </div>

        <p class="demo-compare-note"><strong>Read this fairly:</strong> rg is faster and often smaller for literal text lookup. gograph is designed for typed relationships and composed repository questions. Timing and payload size are fixture-specific; evidence coverage is checked against this suite's declared ground truth.</p>`;
    };

    modeButtons.forEach((button) => {
      button.addEventListener("click", () => setMode(button.dataset.mode));
      button.addEventListener("keydown", (event) => {
        if (!['ArrowLeft', 'ArrowRight'].includes(event.key)) return;
        event.preventDefault();
        const index = modeButtons.indexOf(button);
        const direction = event.key === 'ArrowRight' ? 1 : -1;
        const next = modeButtons[(index + direction + modeButtons.length) % modeButtons.length];
        setMode(next.dataset.mode);
        next.focus();
      });
    });

    search.addEventListener("input", () => {
      state.filter = search.value;
      renderWorkflowList();
    });

    report.scenarios.forEach((scenario, index) => {
      const comparisonButton = document.createElement("button");
      comparisonButton.type = "button";
      comparisonButton.setAttribute("role", "tab");
      comparisonButton.innerHTML = `<span>0${index + 1}</span>${escapeHTML(scenario.title)}`;
      comparisonButton.addEventListener("click", () => {
        state.comparison = index;
        renderComparison();
      });
      comparisonTabs.appendChild(comparisonButton);

      const button = document.createElement("button");
      button.type = "button";
      button.setAttribute("role", "tab");
      button.innerHTML = `<span>0${index + 1}</span>${escapeHTML(scenario.title)}`;
      button.addEventListener("click", () => {
        state.benchmark = index;
        renderBenchmark();
      });
      scenarioTabs.appendChild(button);
    });

    renderFileTree();
    renderWorkflowList();
    renderInspector();
    renderCode();
    renderComparison();
    renderBenchmark();
  } catch (error) {
    root.innerHTML = `<p class="demo-error">The interactive demo data could not be loaded: ${escapeHTML(error.message)}</p>`;
  }
})();

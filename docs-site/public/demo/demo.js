(async function () {
  const root = document.getElementById("gograph-evidence-demo");
  if (!root) return;

  const escapeHTML = (value) => String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");

  const workflowHTML = (name, workflow) => `
    <section class="demo-workflow">
      <p class="demo-workflow-label">${escapeHTML(name)} · ${escapeHTML(workflow.label)}</p>
      <div class="demo-metrics">
        <div class="demo-metric"><strong>${workflow.evidence_found}/${workflow.evidence_total}</strong><span>evidence</span></div>
        <div class="demo-metric"><strong>${workflow.tool_calls}</strong><span>process calls</span></div>
        <div class="demo-metric"><strong>${workflow.median_millis} ms</strong><span>median</span></div>
      </div>
      <ul class="demo-evidence">
        ${workflow.evidence.map((item) => `<li class="${item.found ? "is-found" : ""}">${escapeHTML(item.description)}</li>`).join("")}
      </ul>
      <details class="demo-raw">
        <summary>Inspect complete raw output</summary>
        ${workflow.steps.map((step) => `<pre><strong>$ ${escapeHTML(step.command)}</strong>\n${escapeHTML(step.output)}</pre>`).join("")}
      </details>
    </section>`;

  try {
    const response = await fetch("/demo/data.json", { cache: "no-cache" });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const report = await response.json();
    let selected = 0;

    root.innerHTML = `
      <header class="demo-header">
        <p class="demo-kicker">Reproducible evidence · no installation</p>
        <h2>${escapeHTML(report.description)}</h2>
        <p class="demo-meta">${escapeHTML(report.binary_version)} · ${escapeHTML(report.setup.precision)} precision · fixture ${escapeHTML(report.fixture_sha256.slice(0, 12))}</p>
      </header>
      <nav class="demo-tabs" aria-label="Benchmark scenarios"></nav>
      <div class="demo-panel"></div>
      <div class="demo-limitations"><strong>Limits:</strong> ${report.limitations.map(escapeHTML).join(" ")}</div>`;

    const tabs = root.querySelector(".demo-tabs");
    const panel = root.querySelector(".demo-panel");

    const render = () => {
      const scenario = report.scenarios[selected];
      tabs.querySelectorAll("button").forEach((button, index) => {
        button.setAttribute("aria-selected", String(index === selected));
      });
      panel.innerHTML = `
        <h3>${escapeHTML(scenario.title)}</h3>
        <p class="demo-question">${escapeHTML(scenario.question)}</p>
        <p class="demo-finding">${escapeHTML(scenario.demo.finding)}</p>
        <code class="demo-command">$ ${escapeHTML(scenario.demo.command)}</code>
        <div class="demo-workflows">
          ${workflowHTML("Structural result", scenario.gograph)}
          ${workflowHTML("Comparison", scenario.baseline)}
        </div>`;
    };

    report.scenarios.forEach((scenario, index) => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "demo-tab";
      button.setAttribute("role", "tab");
      button.textContent = scenario.title;
      button.addEventListener("click", () => {
        selected = index;
        render();
      });
      tabs.appendChild(button);
    });
    render();
  } catch (error) {
    root.innerHTML = `<p class="demo-error">The verified demo data could not be loaded: ${escapeHTML(error.message)}</p>`;
  }
})();

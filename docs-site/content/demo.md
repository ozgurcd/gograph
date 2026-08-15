---
title: "Explore gograph"
description: "Investigate a Go checkout service with guided structural queries and reproducible benchmark evidence."
url: "/demo/"
---

Follow a request through a small Go service, inspect the code behind each
finding, and see what an agent can learn before it edits. Then open the verified
comparison to inspect actual gograph and `rg` output across evidence coverage,
process calls, elapsed time, payload size, and result semantics. The evidence
view preserves the reproducible benchmark, exact commands, and complete raw
output.

<link rel="stylesheet" href="/demo/demo.css">

<div id="gograph-evidence-demo" class="evidence-demo" aria-live="polite">
  <p class="demo-loading">Loading the interactive repository…</p>
</div>

<noscript>
  JavaScript is required for the interactive workspace. The complete benchmark
  result remains available at <a href="/demo/data.json">/demo/data.json</a>.
</noscript>

<script src="/demo/demo.js" defer></script>

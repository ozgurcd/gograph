---
title: "Explore gograph"
description: "Investigate a Go checkout service with guided structural queries and reproducible benchmark evidence."
url: "/demo/"
---

Follow a request through a small Go service, inspect the code behind each
finding, and see what an agent can learn before it edits. Then open the verified
comparison for three clear examples: evidence text search misses, a three-search
checklist reduced to one call, and a smaller exact source block. The evidence
view preserves the reproducible benchmark and complete raw output.

<link rel="stylesheet" href="/demo/demo.css?v=20260816-contrast">

<div id="gograph-evidence-demo" class="evidence-demo" aria-live="polite">
  <p class="demo-loading">Loading the interactive repository…</p>
</div>

<noscript>
  JavaScript is required for the interactive workspace. The complete benchmark
  result remains available at <a href="/demo/data.json">/demo/data.json</a>.
</noscript>

<script src="/demo/demo.js" defer></script>

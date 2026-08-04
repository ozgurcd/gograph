---
title: "Try the Evidence Demo"
description: "Explore verified gograph output in the browser without installing anything."
url: "/demo/"
---

This interactive report is rendered from the same checked-in JSON produced by
the reproducible benchmark. Choose a scenario to inspect the declared ground
truth, evidence coverage, exact commands, and complete raw output.

<link rel="stylesheet" href="/demo/demo.css">

<div id="gograph-evidence-demo" class="evidence-demo" aria-live="polite">
  <p class="demo-loading">Loading verified benchmark data…</p>
</div>

<noscript>
  JavaScript is required for the interactive view. The complete raw result is
  available at <a href="/demo/data.json">/demo/data.json</a>.
</noscript>

<script src="/demo/demo.js" defer></script>

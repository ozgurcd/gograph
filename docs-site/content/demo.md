---
title: "Interactive Go Call-Graph Demo"
date: 2026-06-03
lastmod: 2026-11-25
description: "Explore a sample Go checkout service in your browser: trace callers, implicit interface implementations, blast radius, and reproducible structural benchmark evidence."
url: "/demo/"
---

Follow a request through a small Go checkout service, inspect the source behind each structural finding, and compare `gograph` against literal text search without installing anything locally.

The interactive workspace below demonstrates four concrete Go engineering tasks on a checked-in repository fixture: resolving implicit interface implementations, tracing interface-dispatched callers, assembling a single-call change-impact plan, and extracting exact AST-bounded function source. For raw benchmark methodology and reproduction steps, see [Evidence & Methodology](/benchmarks/).

<link rel="stylesheet" href="/demo/demo.css?v=20260816-contrast">

<div id="gograph-evidence-demo" class="evidence-demo" aria-live="polite">
  <p class="demo-loading">Loading the interactive repository…</p>
</div>

<noscript>
  JavaScript is required for the interactive workspace. The complete benchmark
  result remains available at <a href="/demo/data.json">/demo/data.json</a>.
</noscript>

<script src="/demo/demo.js" defer></script>

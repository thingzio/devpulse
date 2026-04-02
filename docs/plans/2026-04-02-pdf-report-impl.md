# PDF Report Generation — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a client-side "Download PDF" button that generates a single-repo report with all chart panels, tables, and insights.

**Architecture:** Purely client-side. jsPDF + autoTable loaded via CDN. A new `generatePDF()` function in app.js fetches all chart data into off-screen canvases, extracts them as PNG, and assembles a sectioned PDF. No server changes.

**Tech Stack:** jsPDF 2.5.2 (UMD), jspdf-autotable 3.8.4 (CDN), existing Chart.js 4.4.7, jQuery 3.6.0

---

### Task 1: Add CDN script tags for jsPDF and autoTable

**Files:**
- Modify: `pkg/server/templates/header.html:13` (after Chart.js CDN line)

**Step 1: Add script tags**

In `pkg/server/templates/header.html`, after line 13 (`chart.js` CDN), add:

```html
    <script src="https://cdn.jsdelivr.net/npm/jspdf@2.5.2/dist/jspdf.umd.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/jspdf-autotable@3.8.4/dist/jspdf.plugin.autotable.min.js"></script>
```

**Step 2: Verify manually**

Run: `make server`
Open browser console, type: `window.jspdf` — should be defined.

**Step 3: Commit**

```
feat(pdf): add jsPDF and autoTable CDN dependencies
```

---

### Task 2: Add "Download PDF" button to the top bar

**Files:**
- Modify: `pkg/server/templates/home.html:37` (after period-wrap div, before theme-toggle button)
- Modify: `pkg/server/static/css/app.css` (after `.period-wrap` styles, around line 255)

**Step 1: Add button HTML**

In `pkg/server/templates/home.html`, between the closing `</div>` of `period-wrap` (line 37) and the `<button class="theme-toggle-btn"` (line 38), insert:

```html
        <button class="pdf-btn" id="pdf-download" disabled aria-label="Download PDF report" title="Download PDF report">
            <svg class="pdf-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
                <polyline points="14 2 14 8 20 8"/>
                <line x1="12" y1="18" x2="12" y2="12"/>
                <polyline points="9 15 12 18 15 15"/>
            </svg>
            <span class="pdf-label">PDF</span>
        </button>
```

**Step 2: Add button CSS**

In `pkg/server/static/css/app.css`, after the `.period-wrap` block (around line 270), add:

```css
.pdf-btn {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 4px 10px;
    border: 1px solid var(--border-color);
    border-radius: 6px;
    background: var(--bg);
    color: var(--text);
    cursor: pointer;
    font-size: 0.8em;
    transition: opacity 0.2s;
}
.pdf-btn:disabled {
    opacity: 0.3;
    cursor: not-allowed;
}
.pdf-btn:not(:disabled):hover {
    background: var(--border-color);
}
.pdf-icon {
    width: 16px;
    height: 16px;
}
.pdf-btn .pdf-spinner {
    display: none;
    width: 14px;
    height: 14px;
    border: 2px solid var(--border-color);
    border-top-color: var(--text);
    border-radius: 50%;
    animation: pdf-spin 0.6s linear infinite;
}
.pdf-btn.loading .pdf-icon { display: none; }
.pdf-btn.loading .pdf-spinner { display: inline-block; }
@keyframes pdf-spin { to { transform: rotate(360deg); } }
```

**Step 3: Verify manually**

Run: `make server`
Navigate to home page — button should appear disabled next to the period selector.
Select a repo — button should still be disabled (we enable it in Task 4).

**Step 4: Commit**

```
feat(pdf): add Download PDF button to top bar
```

---

### Task 3: Add PDF generation scaffolding in app.js

**Files:**
- Modify: `pkg/server/static/js/app.js` — add `generatePDF()` function before the final `});` on line 2440

**Step 1: Add the core generatePDF function skeleton**

Before the final `});` closing the `$(document).on('click', ...)` block at line 2440, add the following top-level function:

```javascript
// --- PDF Report Generation ---

function generatePDF() {
    var org = searchCriteria.org || "";
    var repo = searchCriteria.repo || "";
    if (!repo) { return; }

    var months = $("#period_months").val();
    var q = 'm=' + months + '&o=' + org + '&r=' + repo;
    var btn = $("#pdf-download");

    // Add spinner element if not present
    if (!btn.find('.pdf-spinner').length) {
        btn.append('<span class="pdf-spinner"></span>');
    }
    btn.addClass("loading").prop("disabled", true);

    var PDF_W = 595.28;  // A4 points width
    var PDF_H = 841.89;  // A4 points height
    var MARGIN = 40;
    var CONTENT_W = PDF_W - 2 * MARGIN;
    var CHART_W = (CONTENT_W - 10) / 2;  // two-up with 10pt gap
    var CHART_H = CHART_W * 0.58;        // ~16:9 aspect
    var SECTION_GAP = 18;
    var CHART_GAP = 10;

    var doc = new jspdf.jsPDF({ unit: 'pt', format: 'a4' });
    var y = MARGIN;

    // --- Helper: add page if needed ---
    function ensureSpace(needed) {
        if (y + needed > PDF_H - MARGIN) {
            doc.addPage();
            y = MARGIN;
        }
    }

    // --- Helper: section header ---
    function sectionHeader(title) {
        ensureSpace(30);
        doc.setFontSize(14);
        doc.setFont(undefined, 'bold');
        doc.setTextColor(40, 40, 40);
        doc.text(title, MARGIN, y);
        y += 20;
        doc.setDrawColor(200, 200, 200);
        doc.line(MARGIN, y, PDF_W - MARGIN, y);
        y += 10;
    }

    // --- Helper: place chart image two-up ---
    function placeChart(imgData, col) {
        var x = MARGIN + col * (CHART_W + CHART_GAP);
        ensureSpace(CHART_H + 5);
        doc.addImage(imgData, 'PNG', x, y, CHART_W, CHART_H);
        if (col === 1) { y += CHART_H + CHART_GAP; }
    }

    // --- Helper: place full-width chart ---
    function placeFullWidthChart(imgData) {
        var h = CONTENT_W * 0.45;
        ensureSpace(h + 5);
        doc.addImage(imgData, 'PNG', MARGIN, y, CONTENT_W, h);
        y += h + CHART_GAP;
    }

    // --- Helper: render chart off-screen and return image data ---
    function renderChartOffScreen(width, height, chartFn) {
        return new Promise(function (resolve) {
            var container = document.createElement('div');
            container.style.cssText = 'position:absolute;left:-9999px;top:-9999px;';
            var canvas = document.createElement('canvas');
            canvas.width = width * 2;   // 2x for retina clarity
            canvas.height = height * 2;
            canvas.style.width = width + 'px';
            canvas.style.height = height + 'px';
            container.appendChild(canvas);
            document.body.appendChild(container);

            try {
                var chart = chartFn(canvas);
                if (chart && chart.update) {
                    chart.options.animation = false;
                    chart.options.responsive = false;
                    chart.resize(width * 2, height * 2);
                    chart.update('none');
                }
                setTimeout(function () {
                    var img = canvas.toDataURL('image/png');
                    if (chart && chart.destroy) { chart.destroy(); }
                    document.body.removeChild(container);
                    resolve(img);
                }, 100);
            } catch (e) {
                document.body.removeChild(container);
                resolve(null);
            }
        });
    }

    // --- Helper: fetch JSON ---
    function fetchJSON(url) {
        return $.get(url).then(function (data) { return data; })
            .fail(function () { return null; });
    }

    // --- Helper: add key-value text ---
    function addKeyValue(label, value) {
        ensureSpace(16);
        doc.setFontSize(9);
        doc.setFont(undefined, 'bold');
        doc.setTextColor(80, 80, 80);
        doc.text(label + ':', MARGIN, y);
        doc.setFont(undefined, 'normal');
        doc.setTextColor(40, 40, 40);
        doc.text(String(value || '\u2014'), MARGIN + doc.getTextWidth(label + ':  '), y);
        y += 14;
    }

    // --- Helper: add insight block ---
    function addInsightBlock(heading, items) {
        if (!items || items.length === 0) { return; }
        ensureSpace(20);
        doc.setFontSize(11);
        doc.setFont(undefined, 'bold');
        doc.setTextColor(60, 60, 60);
        doc.text(heading, MARGIN, y);
        y += 14;
        doc.setFontSize(9);
        doc.setFont(undefined, 'normal');
        doc.setTextColor(40, 40, 40);
        items.forEach(function (item) {
            ensureSpace(30);
            doc.setFont(undefined, 'bold');
            var lines = doc.splitTextToSize(item.headline || '', CONTENT_W);
            doc.text(lines, MARGIN + 8, y);
            y += lines.length * 11;
            doc.setFont(undefined, 'normal');
            var detail = doc.splitTextToSize(item.detail || '', CONTENT_W - 8);
            doc.text(detail, MARGIN + 8, y);
            y += detail.length * 11 + 6;
        });
    }

    // --- Build PDF ---
    // Report header
    doc.setFontSize(18);
    doc.setFont(undefined, 'bold');
    doc.setTextColor(30, 30, 30);
    doc.text(org + '/' + repo, MARGIN, y);
    y += 22;
    doc.setFontSize(10);
    doc.setFont(undefined, 'normal');
    doc.setTextColor(100, 100, 100);
    doc.text('Period: ' + months + ' months  |  Generated: ' + new Date().toLocaleDateString(), MARGIN, y);
    y += 20;
    doc.setDrawColor(180, 180, 180);
    doc.line(MARGIN, y, PDF_W - MARGIN, y);
    y += SECTION_GAP;

    // Fetch all data in parallel, then assemble PDF sequentially
    Promise.all([
        fetchJSON('/data/insights/summary?m=' + months + '&o=' + org + '&r=' + repo + '&e='),
        fetchJSON('/data/insights/repo-meta?o=' + org + '&r=' + repo),
        fetchJSON('/data/insights/daily-activity?' + q),
        fetchJSON('/data/insights/repo-metric-history?' + q),
        fetchJSON('/data/type?' + q),
        fetchJSON('/data/insights/pr-size?' + q),
        fetchJSON('/data/insights/forks-and-activity?' + q),
        fetchJSON('/data/insights/issue-ratio?' + q),
        fetchJSON('/data/insights/time-to-first-response?' + q),
        fetchJSON('/data/insights/time-to-merge?' + q),
        fetchJSON('/data/insights/change-failure-rate?' + q),
        fetchJSON('/data/insights/release-cadence?' + q),
        fetchJSON('/data/insights/release-downloads?m=' + months + '&o=' + org + '&r=' + repo),
        fetchJSON('/data/insights/release-downloads-by-tag?m=' + months + '&o=' + org + '&r=' + repo),
        fetchJSON('/data/insights/container-activity?' + q),
        fetchJSON('/data/insights/pr-ratio?' + q),
        fetchJSON('/data/insights/review-latency?' + q),
        fetchJSON('/data/insights/time-to-close?' + q),
        fetchJSON('/data/insights/reputation?' + q),
        fetchJSON('/data/insights/retention?' + q),
        fetchJSON('/data/insights/contributor-momentum?' + q),
        fetchJSON('/data/insights/contributor-funnel?' + q),
        fetchJSON('/data/entity?' + q),
        fetchJSON('/data/developer?' + q),
        fetchJSON('/data/insights/generated?o=' + org + '&r=' + repo),
        fetchJSON('/data/insights/time-to-restore?' + q)
    ]).then(function (results) {
        var summaryData = results[0];
        var repoMeta = results[1];
        var dailyActivity = results[2];
        var metricHistory = results[3];
        var timeEvents = results[4];
        var prSize = results[5];
        var forksActivity = results[6];
        var issueRatio = results[7];
        var timeToFirstResp = results[8];
        var timeToMerge = results[9];
        var changeFailRate = results[10];
        var releaseCadence = results[11];
        var releaseDownloads = results[12];
        var releaseByTag = results[13];
        var containerActivity = results[14];
        var prRatio = results[15];
        var reviewLatency = results[16];
        var timeToClose = results[17];
        var reputation = results[18];
        var retention = results[19];
        var momentum = results[20];
        var funnel = results[21];
        var entityData = results[22];
        var developerData = results[23];
        var insightsData = results[24];
        var timeToRestore = results[25];

        // ========== HEALTH SECTION ==========
        sectionHeader('Health');

        // Bus Factor & Pony Factor from summary
        if (summaryData) {
            addKeyValue('Bus Factor', summaryData.bus_factor);
            addKeyValue('Pony Factor', summaryData.pony_factor);
        }

        // Repo metadata
        if (repoMeta && repoMeta.length > 0) {
            var meta = repoMeta[0];
            addKeyValue('Stars', meta.stars);
            addKeyValue('Forks', meta.forks);
            addKeyValue('Open Issues', meta.open_issues);
            addKeyValue('Language', meta.language);
            addKeyValue('License', meta.license);
        }
        y += SECTION_GAP;

        // NOTE: Chart rendering for each section will be implemented in Task 4.
        // Each chart needs its own render function that creates a Chart.js instance
        // on an off-screen canvas and returns the image data.
        // This is the most complex part — Task 4 builds the chart render functions,
        // Task 5 wires them into this PDF assembly flow.

        // ========== ACTIVITY SECTION ==========
        sectionHeader('Activity');
        y += SECTION_GAP;

        // ========== VELOCITY SECTION ==========
        sectionHeader('Velocity');
        y += SECTION_GAP;

        // ========== QUALITY SECTION ==========
        sectionHeader('Quality');
        y += SECTION_GAP;

        // ========== COMMUNITY SECTION ==========
        sectionHeader('Community');
        y += SECTION_GAP;

        // ========== INSIGHTS SECTION ==========
        if (insightsData && insightsData.length > 0 && insightsData[0].insights) {
            sectionHeader('Insights');
            var insights = insightsData[0].insights;
            addInsightBlock('Observations', insights.observations);
            addInsightBlock('Recommended Actions', insights.actions);
        }

        // Footer on each page
        var pageCount = doc.internal.getNumberOfPages();
        for (var i = 1; i <= pageCount; i++) {
            doc.setPage(i);
            doc.setFontSize(8);
            doc.setTextColor(150, 150, 150);
            doc.text('Generated by DevPulse', MARGIN, PDF_H - 20);
            doc.text('Page ' + i + ' of ' + pageCount, PDF_W - MARGIN - 50, PDF_H - 20);
        }

        doc.save('devpulse-' + org + '-' + repo + '-' + new Date().toISOString().slice(0, 10) + '.pdf');
    }).catch(function (err) {
        console.error('PDF generation failed:', err);
        alert('PDF generation failed. Check console for details.');
    }).always(function () {
        btn.removeClass("loading").prop("disabled", false);
    });
}
```

**Step 2: Verify lint**

Run: `make lint`
Expected: PASS (no Go changes, but verify JS doesn't break embedded assets)

**Step 3: Commit**

```
feat(pdf): add generatePDF scaffolding with data fetch and text sections
```

---

### Task 4: Build off-screen chart rendering functions

**Files:**
- Modify: `pkg/server/static/js/app.js` — add chart-specific render functions before `generatePDF()`

This task adds functions that create Chart.js instances on off-screen canvases for PDF export. Each function takes raw API data and a canvas element, creates the chart with `animation: false`, and returns the Chart instance.

**Step 1: Add PDF chart render functions**

Add the following before the `generatePDF()` function. These mirror the existing chart creation logic in each `load*Chart()` function but accept a canvas element and data directly instead of fetching. Study each existing `load*Chart()` function for the exact Chart.js config (type, datasets, scales, colors) and replicate it in the corresponding `renderPdf*` function below.

The key pattern for each is:
1. Look at the existing `load*Chart()` function (e.g., `loadStarsTrendChart` at line ~1434)
2. Extract the Chart.js constructor call (`new Chart(ctx, { ... })`)
3. Create a `renderPdf*` version that takes `(canvas, data)` and returns the `Chart` instance
4. Override: `animation: false`, `responsive: false`, use light-theme colors (dark text on white)

**Important light-theme override:** All PDF chart renders must force these defaults regardless of current theme:
```javascript
var PDF_COLORS = {
    text: '#282828',
    grid: '#e0e0e0',
    bg: '#ffffff',
    accent: '#4a90d9',
    accent2: '#e55353',
    accent3: '#50c878',
    accent4: '#f5a623',
    muted: '#999999'
};
```

The exact chart configs should be copied from each existing `load*` function. For example, `loadStarsTrendChart` (line ~1434) creates a line chart — the PDF version should use the same dataset structure but with `PDF_COLORS` and `animation: false`.

**Functions to create** (one per chart type, each ~20-40 lines):
- `renderPdfLineChart(canvas, data, labelKey, valueKey, color)` — generic line chart (stars trend, forks trend, daily activity)
- `renderPdfBarChart(canvas, data, labelKey, valueKey, color)` — generic bar chart (release cadence, downloads)
- `renderPdfDoughnutChart(canvas, data, labelKey, valueKey)` — generic doughnut (PR ratio, issue ratio)
- `renderPdfStackedBarChart(canvas, data, config)` — for forks-and-activity, contributor funnel
- `renderPdfTimeSeriesChart(canvas, data)` — for time-based events
- `renderPdfHorizontalBarChart(canvas, data, labelKey, valueKey)` — for review latency, PR size

Where possible, reuse generic renderers. Only create specialized ones where the chart config is significantly different.

**Step 2: Verify lint**

Run: `make lint`

**Step 3: Commit**

```
feat(pdf): add off-screen chart rendering functions for PDF export
```

---

### Task 5: Wire chart renders into generatePDF assembly

**Files:**
- Modify: `pkg/server/static/js/app.js` — replace placeholder comments in `generatePDF()` with actual chart rendering calls

**Step 1: Replace section placeholders with chart rendering**

In the `generatePDF()` function's `.then()` callback, replace each section's placeholder comment with actual chart rendering using `renderChartOffScreen()` and the renderers from Task 4.

The pattern for each section:

```javascript
// Health section — after the key/value metadata
var chartPromises = [];

// Stars trend (left column)
if (metricHistory) {
    chartPromises.push(
        renderChartOffScreen(CHART_W * 2, CHART_H * 2, function(canvas) {
            return renderPdfLineChart(canvas, metricHistory, 'date', 'stars', PDF_COLORS.accent);
        }).then(function(img) { return { img: img, label: 'Stars Trend' }; })
    );
}
// Forks trend (right column)
// ... similar pattern for each chart
```

After all chart promises resolve, iterate and place them two-up:

```javascript
Promise.all(chartPromises).then(function(charts) {
    var col = 0;
    charts.forEach(function(c) {
        if (c.img) {
            // Add small label above chart
            ensureSpace(CHART_H + 20);
            doc.setFontSize(8);
            doc.setTextColor(100, 100, 100);
            var x = MARGIN + col * (CHART_W + CHART_GAP);
            doc.text(c.label, x, y);
            y += 10;
            placeChart(c.img, col);
            col = (col + 1) % 2;
        }
    });
    if (col === 1) { y += CHART_H + CHART_GAP; } // flush partial row
});
```

Since this is async, the entire assembly needs to be restructured as a promise chain: Health charts → Activity charts → Velocity charts → Quality charts → Community charts → Insights → Footer → save.

**Step 2: Test manually**

Run: `make server`
Navigate to a repo view, click "Download PDF".
Verify: PDF downloads with charts rendered in light theme, two-up layout, section headers.

**Step 3: Commit**

```
feat(pdf): wire chart rendering into PDF assembly pipeline
```

---

### Task 6: Enable/disable PDF button based on repo selection

**Files:**
- Modify: `pkg/server/static/js/app.js` — in `applySelection()` (line ~431) and the click handler

**Step 1: Enable button when repo is selected**

In `applySelection()` (line ~431), after `searchItem = item;` (line ~438), add:

```javascript
$("#pdf-download").prop("disabled", scope !== "repo");
```

Also add the click handler. Near the top of the `$(function() { ... })` ready block (around line 210), add:

```javascript
$("#pdf-download").on("click", function () {
    if (!$(this).prop("disabled")) {
        generatePDF();
    }
});
```

**Step 2: Disable when deselecting repo**

In the reset path (when user goes back to all-repos view), ensure the button is disabled. Find where `searchCriteria.reset()` is called and add:

```javascript
$("#pdf-download").prop("disabled", true);
```

**Step 3: Test manually**

1. Load page — button disabled
2. Select a repo — button enabled
3. Select an org — button disabled
4. Select a repo, click PDF — generates and downloads
5. Click PDF while generating — button stays disabled (no double-trigger)

**Step 4: Commit**

```
feat(pdf): enable PDF button on repo selection, add click handler
```

---

### Task 7: Add autoTable rendering for repo metadata table

**Files:**
- Modify: `pkg/server/static/js/app.js` — in `generatePDF()`, Health section

**Step 1: Replace key-value repo metadata with an autoTable**

In the Health section of `generatePDF()`, replace the individual `addKeyValue()` calls for repo metadata with:

```javascript
if (repoMeta && repoMeta.length > 0) {
    var meta = repoMeta[0];
    doc.autoTable({
        startY: y,
        margin: { left: MARGIN, right: MARGIN },
        head: [['Stars', 'Forks', 'Open Issues', 'Language', 'License']],
        body: [[
            String(meta.stars || 0),
            String(meta.forks || 0),
            String(meta.open_issues || 0),
            meta.language || '\u2014',
            meta.license || '\u2014'
        ]],
        styles: { fontSize: 9, cellPadding: 4 },
        headStyles: { fillColor: [74, 144, 217], textColor: 255 },
        theme: 'grid'
    });
    y = doc.lastAutoTable.finalY + 10;
}
```

**Step 2: Test manually**

Generate PDF — repo metadata should appear as a clean table in the Health section.

**Step 3: Commit**

```
feat(pdf): render repo metadata as autoTable in Health section
```

---

### Task 8: Final polish and edge cases

**Files:**
- Modify: `pkg/server/static/js/app.js`

**Step 1: Handle empty data gracefully**

For each chart render call, check if data is null/empty before attempting to render. The `renderChartOffScreen` wrapper already catches exceptions and returns null, but the chart-specific renderers should also guard:

```javascript
if (!data || (Array.isArray(data) && data.length === 0)) { resolve(null); return; }
```

**Step 2: Add "No data" placeholder for empty sections**

When all charts in a section are null, add a small text note:

```javascript
doc.setFontSize(9);
doc.setTextColor(150, 150, 150);
doc.text('No data available for this section.', MARGIN, y);
y += 14;
```

**Step 3: Test edge cases**

1. Repo with no releases — Velocity section should show available charts only
2. Repo with no insights — Insights section should be omitted entirely
3. Repo with no contributors scored — Community section should show available charts only

**Step 4: Run qualify**

Run: `make qualify`
Expected: PASS (no Go changes to break)

**Step 5: Commit**

```
fix(pdf): handle empty data gracefully with placeholder text
```

---

### Task 9: Final commit and verification

**Step 1: Full manual test**

1. `make server`
2. Navigate to a repo with good data
3. Click "Download PDF"
4. Verify all sections present with charts, tables, and insights
5. Verify light theme (dark text, white background)
6. Verify two-up chart layout
7. Verify page numbers and footer
8. Verify file name format: `devpulse-org-repo-YYYY-MM-DD.pdf`

**Step 2: Test with different repos**

- Repo with lots of data (many contributors, releases)
- Repo with minimal data (few events, no releases)
- Repo with insights
- Repo without insights

**Step 3: Run qualify**

Run: `make qualify`
Expected: PASS

**Step 4: Final commit if any cleanup needed**

```
chore(pdf): final cleanup and verification
```

# DevPulse Dashboard Guide

## Portfolio View

Shows when no repo is selected — your bird's-eye view across all tracked projects.

- **Portfolio Summary** — 10 KPI cards (orgs, repos, events, contributors, stars, forks, issues, PRs, avg merge time, last import). Quick pulse check across your entire portfolio.
- **Repository Overview** — Table of all repos with stars, forks, events, weekly activity, and contributor scoring status. Your triage surface for spotting repos that need attention.

## Health

Is this project alive and well?

- **Project Health** — Bus Factor (how many devs account for 50%+ of work), Pony Factor (how many orgs), and an Overall Health grade. High bus factor = key-person risk. Low pony factor = single-company dependency.
- **Health Scorecard** — Grades across three categories: Demand (community interest and growth), Throughput (how efficiently work moves), and Responsiveness (how quickly the team reacts). Each graded A–F.
- **Health Activity** — Daily contributor activity sparkline. Shows whether the project has steady engagement or sporadic bursts — sustained activity signals a healthy community.
- **Repository Status** — Stars, forks, open issues snapshot from GitHub. Lagging indicator but useful for tracking external interest.
- **Stars Trend** — Daily star count over time. Spikes often correlate with blog posts, HN/Reddit mentions, or conference talks.
- **Forks Trend** — Daily fork count. Rising forks without rising PRs may mean people are forking but not contributing back.

## Activity

What's actually happening in the repo?

- **Monthly Activity** — PRs, issues, reviews, comments, and forks with trend line. The single best chart for answering "is this project growing or declining?"
- **PR Size Distribution** — Small/Medium/Large/XL breakdown by lines changed. Large PRs are harder to review and more likely to introduce bugs — healthy projects skew small.
- **Forks & Activity** — Fork count overlaid with total events. When forks rise alongside activity, interest is converting to engagement. Forks without activity = window shoppers.
- **Issue Open/Close Ratio** — Opened vs closed over time. A growing gap means backlog is building — maintainers may be overwhelmed.

## Velocity

How fast does work move through the pipeline?

- **Time to First Response** — Hours until a PR gets its first review or an issue gets its first comment. The #1 signal for contributor experience — slow responses drive contributors away.
- **Lead Time (PR to Merge)** — Days from PR creation to merge. Tracks how efficiently the team processes contributions. Long lead times frustrate external contributors.
- **Change Failure Rate** — Percentage of releases followed by bug reports or reverts. Measures release quality — are you shipping fast but breaking things?
- **Release Cadence** — Releases over time, stable vs pre-release. Regular cadence signals a mature, predictable project. Irregular releases may indicate resource constraints.
- **Release Downloads** — Binary asset download counts. Direct measure of adoption for projects that ship binaries.
- **Downloads by Release** — Per-tag download counts. Shows which versions gained traction and whether adoption is growing.
- **Container Releases** — Container image versions via ghcr.io. Tracks CI/CD health for containerized projects.

## Quality

Is the code getting proper attention?

- **PR Review Ratio** — Reviews per PR. Higher = stronger review culture. Projects with low review ratios tend to accumulate technical debt faster.
- **Review Latency** — Hours from PR creation to first review. Complements Time to First Response — focused specifically on code review bottlenecks.
- **Time to Close (Issues)** — Average days to close all issues vs bug issues near releases. Shows whether the team can keep up with incoming work.
- **Lowest Reputation Contributors** — Flags contributors with limited track record. Useful for identifying PRs that may need extra review scrutiny.
- **Signals** — Quality alerts highlighting unanswered issues and PRs that may need attention.

## Community

Who's contributing and are they sticking around?

- **Contributor Retention** — New vs returning contributors. A healthy project converts newcomers into repeat contributors. High new-to-returning ratio with few returning = revolving door problem.
- **Contributor Momentum** — Rolling active count with period-over-period delta. The clearest signal of community growth or decline.
- **First-Time Contributors** — Tracks the newcomer funnel: first comment, first PR, first merge. Bottlenecks here mean your onboarding or review process needs work.
- **Top Entities** — Which companies are contributing. Critical for understanding organizational dependency and sponsorship diversity.
- **Top Collaborators** — Individual contributor rankings. Helps identify your most active community members and potential maintainers.
- **Contributor Profile** — Per-person metrics vs repo average. Useful for understanding individual contribution patterns.

## Events

Raw data exploration.

- **Event Search** — Filter and browse individual PRs, issues, reviews, comments, and forks. Drill down when you need specifics behind the charts.
- **Filters** — Narrow results by event type, date range, username, and entity (company). Filters can be combined.
- **Pagination** — Results are paged with Prev/Next navigation.
- **Export** — Download the current filtered view as a CSV file.

## Data Export

- **PDF report** — Downloadable report of the current dashboard view. Starter plans and above.
- **CSV/ZIP export** — ZIP archive with summary, events, developers, insights, and reputation data. Pro plans and above.
- **Portfolio CSV** — Export all repo metrics from the Repository Overview panel.
- **Event search CSV** — Export filtered search results from the Events tab.

## Insights (AI-powered, plan dependent)

Machine-generated analysis of your repo's health.

- **Key Observations** — AI summary of trends, risks, and notable patterns across your metrics. Saves time vs manually interpreting every chart.
- **Action Items** — Prioritized recommendations based on the data. Turns insights into next steps.

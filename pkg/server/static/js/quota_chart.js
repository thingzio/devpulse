(function() {
  var chart = null;
  var colors = ['#3b82f6','#ef4444','#22c55e','#eab308','#8b5cf6','#ec4899','#14b8a6','#f97316'];

  function fmtTime(iso) {
    var d = new Date(iso);
    var mo = d.toLocaleString('en', {month:'short'});
    var day = d.getDate();
    var hh = String(d.getHours()).padStart(2,'0');
    var mm = String(d.getMinutes()).padStart(2,'0');
    return mo + ' ' + day + ' ' + hh + ':' + mm;
  }

  function loadQuotaHistory(hours) {
    fetch('/admin/tokens/quota-history?hours=' + hours)
      .then(function(r) { return r.json(); })
      .then(function(samples) {
        if (!samples || samples.length === 0) {
          document.getElementById('quota-empty').style.display = 'block';
          if (chart) { chart.destroy(); chart = null; }
          return;
        }
        document.getElementById('quota-empty').style.display = 'none';

        var timeSet = {};
        samples.forEach(function(s) { timeSet[s.sampled_at] = true; });
        var times = Object.keys(timeSet).sort();
        var labels = times.map(fmtTime);

        var byLogin = {};
        samples.forEach(function(s) {
          if (!byLogin[s.login]) byLogin[s.login] = {};
          byLogin[s.login][s.sampled_at] = s.quota_limit > 0 ? Math.round(s.quota_used / s.quota_limit * 100) : 0;
        });

        var datasets = [];
        var ci = 0;
        Object.keys(byLogin).sort().forEach(function(login) {
          var pts = byLogin[login];
          datasets.push({
            label: login,
            data: times.map(function(t) { return pts[t] !== undefined ? pts[t] : null; }),
            borderColor: colors[ci % colors.length],
            backgroundColor: colors[ci % colors.length] + '22',
            borderWidth: 2,
            pointRadius: 3,
            tension: 0.3,
            fill: false,
            spanGaps: true
          });
          ci++;
        });

        var ctx = document.getElementById('quotaChart').getContext('2d');
        if (chart) chart.destroy();
        chart = new Chart(ctx, {
          type: 'line',
          data: { labels: labels, datasets: datasets },
          options: {
            responsive: true,
            maintainAspectRatio: false,
            interaction: { mode: 'index', intersect: false },
            scales: {
              x: {
                title: { display: true, text: 'Time' },
                grid: { color: 'rgba(128,128,128,0.15)' },
                ticks: { maxTicksLimit: 12 }
              },
              y: {
                min: 0, max: 100,
                title: { display: true, text: 'Utilization %' },
                grid: { color: 'rgba(128,128,128,0.15)' },
                ticks: { callback: function(v) { return v + '%'; } }
              }
            },
            plugins: {
              tooltip: {
                callbacks: {
                  label: function(tip) { return tip.dataset.label + ': ' + tip.parsed.y + '%'; }
                }
              },
              legend: { position: 'top' }
            }
          }
        });
      })
      .catch(function(err) {
        console.error('quota history fetch error:', err);
        document.getElementById('quota-empty').style.display = 'block';
      });
  }

  document.getElementById('quota-hours').addEventListener('change', function() {
    loadQuotaHistory(this.value);
  });

  loadQuotaHistory(24);
})();

(function() {
  var chart = null;

  function fmtTime(iso) {
    var d = new Date(iso);
    var mo = d.toLocaleString('en', {month:'short'});
    var day = d.getDate();
    var hh = String(d.getHours()).padStart(2,'0');
    var mm = String(d.getMinutes()).padStart(2,'0');
    return mo + ' ' + day + ' ' + hh + ':' + mm;
  }

  function comma(n) {
    return n.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
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

        // Aggregate per timestamp: sum used and limit across all installations.
        var byTime = {};
        samples.forEach(function(s) {
          if (!byTime[s.sampled_at]) {
            byTime[s.sampled_at] = { used: 0, limit: 0 };
          }
          byTime[s.sampled_at].used += s.quota_used;
          byTime[s.sampled_at].limit += s.quota_limit;
        });

        var times = Object.keys(byTime).sort();
        var labels = times.map(fmtTime);
        var usedData = times.map(function(t) { return byTime[t].used; });
        var limitData = times.map(function(t) { return byTime[t].limit; });
        var pctData = times.map(function(t) {
          return byTime[t].limit > 0 ? Math.round(byTime[t].used / byTime[t].limit * 100) : 0;
        });

        var ctx = document.getElementById('quotaChart').getContext('2d');
        if (chart) chart.destroy();
        chart = new Chart(ctx, {
          type: 'line',
          data: {
            labels: labels,
            datasets: [
              {
                label: 'Used',
                data: usedData,
                borderColor: '#3b82f6',
                backgroundColor: '#3b82f622',
                borderWidth: 2,
                pointRadius: 3,
                tension: 0.3,
                fill: true,
                yAxisID: 'y'
              },
              {
                label: 'Pool Limit',
                data: limitData,
                borderColor: '#6b7280',
                borderDash: [6, 3],
                borderWidth: 1,
                pointRadius: 0,
                tension: 0,
                fill: false,
                yAxisID: 'y'
              },
              {
                label: 'Utilization %',
                data: pctData,
                borderColor: '#ef4444',
                backgroundColor: '#ef444422',
                borderWidth: 2,
                pointRadius: 3,
                tension: 0.3,
                fill: false,
                yAxisID: 'y1'
              }
            ]
          },
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
                type: 'linear',
                position: 'left',
                min: 0,
                title: { display: true, text: 'Tokens' },
                grid: { color: 'rgba(128,128,128,0.15)' },
                ticks: { callback: function(v) { return comma(v); } }
              },
              y1: {
                type: 'linear',
                position: 'right',
                min: 0,
                max: 100,
                title: { display: true, text: 'Utilization %' },
                grid: { drawOnChartArea: false },
                ticks: { callback: function(v) { return v + '%'; } }
              }
            },
            plugins: {
              tooltip: {
                callbacks: {
                  label: function(tip) {
                    if (tip.dataset.yAxisID === 'y1') {
                      return tip.dataset.label + ': ' + tip.parsed.y + '%';
                    }
                    return tip.dataset.label + ': ' + comma(tip.parsed.y);
                  }
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

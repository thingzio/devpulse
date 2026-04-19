// Theme toggle — loaded on all pages via layout.html.
// Applies saved theme immediately (no flash) and wires up toggle buttons.
(function(){
  var key = 'theme';
  var saved = localStorage.getItem(key) || 'dark';
  document.documentElement.setAttribute('data-theme', saved);

  document.addEventListener('DOMContentLoaded', function(){
    var btns = document.querySelectorAll('.theme-btn[data-theme]');
    if (!btns.length) return;
    btns.forEach(function(b){
      b.classList.toggle('active', b.getAttribute('data-theme') === saved);
    });
    btns.forEach(function(b){
      b.addEventListener('click', function(){
        var t = this.getAttribute('data-theme');
        document.documentElement.setAttribute('data-theme', t);
        localStorage.setItem(key, t);
        btns.forEach(function(x){
          x.classList.toggle('active', x.getAttribute('data-theme') === t);
        });
      });
    });
  });
})();

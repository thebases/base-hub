// Parallax for [data-bh-parallax]: shifts the element's background as the page scrolls.
// Disabled for visitors who prefer reduced motion.
;(function () {
  "use strict"
  if (window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches) return
  var ticking = false
  function update() {
    ticking = false
    document.querySelectorAll("[data-bh-parallax]").forEach(function (el) {
      var r = el.getBoundingClientRect()
      if (r.bottom < 0 || r.top > window.innerHeight) return
      var shift = Math.round((r.top + r.height / 2 - window.innerHeight / 2) * -0.25)
      el.style.backgroundPosition = "center calc(50% + " + shift + "px)"
    })
  }
  function request() { if (!ticking) { ticking = true; requestAnimationFrame(update) } }
  window.addEventListener("scroll", request, { passive: true })
  window.addEventListener("resize", request)
  new MutationObserver(request).observe(document.body, { childList: true, subtree: true })
  request()
})()

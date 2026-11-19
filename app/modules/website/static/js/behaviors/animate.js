// Scroll reveal for [data-bh-animate="fade-in|slide-up|zoom-in"]. The hidden start state is applied
// only once this script runs (html.bh-js), so content is never invisible when scripts are blocked.
;(function () {
  "use strict"
  var root = document.documentElement
  root.classList.add("bh-js")
  var reduced = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches
  var seen = new WeakSet()
  var io = "IntersectionObserver" in window && !reduced ? new IntersectionObserver(function (entries) {
    entries.forEach(function (e) {
      if (!e.isIntersecting) return
      e.target.classList.add("bh-in")
      io.unobserve(e.target)
    })
  }, { threshold: 0.15 }) : null

  function scan() {
    document.querySelectorAll("[data-bh-animate]").forEach(function (el) {
      if (seen.has(el)) return
      seen.add(el)
      if (io) io.observe(el)
      else el.classList.add("bh-in")
    })
  }
  scan()
  // Pages are mounted by the router after load, so keep looking for new elements.
  new MutationObserver(scan).observe(document.body, { childList: true, subtree: true })
})()

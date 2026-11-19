// Countdown for [data-bh-countdown="<ISO date>"]: writes "Dd HH:MM:SS" into the element's
// [data-bh-countdown-out] (or the element itself) once a second, "00:00:00" when it has passed.
;(function () {
  "use strict"
  var timers = new WeakMap()
  function pad(n) { return (n < 10 ? "0" : "") + n }
  function render(el) {
    var target = Date.parse(el.getAttribute("data-bh-countdown"))
    var out = el.querySelector("[data-bh-countdown-out]") || el
    if (isNaN(target)) { out.textContent = ""; return }
    var s = Math.max(0, Math.floor((target - Date.now()) / 1000))
    var d = Math.floor(s / 86400)
    out.textContent = (d ? d + "d " : "") + pad(Math.floor(s % 86400 / 3600)) + ":" + pad(Math.floor(s % 3600 / 60)) + ":" + pad(s % 60)
  }
  function scan() {
    document.querySelectorAll("[data-bh-countdown]").forEach(function (el) {
      if (timers.has(el)) return
      render(el)
      timers.set(el, setInterval(function () {
        if (!document.body.contains(el)) { clearInterval(timers.get(el)); timers.delete(el); return }
        render(el)
      }, 1000))
    })
  }
  scan()
  new MutationObserver(scan).observe(document.body, { childList: true, subtree: true })
})()

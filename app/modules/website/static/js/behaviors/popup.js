// Popup / cookie bar for [data-bh-popup]: hidden until shown after data-bh-popup-delay seconds
// (default 1); a [data-bh-popup-close] button hides it and remembers that for the browser
// (localStorage key "bh-popup:<id>"). data-bh-popup is the popup id.
;(function () {
  "use strict"
  var done = new WeakSet()
  function key(el) { return "bh-popup:" + (el.getAttribute("data-bh-popup") || "default") }
  function dismissed(el) { try { return window.localStorage.getItem(key(el)) === "1" } catch (_) { return false } }
  function remember(el) { try { window.localStorage.setItem(key(el), "1") } catch (_) { /* storage blocked */ } }
  function scan() {
    document.querySelectorAll("[data-bh-popup]").forEach(function (el) {
      if (done.has(el)) return
      done.add(el)
      if (dismissed(el)) return
      var delay = parseFloat(el.getAttribute("data-bh-popup-delay")) || 1
      setTimeout(function () { el.classList.add("bh-popup-open") }, delay * 1000)
    })
  }
  document.addEventListener("click", function (ev) {
    var btn = ev.target && ev.target.closest ? ev.target.closest("[data-bh-popup-close]") : null
    var el = btn && btn.closest("[data-bh-popup]")
    if (!el) return
    el.classList.remove("bh-popup-open")
    remember(el)
  })
  scan()
  new MutationObserver(scan).observe(document.body, { childList: true, subtree: true })
})()

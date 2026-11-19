// Language selector for [data-bh-lang-switch]: lists the translations of the current page as links.
// The links come from main.js, which publishes them on <html data-bh-langs> after every navigation,
// so the selector always points at the same page in the other languages. Hidden when the page has
// no translation.
;(function () {
  "use strict"
  var root = document.documentElement
  function langs() {
    try { return JSON.parse(root.getAttribute("data-bh-langs") || "[]") } catch (_) { return [] }
  }
  function render(box) {
    // Rendering changes the DOM, which re-triggers the observers: only touch the box when its input changed.
    var signature = (root.getAttribute("data-bh-langs") || "[]") + "|" + (root.getAttribute("lang") || "")
    if (box.getAttribute("data-bh-rendered") === signature) return
    box.setAttribute("data-bh-rendered", signature)
    var list = langs()
    while (box.firstChild) box.removeChild(box.firstChild)
    box.hidden = list.length < 2
    var current = root.getAttribute("lang") || ""
    list.forEach(function (item) {
      var a = document.createElement("a")
      a.className = "btn btn-sm " + (item.lang === current ? "btn-primary" : "btn-outline-secondary") + " me-1"
      a.setAttribute("href", /^\/(?!\/)/.test(item.path) ? item.path : "#")
      a.setAttribute("hreflang", item.lang)
      a.textContent = String(item.lang).toUpperCase()
      box.appendChild(a)
    })
  }
  function scan() { document.querySelectorAll("[data-bh-lang-switch]").forEach(render) }
  scan()
  new MutationObserver(scan).observe(document.body, { childList: true, subtree: true })
  new MutationObserver(scan).observe(root, { attributes: true, attributeFilter: ["data-bh-langs", "lang"] })
})()

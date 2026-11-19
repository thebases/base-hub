// Header/footer behaviors, driven by marker attributes written by the header/footer templates:
//   [data-bh-header]     scroll effect, from the header's bh-effect-* class (standard | scroll | fixed | disappears | fade-out)
//   [data-bh-scrolltop]  smooth scroll to the top
//   form[data-bh-search] sends the query to the site's /search page
// Everything is delegated or read per event, so it works whenever the SPA mounts a page and needs no observer.
;(function () {
  "use strict"
  var ticking = false
  var lastY = window.pageYOffset || 0

  function effectOf(header) {
    var m = /(?:^|\s)bh-effect-([a-z-]+)(?:\s|$)/.exec(header.className || "")
    return m ? m[1] : "standard"
  }

  // Class writes are guarded so a scroll event never touches the DOM when nothing changed.
  function setClass(el, name, on) {
    if (el.classList.contains(name) !== on) el.classList.toggle(name, on)
  }

  function update() {
    ticking = false
    var y = window.pageYOffset || 0
    var down = y > lastY
    var headers = document.querySelectorAll("[data-bh-header]")
    for (var i = 0; i < headers.length; i++) {
      var h = headers[i]
      var height = h.offsetHeight || 1
      var effect = effectOf(h)
      setClass(h, "bh-header-scrolled", y > 0)
      // standard: reappears as soon as the visitor scrolls up; disappears: only back at the top.
      setClass(h, "bh-header-hidden", (effect === "standard" && down && y > height) || (effect === "disappears" && y > height))
      if (effect === "fade-out") {
        var opacity = Math.max(0, 1 - y / height)
        h.style.opacity = opacity >= 1 ? "" : String(opacity)
        h.style.pointerEvents = opacity <= 0 ? "none" : ""
      } else if (h.style.opacity) {
        h.style.opacity = ""
        h.style.pointerEvents = ""
      }
    }
    lastY = y
  }

  window.addEventListener("scroll", function () {
    if (!ticking) {
      ticking = true
      window.requestAnimationFrame(update)
    }
  }, { passive: true })

  document.addEventListener("click", function (ev) {
    var top = ev.target.closest && ev.target.closest("[data-bh-scrolltop]")
    if (!top) return
    ev.preventDefault()
    window.scrollTo({ top: 0, behavior: "smooth" })
  })

  document.addEventListener("submit", function (ev) {
    var form = ev.target
    if (!form.hasAttribute || !form.hasAttribute("data-bh-search")) return
    ev.preventDefault()
    var input = form.querySelector("input[name=q]")
    var q = input ? input.value.trim() : ""
    if (q) window.location.assign("/search?q=" + encodeURIComponent(q))
  })

  update()
})()

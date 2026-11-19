// Lightbox for images inside [data-bh-lightbox]: click opens the full image over the page,
// Escape or a click closes it.
;(function () {
  "use strict"
  function close() {
    var box = document.getElementById("bh-lightbox")
    if (box) box.remove()
  }
  document.addEventListener("click", function (ev) {
    var img = ev.target && ev.target.closest ? ev.target.closest("[data-bh-lightbox] img") : null
    if (!img) { if (ev.target && ev.target.id === "bh-lightbox") close(); return }
    ev.preventDefault()
    close()
    var box = document.createElement("div")
    box.id = "bh-lightbox"
    box.setAttribute("role", "dialog")
    var big = document.createElement("img")
    big.src = img.currentSrc || img.src
    big.alt = img.alt || ""
    box.appendChild(big)
    document.body.appendChild(box)
  })
  document.addEventListener("keydown", function (ev) { if (ev.key === "Escape") close() })
})()

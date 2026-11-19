// Data-source blocks: [data-bh-source="<key>"] (optional data-bh-limit) is filled with cards from
// GET /website/runtime/data/<source>/<limit>. Cards are built with textContent/setAttribute only, never innerHTML, and
// links are limited to site paths and http(s) addresses. The block's own content stays as the
// fallback when the request fails.
;(function () {
  "use strict"
  var done = new WeakSet()
  function safeUrl(u) { return /^(\/(?!\/)|https?:\/\/)/.test(u || "") ? u : "#" }
  function el(tag, cls, text) {
    var e = document.createElement(tag)
    if (cls) e.className = cls
    if (text) e.textContent = text
    return e
  }
  function card(item) {
    var col = el("div", "col-md-4")
    var box = el("div", "card h-100")
    if (item.image) {
      var img = el("img", "card-img-top")
      img.setAttribute("src", safeUrl(item.image))
      img.setAttribute("alt", "")
      box.appendChild(img)
    }
    var body = el("div", "card-body")
    var h = el("h5", "card-title")
    var a = el("a", "text-decoration-none", item.title)
    a.setAttribute("href", safeUrl(item.url))
    h.appendChild(a)
    body.appendChild(h)
    if (item.summary) body.appendChild(el("p", "card-text text-muted", item.summary))
    if (item.date) {
      var d = new Date(item.date)
      if (!isNaN(d.getTime())) body.appendChild(el("small", "text-muted", d.toLocaleDateString()))
    }
    box.appendChild(body)
    col.appendChild(box)
    return col
  }
  function load(block) {
    var out = block.querySelector("[data-bh-source-out]") || block
    var limit = parseInt(block.getAttribute("data-bh-limit"), 10) || 6
    fetch("/website/runtime/data/" + encodeURIComponent(block.getAttribute("data-bh-source")) + "/" + limit, { headers: { Accept: "application/json" } })
      .then(function (res) { return res.ok ? res.json() : null })
      .then(function (data) {
        if (!data || !data.items || !data.items.length) return
        var row = el("div", "row g-4")
        data.items.forEach(function (item) { row.appendChild(card(item)) })
        while (out.firstChild) out.removeChild(out.firstChild)
        out.appendChild(row)
      })
      .catch(function () { /* keep the fallback content */ })
  }
  function scan() {
    document.querySelectorAll("[data-bh-source]").forEach(function (block) {
      if (done.has(block)) return
      done.add(block)
      load(block)
    })
  }
  scan()
  new MutationObserver(scan).observe(document.body, { childList: true, subtree: true })
})()

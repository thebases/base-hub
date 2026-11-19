// Contact-us page runtime: submits `form[data-site-contact-form]` to the anonymous
// POST /website/runtime/contact endpoint (tbl.website_contact_request.submit_public) and shows the
// result in `[data-site-contact-status]`. Delegated, so it works however late Owl mounts the page.
;(function () {
  "use strict"
  var MESSAGES = {
    en: { sent: "Thank you! Your request was sent.", invalid: "Please fill in your name, a valid email and a message.", limit: "Too many requests. Please try again later.", error: "Could not send your request. Please try again." },
    vi: { sent: "Cảm ơn bạn! Yêu cầu đã được gửi.", invalid: "Vui lòng nhập họ tên, email hợp lệ và nội dung.", limit: "Quá nhiều yêu cầu. Vui lòng thử lại sau.", error: "Không thể gửi yêu cầu. Vui lòng thử lại." }
  }

  function text(key) {
    var lang = document.documentElement.getAttribute("lang") === "vi" ? "vi" : "en"
    return MESSAGES[lang][key]
  }

  function show(form, kind, key) {
    var box = form.querySelector("[data-site-contact-status]")
    if (!box) return
    box.className = "mt-3 alert alert-" + kind
    box.textContent = text(key)
  }

  document.addEventListener("submit", function (ev) {
    var form = ev.target && ev.target.closest ? ev.target.closest("form[data-site-contact-form]") : null
    if (!form) return
    ev.preventDefault()
    if (form.getAttribute("data-sending") === "1") return
    var data = {}
    new FormData(form).forEach(function (value, key) { data[key] = String(value) })
    if (!data.name || !data.email || !data.message) return show(form, "warning", "invalid")
    form.setAttribute("data-sending", "1")
    fetch("/website/runtime/contact", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data)
    }).then(function (res) {
      if (res.ok) { form.reset(); return show(form, "success", "sent") }
      show(form, res.status === 429 ? "warning" : res.status === 400 ? "warning" : "danger", res.status === 429 ? "limit" : res.status === 400 ? "invalid" : "error")
    }).catch(function () {
      show(form, "danger", "error")
    }).then(function () {
      form.removeAttribute("data-sending")
    })
  })
})()

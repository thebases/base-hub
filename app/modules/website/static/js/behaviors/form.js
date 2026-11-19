// Form blocks: submits form[data-bh-form="<key>"] as JSON to POST /website/runtime/form.
// Optional attributes: data-bh-form-success (message), data-bh-form-redirect (same-site path).
// Status is written to [data-bh-form-status]. Delegated, so it works however late the page mounts.
;(function () {
  "use strict"
  function status(form, kind, text) {
    var box = form.querySelector("[data-bh-form-status]")
    if (!box) return
    box.className = "mt-3 alert alert-" + kind
    box.textContent = text
  }
  function collect(form) {
    var fields = {}
    Array.prototype.forEach.call(form.elements, function (el) {
      if (!el.name || el.disabled) return
      if ((el.type === "checkbox" || el.type === "radio") && !el.checked) return
      fields[el.name] = el.type === "checkbox" ? true : el.value
    })
    return fields
  }
  document.addEventListener("submit", function (ev) {
    var form = ev.target && ev.target.closest ? ev.target.closest("form[data-bh-form]") : null
    if (!form) return
    ev.preventDefault()
    if (form.getAttribute("data-sending") === "1") return
    if (form.checkValidity && !form.checkValidity()) { form.reportValidity(); return }
    var fields = collect(form)
    var trap = fields.website_url || ""
    delete fields.website_url
    form.setAttribute("data-sending", "1")
    fetch("/website/runtime/form", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ form: form.getAttribute("data-bh-form"), fields: fields, website_url: trap })
    }).then(function (res) {
      if (!res.ok) return status(form, res.status === 429 ? "warning" : res.status === 400 ? "warning" : "danger",
        res.status === 429 ? "Too many requests. Please try again later." : res.status === 400 ? "Please check the form and try again." : "Could not send. Please try again.")
      form.reset()
      var to = form.getAttribute("data-bh-form-redirect")
      if (to && /^\/(?!\/)/.test(to)) { window.location.assign(to); return }
      status(form, "success", form.getAttribute("data-bh-form-success") || "Thank you! Your message was sent.")
    }).catch(function () {
      status(form, "danger", "Could not send. Please try again.")
    }).then(function () {
      form.removeAttribute("data-sending")
    })
  })
})()

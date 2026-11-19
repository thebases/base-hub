// Site chrome runtime: theme toggle + language switch for the seeded header.
// Delegated listeners, so it works regardless of when Owl mounts the page.
;(function () {
  "use strict"
  var THEME_KEY = "website:theme"
  var LANG_KEY = "website:lang"
  var root = document.documentElement

  function read(key) {
    try { return window.localStorage.getItem(key) } catch (_) { return null }
  }
  function write(key, value) {
    try { window.localStorage.setItem(key, value) } catch (_) { /* storage blocked */ }
  }

  var ICONS = {
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
    moon: '<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/>',
    globe: '<circle cx="12" cy="12" r="10"/><path d="M2 12h20M12 2a15 15 0 0 1 0 20M12 2a15 15 0 0 0 0 20"/>'
  }

  // Icon-only buttons; skip the write when unchanged (called from a MutationObserver).
  function setIcon(btn, name) {
    if (!btn || btn.getAttribute("data-icon") === name) return
    btn.setAttribute("data-icon", name)
    btn.innerHTML = '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" ' +
      'stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + ICONS[name] + "</svg>"
  }

  function currentTheme() {
    var saved = read(THEME_KEY)
    if (saved === "light" || saved === "dark") return saved
    return window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light"
  }

  function applyTheme(theme) {
    root.setAttribute("data-bs-theme", theme)
    setIcon(document.querySelector("[data-site-theme-toggle]"), theme === "dark" ? "sun" : "moon")
    setIcon(document.getElementById("site-lang-current"), "globe")
  }

  // Elements carry the default (English) text; data-i18n-<lang> overrides it.
  // When the page has real translations (main.js lists them on <html data-bh-langs>), the route decides
  // the language and this script follows it; otherwise the visitor's saved choice applies.
  function routeHasLangs() {
    try { return JSON.parse(root.getAttribute("data-bh-langs") || "[]").length > 1 } catch (_) { return false }
  }

  function applyLang(lang) {
    if (!routeHasLangs()) root.setAttribute("lang", lang)
    var nodes = document.querySelectorAll("[data-i18n-vi]")
    for (var i = 0; i < nodes.length; i++) {
      var node = nodes[i]
      if (node.getAttribute("data-i18n-en") === null) node.setAttribute("data-i18n-en", node.textContent)
      var text = node.getAttribute("data-i18n-" + lang) || node.getAttribute("data-i18n-en")
      // Only write on change: this runs from a MutationObserver, so a no-op write would loop.
      if (node.textContent !== text) node.textContent = text
    }
    var links = document.querySelectorAll("[data-site-lang]")
    for (var j = 0; j < links.length; j++) {
      var on = links[j].getAttribute("data-site-lang") === lang
      if (links[j].classList.contains("active") !== on) links[j].classList.toggle("active", on)
    }
  }

  function currentLang() {
    if (routeHasLangs()) return root.getAttribute("lang") || "en"
    var saved = read(LANG_KEY)
    return saved === "vi" || saved === "en" ? saved : "en"
  }

  document.addEventListener("click", function (ev) {
    var target = ev.target && ev.target.closest ? ev.target : null
    if (!target) return
    if (target.closest("[data-site-theme-toggle]")) {
      var next = currentTheme() === "dark" ? "light" : "dark"
      write(THEME_KEY, next)
      applyTheme(next)
      return
    }
    var langLink = target.closest("[data-site-lang]")
    if (langLink) {
      ev.preventDefault()
      var lang = langLink.getAttribute("data-site-lang")
      write(LANG_KEY, lang)
      applyLang(lang)
    }
  })

  // Pages mount asynchronously (Owl), so re-apply when the app root changes.
  function init() {
    applyTheme(currentTheme())
    var appRoot = document.getElementById("app")
    if (!appRoot || !window.MutationObserver) return applyLang(currentLang())
    var pending = false
    new MutationObserver(function () {
      if (pending) return
      pending = true
      window.requestAnimationFrame(function () {
        pending = false
        applyTheme(currentTheme())
        applyLang(currentLang())
      })
    }).observe(appRoot, { childList: true, subtree: true })
    applyLang(currentLang())
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init)
  else init()
})()

// B0 block catalog: three core blocks on BaseUI/Bootstrap utility classes. Full catalog (25 blocks)
// ships as Owl XML templates under modules/<m>/static/snippets; same shape: { key, name, group, keywords, html }.
export const CORE_SNIPPETS = [
  {
    key: "title",
    name: "Title",
    group: "text",
    keywords: ["heading", "h1"],
    html: '<section class="bh-title py-4" data-snippet="title"><div class="container"><h2>Section title</h2><p class="text-muted">Subtitle goes here.</p></div></section>',
  },
  {
    key: "text-image",
    name: "Text - Image",
    group: "content",
    keywords: ["image", "picture", "text"],
    html: '<section class="bh-text-image py-5" data-snippet="text-image"><div class="container"><div class="row align-items-center"><div class="col-lg-6"><h2>Tell your story</h2><p>Write a few sentences about your product or service.</p></div><div class="col-lg-6"><img class="img-fluid rounded" src="data:image/svg+xml;utf8,%3Csvg xmlns=%27http://www.w3.org/2000/svg%27 width=%27600%27 height=%27400%27%3E%3Crect width=%27600%27 height=%27400%27 fill=%27%23dee2e6%27/%3E%3C/svg%3E" alt=""/></div></div></div></section>',
  },
  {
    key: "cta",
    name: "Call to action",
    group: "content",
    keywords: ["button", "action"],
    html: '<section class="bh-cta py-5 text-center bg-light" data-snippet="cta"><div class="container"><h2>Ready to start?</h2><a class="btn btn-primary btn-lg" href="/contact">Contact us</a></div></section>',
  },
];

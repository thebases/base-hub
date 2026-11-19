---
name: website-blog
version: 0.1.0
category: Website
installable: true
depends: [base, website]
auto_install: false
frontend_widgets: {}
---

# Website Blog

CMS blogging on top of the `website` module.

- Blogs, posts, tags and tag groups with admin list/form/search/kanban screens.
- Editorial workflow: draft, published (a future publish date schedules the post), archived, plus duplicate.
- Per-post SEO (title, description, keywords, canonical, noindex), cover image, teaser, featured pin,
  read-time estimate and visit counter. Post HTML is sanitized to an editorial allowlist.
- Public server-rendered pages: `/blog`, `/blog/<blog>`, `/blog/<blog>/<post>`, tag filter (`?tag=`),
  search (`?q=`), pagination, related posts, RSS (`/blog/feed.xml`) and `/blog/sitemap.xml`,
  styled with the active site's published bootstrap and `styles.css`.

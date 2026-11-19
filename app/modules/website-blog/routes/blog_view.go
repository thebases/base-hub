package routes

// File overview:
// - Responsibility: server-side HTML/RSS/sitemap rendering for the public blog. Pages reuse the
//   published site's bootstrap and styles.css when present so they match the website's look.
// - Key symbols: `renderListPage`, `renderPostPage`, `renderFeed`, `renderSitemap`.

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html/template"
	"net/url"
	"strconv"
	"time"

	"git.thebase.vn/base_hub/modules/website-blog/models"
)

type pageChrome struct {
	SiteName     string
	BootstrapURL string
	StylesURL    string // empty when the site has no published styles.css
	BaseURL      string
}

type listPageData struct {
	Chrome      pageChrome
	Title       string
	Description string
	Canonical   string
	Heading     string
	Subheading  string
	CoverURL    string
	Blogs       []blogInfo
	ActiveBlog  string
	Posts       []blogPost
	TagCloud    []tagGroup
	ActiveTag   string
	Query       string
	Page, Pages int
	PrevURL     string
	NextURL     string
	FeedURL     string
}

type tagGroup struct {
	Name string
	Tags []tagInfo
}

type postPageData struct {
	Chrome    pageChrome
	Post      blogPost
	Blog      blogInfo
	Title     string
	Desc      string
	Canonical string
	Body      template.HTML
	Related   []blogPost
	JSONLD    template.JS
	Robots    string
}

var blogFuncs = template.FuncMap{
	"date": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format("Jan 2, 2006")
	},
	"iso": func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
	"postURL": func(p blogPost) string {
		return "/blog/" + url.PathEscape(p.BlogSlug) + "/" + url.PathEscape(p.Slug)
	},
	"tagURL": func(blogSlug, tag string) string {
		base := "/blog"
		if blogSlug != "" {
			base += "/" + url.PathEscape(blogSlug)
		}
		return base + "?tag=" + url.QueryEscape(tag)
	},
	"blogURL": func(slug string) string { return "/blog/" + url.PathEscape(slug) },
	"excerpt": func(p blogPost) string {
		if p.Teaser != "" {
			return p.Teaser
		}
		return models.BlogPlainText(p.Content)
	},
}

const blogHeadTpl = `{{define "head"}}<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="stylesheet" href="{{.BootstrapURL}}">{{if .StylesURL}}<link rel="stylesheet" href="{{.StylesURL}}">{{end}}{{end}}
{{define "nav"}}<nav class="navbar navbar-expand border-bottom mb-4"><div class="container"><a class="navbar-brand" href="/">{{.SiteName}}</a><a class="nav-link" href="/blog">Blog</a></div></nav>{{end}}`

const listTpl = blogHeadTpl + `<!doctype html><html lang="en"><head>{{template "head" .Chrome}}
<title>{{.Title}}</title><meta name="description" content="{{.Description}}"><link rel="canonical" href="{{.Canonical}}">
<link rel="alternate" type="application/rss+xml" title="{{.Heading}}" href="{{.FeedURL}}">
<meta property="og:type" content="website"><meta property="og:title" content="{{.Title}}"><meta property="og:description" content="{{.Description}}">{{if .CoverURL}}<meta property="og:image" content="{{.CoverURL}}">{{end}}
</head><body>{{template "nav" .Chrome}}
<main class="container pb-5">
<header class="mb-4"><h1>{{.Heading}}</h1>{{if .Subheading}}<p class="lead text-body-secondary">{{.Subheading}}</p>{{end}}</header>
{{if .Blogs}}<ul class="nav nav-pills mb-4"><li class="nav-item"><a class="nav-link {{if eq .ActiveBlog ""}}active{{end}}" href="/blog">All</a></li>{{range .Blogs}}<li class="nav-item"><a class="nav-link {{if eq $.ActiveBlog .Slug}}active{{end}}" href="{{blogURL .Slug}}">{{.Name}}</a></li>{{end}}</ul>{{end}}
<div class="row g-4"><div class="col-lg-8">
{{range .Posts}}<article class="card mb-4">{{if .CoverURL}}<img class="card-img-top" src="{{.CoverURL}}" alt="" loading="lazy">{{end}}
<div class="card-body"><h2 class="h4 card-title"><a class="text-decoration-none" href="{{postURL .}}">{{.Title}}</a></h2>
{{if .Subtitle}}<p class="text-body-secondary mb-2">{{.Subtitle}}</p>{{end}}
<p class="small text-body-secondary mb-2"><time datetime="{{iso .PublishedAt}}">{{date .PublishedAt}}</time>{{if .Author}} · {{.Author}}{{end}}{{if .ReadMinutes}} · {{.ReadMinutes}} min read{{end}}{{if .BlogName}} · {{.BlogName}}{{end}}</p>
<p class="card-text">{{excerpt .}}</p>
{{range .Tags}}<a class="badge text-bg-light text-decoration-none me-1" href="{{tagURL "" .Slug}}">#{{.Name}}</a>{{end}}</div></article>
{{else}}<p class="text-body-secondary">No posts found.</p>{{end}}
{{if gt .Pages 1}}<nav class="d-flex justify-content-between">{{if .PrevURL}}<a class="btn btn-outline-secondary" href="{{.PrevURL}}">&larr; Newer</a>{{else}}<span></span>{{end}}<span class="align-self-center small">Page {{.Page}} / {{.Pages}}</span>{{if .NextURL}}<a class="btn btn-outline-secondary" href="{{.NextURL}}">Older &rarr;</a>{{else}}<span></span>{{end}}</nav>{{end}}
</div><aside class="col-lg-4">
<form class="mb-4" method="get" action=""><div class="input-group"><input class="form-control" type="search" name="q" value="{{.Query}}" placeholder="Search posts"><button class="btn btn-primary" type="submit">Search</button></div>{{if .ActiveTag}}<input type="hidden" name="tag" value="{{.ActiveTag}}">{{end}}</form>
{{range .TagCloud}}<div class="mb-3">{{if .Name}}<h2 class="h6 text-uppercase text-body-secondary">{{.Name}}</h2>{{else}}<h2 class="h6 text-uppercase text-body-secondary">Tags</h2>{{end}}
{{range .Tags}}<a class="badge text-bg-light text-decoration-none me-1 mb-1" href="{{tagURL "" .Slug}}">#{{.Name}} ({{.Count}})</a>{{end}}</div>{{end}}
<a class="small" href="{{.FeedURL}}">RSS feed</a>
</aside></div></main></body></html>`

const postTpl = blogHeadTpl + `<!doctype html><html lang="en"><head>{{template "head" .Chrome}}
<title>{{.Title}}</title><meta name="description" content="{{.Desc}}">{{if .Post.MetaKeywords}}<meta name="keywords" content="{{.Post.MetaKeywords}}">{{end}}
<meta name="robots" content="{{.Robots}}"><link rel="canonical" href="{{.Canonical}}">
<meta property="og:type" content="article"><meta property="og:title" content="{{.Title}}"><meta property="og:description" content="{{.Desc}}"><meta property="og:url" content="{{.Canonical}}">{{if .Post.CoverURL}}<meta property="og:image" content="{{.Post.CoverURL}}">{{end}}
<meta property="article:published_time" content="{{iso .Post.PublishedAt}}">
<meta name="twitter:card" content="summary_large_image">
<script type="application/ld+json">{{.JSONLD}}</script></head><body>{{template "nav" .Chrome}}
<main class="container pb-5"><article class="mx-auto" style="max-width:48rem">
<p class="small"><a href="{{blogURL .Blog.Slug}}">&larr; {{.Blog.Name}}</a></p>
<h1>{{.Post.Title}}</h1>{{if .Post.Subtitle}}<p class="lead text-body-secondary">{{.Post.Subtitle}}</p>{{end}}
<p class="small text-body-secondary"><time datetime="{{iso .Post.PublishedAt}}">{{date .Post.PublishedAt}}</time>{{if .Post.Author}} · {{.Post.Author}}{{end}}{{if .Post.ReadMinutes}} · {{.Post.ReadMinutes}} min read{{end}}</p>
{{if .Post.CoverURL}}<img class="img-fluid rounded mb-4" src="{{.Post.CoverURL}}" alt="">{{end}}
<div class="blog-content">{{.Body}}</div>
<div class="mt-4">{{range .Post.Tags}}<a class="badge text-bg-light text-decoration-none me-1" href="{{tagURL $.Blog.Slug .Slug}}">#{{.Name}}</a>{{end}}</div>
{{if .Related}}<hr class="my-5"><h2 class="h5">Related posts</h2><ul>{{range .Related}}<li><a href="{{postURL .}}">{{.Title}}</a></li>{{end}}</ul>{{end}}
</article></main></body></html>`

var (
	listTemplate = template.Must(template.New("list").Funcs(blogFuncs).Parse(listTpl))
	postTemplate = template.Must(template.New("post").Funcs(blogFuncs).Parse(postTpl))
)

func renderTemplate(t *template.Template, data any) ([]byte, error) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func renderListPage(data listPageData) ([]byte, error) { return renderTemplate(listTemplate, data) }

func renderPostPage(data postPageData) ([]byte, error) {
	ld := map[string]any{
		"@context": "https://schema.org", "@type": "BlogPosting", "headline": data.Post.Title,
		"datePublished": data.Post.PublishedAt.UTC().Format(time.RFC3339), "dateModified": data.Post.UpdatedAt.UTC().Format(time.RFC3339),
		"mainEntityOfPage": data.Canonical, "description": data.Desc,
	}
	if data.Post.UpdatedAt.IsZero() {
		ld["dateModified"] = ld["datePublished"]
	}
	if data.Post.Author != "" {
		ld["author"] = map[string]any{"@type": "Person", "name": data.Post.Author}
	}
	if data.Post.CoverURL != "" {
		ld["image"] = data.Post.CoverURL
	}
	encoded, err := json.Marshal(ld)
	if err != nil {
		return nil, err
	}
	// html/template JS-escapes a template.JS value as already safe, so the marshalled JSON (which
	// escapes <, > and & itself) is embedded verbatim.
	data.JSONLD = template.JS(encoded)
	return renderTemplate(postTemplate, data)
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Author      string `xml:"author,omitempty"`
	Description string `xml:"description"`
}

type rssFeed struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel struct {
		Title         string    `xml:"title"`
		Link          string    `xml:"link"`
		Description   string    `xml:"description"`
		LastBuildDate string    `xml:"lastBuildDate"`
		Items         []rssItem `xml:"item"`
	} `xml:"channel"`
}

// renderFeed builds RSS 2.0 for the newest 30 posts.
func renderFeed(chrome pageChrome, title, description, selfPath string, posts []blogPost) ([]byte, error) {
	feed := rssFeed{Version: "2.0"}
	feed.Channel.Title = title
	feed.Channel.Link = chrome.BaseURL + selfPath
	feed.Channel.Description = description
	feed.Channel.LastBuildDate = time.Now().UTC().Format(time.RFC1123Z)
	for i, p := range posts {
		if i == 30 {
			break
		}
		link := chrome.BaseURL + blogFuncs["postURL"].(func(blogPost) string)(p)
		item := rssItem{Title: p.Title, Link: link, GUID: link, PubDate: p.PublishedAt.UTC().Format(time.RFC1123Z), Author: p.Author}
		item.Description = blogFuncs["excerpt"].(func(blogPost) string)(p)
		feed.Channel.Items = append(feed.Channel.Items, item)
	}
	body, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
}

// renderSitemap lists the blog indexes and every indexable post.
func renderSitemap(chrome pageChrome, blogs []blogInfo, posts []blogPost) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	buf.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	write := func(loc string, mod time.Time) error {
		var esc bytes.Buffer
		if err := xml.EscapeText(&esc, []byte(loc)); err != nil {
			return err
		}
		fmt.Fprintf(&buf, "  <url><loc>%s</loc>", esc.String())
		if !mod.IsZero() {
			fmt.Fprintf(&buf, "<lastmod>%s</lastmod>", mod.UTC().Format("2006-01-02"))
		}
		buf.WriteString("</url>\n")
		return nil
	}
	if err := write(chrome.BaseURL+"/blog", time.Time{}); err != nil {
		return nil, err
	}
	for _, b := range blogs {
		if err := write(chrome.BaseURL+"/blog/"+url.PathEscape(b.Slug), time.Time{}); err != nil {
			return nil, err
		}
	}
	for _, p := range posts {
		if p.NoIndex {
			continue
		}
		mod := p.UpdatedAt
		if mod.IsZero() {
			mod = p.PublishedAt
		}
		if err := write(chrome.BaseURL+blogFuncs["postURL"].(func(blogPost) string)(p), mod); err != nil {
			return nil, err
		}
	}
	buf.WriteString("</urlset>\n")
	return buf.Bytes(), nil
}

func pageURL(base string, page int, tag, q string) string {
	values := url.Values{}
	if tag != "" {
		values.Set("tag", tag)
	}
	if q != "" {
		values.Set("q", q)
	}
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	if encoded := values.Encode(); encoded != "" {
		return base + "?" + encoded
	}
	return base
}

package routes

import (
	"strings"
	"testing"
	"time"
)

func samplePosts(n int) []blogPost {
	posts := make([]blogPost, n)
	for i := range posts {
		posts[i] = blogPost{ID: int64(i + 1), Title: "Post", Slug: "post", BlogSlug: "news", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	}
	return posts
}

func TestPageBoundsClampsPages(t *testing.T) {
	cases := []struct{ total, page, wantPage, wantPages, wantOffset int }{
		{25, 99, 3, 3, 20},
		{25, -4, 1, 3, 0},
		{25, 2, 2, 3, 10},
		{0, 1, 1, 1, 0},
	}
	for _, tc := range cases {
		page, pages, offset := pageBounds(tc.total, tc.page, 10)
		if page != tc.wantPage || pages != tc.wantPages || offset != tc.wantOffset {
			t.Fatalf("pageBounds(%d,%d) = %d,%d,%d", tc.total, tc.page, page, pages, offset)
		}
	}
}

func TestPostFiltersAndIDHelpers(t *testing.T) {
	if postSearchFilter("  ") != nil {
		t.Fatal("empty search must add no condition")
	}
	if search := postSearchFilter("go"); len(search) != 1 || len(search[0].Children) != 3 {
		t.Fatalf("search filter = %+v", search)
	}
	posts := []blogPost{{ID: 1, TagIDs: []int64{1}}, {ID: 2, TagIDs: []int64{2}}, {ID: 3, TagIDs: []int64{1, 2}}}
	if got := postIDsWithTag(posts, 2); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("postIDsWithTag = %v", got)
	}
	if got := postIDsWithTag(posts, 9); got == nil || len(got) != 0 {
		t.Fatalf("no match must be an empty (not nil) list so the id filter matches nothing: %v", got)
	}
	if got := orderPostsByIDs(posts, []int64{3, 9, 1}); len(got) != 2 || got[0].ID != 3 || got[1].ID != 1 {
		t.Fatalf("orderPostsByIDs = %+v", got)
	}
}

func TestRelatedPostsRanksBySharedTags(t *testing.T) {
	current := blogPost{ID: 1, TagIDs: []int64{1, 2}}
	all := []blogPost{current, {ID: 2, TagIDs: []int64{1}}, {ID: 3, TagIDs: []int64{1, 2}}, {ID: 4, TagIDs: []int64{9}}}
	got := relatedPosts(all, current, 3)
	if len(got) != 2 || got[0].ID != 3 || got[1].ID != 2 {
		t.Fatalf("related = %+v", got)
	}
}

func TestRenderPostPageEscapesTitleAndKeepsBody(t *testing.T) {
	post := blogPost{Title: `<img src=x onerror=1>`, Slug: "p", BlogSlug: "news", Tags: nil, PublishedAt: time.Now()}
	out, err := renderPostPage(postPageData{
		Chrome: pageChrome{SiteName: "S", BootstrapURL: "/b.css"}, Post: post, Blog: blogInfo{Name: "News", Slug: "news"},
		Title: post.Title, Canonical: "https://x.test/blog/news/p", Body: postBodyHTML(`<p>ok</p><script>x()</script>`), Robots: "index,follow",
	})
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)
	if strings.Contains(html, "<img src=x") || strings.Contains(html, "<script>x()") {
		t.Fatalf("unescaped content leaked: %s", html)
	}
	if !strings.Contains(html, "<p>ok</p>") || !strings.Contains(html, `"@type":"BlogPosting"`) {
		t.Fatalf("expected body and JSON-LD: %s", html)
	}
}

func TestRenderListPageAndFeed(t *testing.T) {
	posts := samplePosts(2)
	out, err := renderListPage(listPageData{Chrome: pageChrome{SiteName: "S", BootstrapURL: "/b.css"}, Heading: "Blog", Posts: posts, Page: 1, Pages: 2, NextURL: "/blog?page=2", FeedURL: "/blog/feed.xml"})
	if err != nil || !strings.Contains(string(out), `href="/blog/news/post"`) || !strings.Contains(string(out), "Older") {
		t.Fatalf("list render err=%v out=%s", err, out)
	}
	feed, err := renderFeed(pageChrome{BaseURL: "https://x.test"}, "T", "D", "/blog", posts)
	if err != nil || !strings.Contains(string(feed), "<link>https://x.test/blog/news/post</link>") {
		t.Fatalf("feed err=%v out=%s", err, feed)
	}
}

func TestSitemapSkipsNoIndex(t *testing.T) {
	posts := []blogPost{{Slug: "a", BlogSlug: "n"}, {Slug: "hidden", BlogSlug: "n", NoIndex: true}}
	out, err := renderSitemap(pageChrome{BaseURL: "https://x.test"}, []blogInfo{{Slug: "n"}}, posts)
	if err != nil || strings.Contains(string(out), "hidden") || !strings.Contains(string(out), "/blog/n/a") {
		t.Fatalf("sitemap err=%v out=%s", err, out)
	}
}

func TestIsBot(t *testing.T) {
	if !isBot("Googlebot/2.1") || isBot("Mozilla/5.0 (X11; Linux) Firefox/130") {
		t.Fatal("bot detection wrong")
	}
}

func TestRowIDsHandlesEverySliceShape(t *testing.T) {
	cases := []any{
		[]int64{2, 3},
		[]any{int64(2), float64(3)},
		[][]any{{int64(2), "Two"}, {int64(3), "Three"}},
		[][2]any{{int64(2), "Two"}, {int64(3), "Three"}},
		[]any{[]any{2, "Two"}, [2]any{3, "Three"}},
	}
	for i, in := range cases {
		got := rowIDs(in)
		if len(got) != 2 || got[0] != 2 || got[1] != 3 {
			t.Errorf("case %d: rowIDs(%T) = %v", i, in, got)
		}
	}
	if rowIDs(nil) != nil || rowIDs(false) != nil {
		t.Error("non-slice values must yield no ids")
	}
}

package models

import (
	"strings"
	"testing"
)

func TestInsertBlogNavItemIsIdempotent(t *testing.T) {
	body := `<header><ul class="navbar-nav" id="site-nav-menu"><li class="nav-item" data-nav-key="home"><a href="/">Home</a></li></ul></header>`
	out, changed := insertBlogNavItem(body)
	if !changed || !strings.Contains(out, `data-nav-key="blog"`) || !strings.Contains(out, `</li><li class="nav-item" data-nav-key="blog">`) {
		t.Fatalf("expected the Blog item inside the list, got %q", out)
	}
	if again, changed := insertBlogNavItem(out); changed || again != out {
		t.Fatal("expected second insert to be a no-op")
	}
	if _, changed := insertBlogNavItem("<p>no nav</p>"); changed {
		t.Fatal("expected no insert without a nav list")
	}
}

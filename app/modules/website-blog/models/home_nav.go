package models

import (
	"context"
	"strconv"
	"strings"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

const (
	websitePageModelName = "tbl.website_page"
	homeNavMenuID        = "site-nav-menu"
	homeNavBlogKey       = "blog"
)

// init contributes the Blog entry to the website home page header: after this
// module's install/reinit, the homepage's nav list (id "site-nav-menu", seeded
// by the website module) gains a "Blog" link to /blog.
func init() {
	kernelmodules.RegisterModuleSeededHook("website-blog", addBlogToHomeNavigation)
}

func addBlogToHomeNavigation(ctx context.Context) error {
	pages, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(websitePageModelName, nil), orm.RecordMap{
		"is_homepage": true,
		"__limit":     100,
	}, []string{"id", "body_html", "deploy_path"})
	if err != nil {
		return err
	}
	for _, page := range pages {
		id, ok := int64FromAny(page["id"])
		if !ok || id <= 0 {
			continue
		}
		body, changed := insertBlogNavItem(stringValue(page["body_html"]))
		if !changed {
			continue
		}
		ids := []string{strconv.FormatInt(id, 10)}
		if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(websitePageModelName, nil), ids, orm.RecordMap{"body_html": body}); err != nil {
			return err
		}
		// Only refresh pages that are already live; unpublished pages pick the link up on publish.
		if stringValue(page["deploy_path"]) == "" {
			continue
		}
		if _, err := orm.ExecuteRuntimeMethod(ctx, websitePageModelName, "action_publish", ids, nil, nil); err != nil {
			return err
		}
		log.Infof("website-blog: added Blog to header navigation of page %d", id)
	}
	return nil
}

// insertBlogNavItem appends the Blog <li> to the first nav list with id homeNavMenuID of a page's
// body_html. It reports false when there is no such list or the item is already present, so
// reinit is idempotent and never duplicates the link.
func insertBlogNavItem(body string) (string, bool) {
	if strings.Contains(body, `data-nav-key="`+homeNavBlogKey+`"`) {
		return body, false
	}
	marker := strings.Index(body, `id="`+homeNavMenuID+`"`)
	if marker < 0 {
		return body, false
	}
	end := strings.Index(body[marker:], "</ul>")
	if end < 0 {
		return body, false
	}
	at := marker + end
	return body[:at] + blogNavItemHTML + body[at:], true
}

const blogNavItemHTML = `<li class="nav-item" data-nav-key="` + homeNavBlogKey + `"><a class="nav-link" data-i18n-vi="Blog" href="/blog">Blog</a></li>`

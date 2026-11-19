package models

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestWebsiteBehaviorsUsed(t *testing.T) {
	site := orm.RecordMap{"header_html": `<div data-bh-popup="cookie">x</div>`}
	pages := []orm.RecordMap{{"body_html": `<p data-bh-animate="fade-in">a</p><p data-bh-countdown="2030-01-01T00:00:00Z"></p>`}, {"body_html": "<p>plain</p>"}}
	got := websiteBehaviorsUsed(site, pages)
	if !reflect.DeepEqual(got, []string{"animate", "countdown", "popup"}) {
		t.Fatalf("got %v", got)
	}
	if got := websiteBehaviorsUsed(orm.RecordMap{}, []orm.RecordMap{{"body_html": "<p>x</p>"}}); len(got) != 0 {
		t.Fatalf("unused site must ship no behavior: %v", got)
	}
	if tags := renderWebsiteBehaviorScriptTags(nil); tags != "" {
		t.Fatal("no tags expected")
	}
	if tags := renderWebsiteBehaviorScriptTags([]string{"animate"}); !strings.Contains(tags, "/website/static/js/behaviors/animate.js") {
		t.Fatalf("missing tag: %q", tags)
	}
}

func TestEveryBehaviorMarkerHasAFile(t *testing.T) {
	for marker, name := range websiteBehaviorMarkers {
		raw, err := os.ReadFile("../static/js/behaviors/" + name + ".js")
		if err != nil {
			t.Fatalf("%s: %v", marker, err)
		}
		if !strings.Contains(string(raw), marker) {
			t.Errorf("%s.js does not reference %s", name, marker)
		}
	}
}

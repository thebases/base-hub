package models

import (
	"context"
	"strconv"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// init orders "seed the default site and home page, then publish it" for
// install/reinit: master data can only create rows, but the public "/" is
// served from the site's deploy_path on disk, so without this step a freshly
// installed website keeps showing the SPA login until someone publishes by hand.
func init() {
	kernelmodules.RegisterModuleSeededHook("website", publishUnpublishedActiveWebsiteSites)
}

// publishUnpublishedActiveWebsiteSites publishes every active site that has
// pages but has never been deployed (empty deploy_path). Sites that already
// have a deploy folder are left alone, so reinit never republishes over an
// operator's deliberate unpublish or edits.
func publishUnpublishedActiveWebsiteSites(ctx context.Context) error {
	siteRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsiteSiteModelName, nil), orm.RecordMap{
		"is_active": true,
		"__order":   "id asc",
		"__limit":   100,
	}, []string{"id", "deploy_path"})
	if err != nil {
		return err
	}
	for _, siteRow := range siteRows {
		siteID := anyToInt64(siteRow["id"])
		if siteID <= 0 || stringValue(siteRow["deploy_path"]) != "" {
			continue
		}
		pageRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), orm.RecordMap{
			"site_id": siteID,
			"__limit": 1,
		}, []string{"id"})
		if err != nil {
			return err
		}
		if len(pageRows) == 0 {
			continue
		}
		if _, err := actionPublishWebsiteSite(ctx, nil, []string{strconv.FormatInt(siteID, 10)}, nil, nil); err != nil {
			return err
		}
		log.Infof("website: published seeded site %d on install", siteID)
	}
	return nil
}

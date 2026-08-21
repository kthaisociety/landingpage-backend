package database

import (
	"fmt"
	"log"

	"backend/internal/models"
	"backend/internal/utils"

	"gorm.io/gorm"
)

// BackfillProfileSlugs assigns a URL slug to every profile that predates the
// Slug column (created before Profile.BeforeCreate started generating one).
// Runs unconditionally on every API boot, in every environment — not just
// dev — right after AutoMigrate, the same way schema migration does: it's
// the only way to reach production without a manual one-off step. Safe to
// run on every boot since it only ever touches profiles with an empty slug,
// so once every profile has one this is a single query that finds nothing.
func BackfillProfileSlugs(db *gorm.DB) {
	var profiles []models.Profile
	if err := db.Where("slug = ? OR slug IS NULL", "").Find(&profiles).Error; err != nil {
		log.Printf("[profile slug backfill] failed to query profiles: %v", err)
		return
	}
	if len(profiles) == 0 {
		return
	}

	taken := map[string]bool{}
	var existing []string
	db.Model(&models.Profile{}).Where("slug != ?", "").Pluck("slug", &existing)
	for _, s := range existing {
		taken[s] = true
	}

	assigned := 0
	for _, profile := range profiles {
		base := utils.Slugify(profile.FirstName + " " + profile.LastName)
		if base == "" {
			base = "member"
		}
		slug := base
		for suffix := 2; taken[slug]; suffix++ {
			slug = fmt.Sprintf("%s-%d", base, suffix)
		}
		taken[slug] = true

		if err := db.Model(&models.Profile{}).Where("id = ?", profile.Id).Update("slug", slug).Error; err != nil {
			log.Printf("[profile slug backfill] failed to set slug for profile %s: %v", profile.Id, err)
			continue
		}
		assigned++
	}

	if assigned > 0 {
		log.Printf("[profile slug backfill] assigned slugs to %d profile(s)", assigned)
	}
}

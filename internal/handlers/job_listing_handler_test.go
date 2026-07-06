package handlers

import (
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestJobListingSummaryQueryOrdersNewestFirst(t *testing.T) {
	db, err := gorm.Open(postgres.Open("host=localhost user=test dbname=test sslmode=disable"), &gorm.Config{
		DisableAutomaticPing: true,
		DryRun:               true,
	})
	if err != nil {
		t.Fatalf("open dry-run database: %v", err)
	}

	query := jobListingSummaryQuery(db).Scan(&[]SmallJobListing{})
	sql := strings.Join(strings.Fields(query.Statement.SQL.String()), " ")
	want := "ORDER BY job_listings.created_at DESC, job_listings.id DESC"
	if !strings.Contains(sql, want) {
		t.Fatalf("query ordering = %q, want it to contain %q", sql, want)
	}
}

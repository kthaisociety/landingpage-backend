package mailchimp

import (
	"testing"

	"backend/internal/models"
)

func TestDisabledMailchimpSideEffectsAreNoops(t *testing.T) {
	var api *MailchimpAPI

	profile := &models.Profile{Email: "ada@example.com"}

	if err := api.SubscribeMember(profile); err != nil {
		t.Fatalf("SubscribeMember() error = %v", err)
	}
	if err := api.SubscribeNewsletter("ada@example.com"); err != nil {
		t.Fatalf("SubscribeNewsletter() error = %v", err)
	}
	if _, err := api.UpdateMember(&profile.Email, &MemberRequest{Email: profile.Email}); err != nil {
		t.Fatalf("UpdateMember() error = %v", err)
	}
	if err := api.DeleteMember(&profile.Email); err != nil {
		t.Fatalf("DeleteMember() error = %v", err)
	}
}

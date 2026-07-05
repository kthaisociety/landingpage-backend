package mailchimp

import (
	"backend/internal/models"
	"fmt"
	"net/http"
)

const (
	main_path   = "/lists/%s/members"
	member_path = main_path + "/%s"
	delete_path = member_path + "/actions/delete-permanent"
)

// Constants for member status
const (
	Subscribed    string = "subscribed"
	Unsubscribed  string = "unsubscribed"
	Cleaned       string = "cleaned"
	Pending       string = "pending"
	Transactional string = "transactional"
)

// Constants for HTTP methods
const (
	Post   string = "POST"
	Get    string = "GET"
	Put    string = "PUT"
	Patch  string = "PATCH"
	Delete string = "DELETE"
)

// MergeFields are the merge fields for a member
type MergeFields struct {
	FirstName      string `json:"FNAME,omitempty"`
	LastName       string `json:"LNAME,omitempty"`
	Programme      string `json:"MMERGE3,omitempty"`
	GraduationYear any    `json:"YEAR,omitempty"` // string if empty, int otherwise
}

// MemberRequest is the request body for adding or updating a member
type MemberRequest struct {
	Email       string      `json:"email_address"`
	Status      string      `json:"status,omitempty"`
	MergeFields MergeFields `json:"merge_fields,omitempty"`
}

// MemberResponse is the response body for retrieving, adding or updating a member
type MemberResponse struct {
	Id          string      `json:"id"`
	Email       string      `json:"email_address"`
	EmailId     string      `json:"unique_email_id"`
	ContactId   string      `json:"contact_id"`
	FullName    string      `json:"full_name"`
	Status      string      `json:"status"`
	MergeFields MergeFields `json:"merge_fields,omitempty"`
}

func (api *MailchimpAPI) GetMember(id *string) (*MemberResponse, error) {
	if !api.IsConfigured() {
		return nil, fmt.Errorf("mailchimp is not configured")
	}

	response := &MemberResponse{}

	err := api.Request(Get, fmt.Sprintf(member_path, api.ListId, *id), nil, nil, response)
	if err != nil {
		return nil, err
	}

	return response, nil
}

func (api *MailchimpAPI) AddMember(request *MemberRequest) (*MemberResponse, error) {
	if !api.IsConfigured() {
		return nil, fmt.Errorf("mailchimp is not configured")
	}

	response := &MemberResponse{}

	err := api.Request(Post, fmt.Sprintf(main_path, api.ListId), nil, request, response)
	if err != nil {
		return nil, err
	}

	return response, nil
}

func (api *MailchimpAPI) UpdateMember(id *string, request *MemberRequest) (*MemberResponse, error) {
	if !api.IsConfigured() {
		return &MemberResponse{}, nil
	}

	response := &MemberResponse{}

	err := api.Request(Patch, fmt.Sprintf(member_path, api.ListId, *id), nil, request, response)
	if err != nil {
		return nil, err
	}

	return response, nil
}

func (api *MailchimpAPI) DeleteMember(id *string) error {
	if !api.IsConfigured() {
		return nil
	}

	err := api.Request(Post, fmt.Sprintf(delete_path, api.ListId, *id), nil, nil, nil)
	if err != nil {
		return err
	}

	return nil
}

func (api *MailchimpAPI) SubscribeMember(profile *models.Profile) error {
	if !api.IsConfigured() {
		return nil
	}

	// Check if user is subscribed to the mailing list
	_, memberResErr := api.GetMember(&profile.Email)
	if memberResErr != nil {
		serr, ok := memberResErr.(*MailchimpAPIError)
		if ok && serr.Status == http.StatusNotFound {
			// User is not subscribed, add it to the mailing list
			req := &MemberRequest{
				Email:  profile.Email,
				Status: Subscribed,
				MergeFields: MergeFields{
					FirstName:      profile.FirstName,
					LastName:       profile.LastName,
					Programme:      string(profile.Programme),
					GraduationYear: profile.GraduationYear,
				},
			}

			_, addErr := api.AddMember(req)
			if addErr != nil {
				return addErr
			}
		} else {
			return memberResErr
		}
	}

	return nil
}

// SubscribeNewsletterSubscriber adds a newsletter subscriber with the merge
// fields Mailchimp already supports (mirrors SubscribeMember, sourced from a
// NewsletterSubscription instead of a Profile). Existing members are left
// unchanged.
func (api *MailchimpAPI) SubscribeNewsletterSubscriber(sub *models.NewsletterSubscription) error {
	if !api.IsConfigured() {
		return nil
	}

	_, memberResErr := api.GetMember(&sub.Email)
	if memberResErr != nil {
		serr, ok := memberResErr.(*MailchimpAPIError)
		if ok && serr.Status == http.StatusNotFound {
			req := &MemberRequest{
				Email:  sub.Email,
				Status: Subscribed,
				MergeFields: MergeFields{
					FirstName:      sub.FirstName,
					LastName:       sub.LastName,
					Programme:      sub.Programme,
					GraduationYear: sub.GraduationYear,
				},
			}

			_, addErr := api.AddMember(req)
			return addErr
		}
		return memberResErr
	}

	return nil
}

// SubscribeNewsletter adds an email-only signup (e.g. landing page form). Existing members are left unchanged.
func (api *MailchimpAPI) SubscribeNewsletter(email string) error {
	if !api.IsConfigured() {
		return nil
	}

	_, memberResErr := api.GetMember(&email)
	if memberResErr != nil {
		serr, ok := memberResErr.(*MailchimpAPIError)
		if ok && serr.Status == http.StatusNotFound {
			req := &MemberRequest{
				Email:  email,
				Status: Subscribed,
			}
			_, addErr := api.AddMember(req)
			return addErr
		}
		return memberResErr
	}
	return nil
}

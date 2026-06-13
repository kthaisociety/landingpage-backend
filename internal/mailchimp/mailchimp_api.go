package mailchimp

import (
	"backend/internal/config"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
)

// URIFormat defines the endpoint for
const URIFormat string = "%s.api.mailchimp.com"

// Version of Mailchimp API
const Version string = "/3.0"

// DatacenterRegex defines which datacenter to hit
var DatacenterRegex = regexp.MustCompile(`[^-]\w+$`)

type MailchimpAPI struct {
	Key      string
	User     string
	ListId   string
	Endpoint string
}

type QueryParams interface {
	Params() map[string]string
}

// MailchimpAPIError is the error response from the Mailchimp API
type MailchimpAPIError struct {
	Type            string `json:"type,omitempty"`
	Title           string `json:"title,omitempty"`
	Status          int    `json:"status,omitempty"`
	Detail          string `json:"detail,omitempty"`
	Instance        string `json:"instance,omitempty"`
	ReferenceNumber string `json:"ref_no,omitempty"`
	Errors          []struct {
		Field   string `json:"field"`
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

func (err *MailchimpAPIError) Error() string {
	return fmt.Sprintf("Status: %d \n Type: %s \n Title: %s \n Details: %s \n Error: %s", err.Status, err.Type, err.Title, err.Detail, err.Errors)
}

// InitMailchimpApi creates a MailchimpAPI
func InitMailchimpApi(cfg *config.Config) (*MailchimpAPI, error) {
	apiKey := cfg.Mailchimp.APIKey
	user := cfg.Mailchimp.User
	listId := cfg.Mailchimp.ListID

	if len(apiKey) == 0 {
		return nil, fmt.Errorf("mailchimp API key is missing")
	}
	if strings.TrimSpace(listId) == "" {
		return nil, fmt.Errorf("mailchimp list/audience id is missing")
	}

	u := url.URL{}
	u.Scheme = "https"
	u.Host = fmt.Sprintf(URIFormat, DatacenterRegex.FindString(apiKey))
	u.Path = Version

	return &MailchimpAPI{
		User:     user,
		Key:      apiKey,
		ListId:   listId,
		Endpoint: u.String(),
	}, nil
}

func (api *MailchimpAPI) IsConfigured() bool {
	return api != nil &&
		strings.TrimSpace(api.Key) != "" &&
		strings.TrimSpace(api.ListId) != "" &&
		strings.TrimSpace(api.Endpoint) != ""
}

// Request will make a call to the MailchimpAPI
func (api *MailchimpAPI) Request(method, path string, params QueryParams, body, response any) error {
	if !api.IsConfigured() {
		return fmt.Errorf("mailchimp is not configured")
	}

	client := &http.Client{}

	requestURL := fmt.Sprintf("%s%s", api.Endpoint, path)

	// Prepare body
	var bodyBytes io.Reader
	var err error
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
		bodyBytes = bytes.NewBuffer(data)
	}

	// Prepare request
	req, err := http.NewRequest(method, requestURL, bodyBytes)
	if err != nil {
		return err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(api.User, api.Key)

	// Set query parameters
	if params != nil && !reflect.ValueOf(params).IsNil() {
		queryParams := req.URL.Query()
		for k, v := range params.Params() {
			if v != "" {
				queryParams.Set(k, v)
			}
		}
		req.URL.RawQuery = queryParams.Encode()
	}

	// Make request
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	// Defer closing response body
	defer resp.Body.Close()

	// Read response
	data, err = io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Check status code
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Do not unmarshall nil response
		if response == nil || reflect.ValueOf(response).IsNil() || len(data) == 0 {
			return nil
		}

		err = json.Unmarshal(data, response)
		if err != nil {
			return err
		}

		return nil
	}

	// Handle API Error
	apiError := new(MailchimpAPIError)
	err = json.Unmarshal(data, apiError)
	if err != nil {
		return err
	}

	return apiError
}

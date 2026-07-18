package email

type EmailConfig struct {
	AppName               string
	AppDescription        string
	AppEmailContact       string
	StaticURL             string
	LogoURL               string
	LegalNoticeURL        string
	TermsAndConditionsURL string
	PrivacyAndCookiesURL  string
}

var DefaultEmailConfig = EmailConfig{
	AppName:               "KTH AI Society",
	AppDescription:        "Welcome to KTHAIS",
	AppEmailContact:       "contact@kthais.com",
	StaticURL:             "https://kthais.com/static",
	LogoURL:               "https://kthais.com/brand/nav-wordmark.png",
	LegalNoticeURL:        "https://kthais.com/legal/legal-notice",
	TermsAndConditionsURL: "https://kthais.com/legal/terms-and-conditions",
	PrivacyAndCookiesURL:  "https://kthais.com/legal/privacy-and-cookies",
}

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
	AppName:               "KTHAIS",
	AppDescription:        "Welcome to KTHAIS",
	AppEmailContact:       "contact@kthais.com",
	StaticURL:             "https://kthais.com/static",
	LogoURL:               "https://kthais.com/images/brand_assets/ais-logo-main-long-white.jpg",
	LegalNoticeURL:        "https://kthais.com/page/legal/legal-notice/",
	TermsAndConditionsURL: "https://kthais.com/page/legal/terms-and-conditions/",
	PrivacyAndCookiesURL:  "https://kthais.com/page/legal/privacy-and-cookies/",
}

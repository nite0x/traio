package bootstrap

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/nite/traio/internal/config"
)

type ProviderConfig struct {
	Name         string
	SiteURL      string
	ProjectID    string
	Environment  string
	SecretPath   string
	ClientID     string
	ClientSecret string
}

func (ProviderConfig) String() string     { return "[redacted provider configuration]" }
func (p ProviderConfig) GoString() string { return p.String() }

func readProviderConfig(get func(string) string) (ProviderConfig, error) {
	p := ProviderConfig{Name: strings.ToLower(strings.TrimSpace(get("TRAIO_CONFIG_PROVIDER")))}
	if p.Name == "" {
		p.Name = "none"
	}
	if p.Name == "none" {
		return p, nil
	}
	if p.Name != "infisical" {
		return p, fmt.Errorf("unsupported TRAIO_CONFIG_PROVIDER")
	}
	p.SiteURL = strings.TrimRight(strings.TrimSpace(get("TRAIO_INFISICAL_SITE_URL")), "/")
	p.ProjectID = strings.TrimSpace(get("TRAIO_INFISICAL_PROJECT_ID"))
	p.Environment = strings.TrimSpace(get("TRAIO_INFISICAL_ENVIRONMENT"))
	p.SecretPath = strings.TrimSpace(get("TRAIO_INFISICAL_SECRET_PATH"))
	if p.SecretPath == "" {
		p.SecretPath = "/"
	}
	p.ClientID = strings.TrimSpace(get("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID"))
	var err error
	p.ClientSecret, err = config.ResolveSecretFrom(get, "INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET", "INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET_FILE")
	if err != nil {
		return p, err
	}
	for _, item := range []struct{ key, value string }{
		{"TRAIO_INFISICAL_SITE_URL", p.SiteURL}, {"TRAIO_INFISICAL_PROJECT_ID", p.ProjectID},
		{"TRAIO_INFISICAL_ENVIRONMENT", p.Environment}, {"INFISICAL_UNIVERSAL_AUTH_CLIENT_ID", p.ClientID},
		{"INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET", p.ClientSecret},
	} {
		if item.value == "" {
			return p, fmt.Errorf("%s is required when Infisical is enabled", item.key)
		}
	}
	u, err := url.Parse(p.SiteURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return p, fmt.Errorf("TRAIO_INFISICAL_SITE_URL must be an HTTPS origin")
	}
	if !strings.HasPrefix(p.SecretPath, "/") {
		return p, fmt.Errorf("TRAIO_INFISICAL_SECRET_PATH must start with /")
	}
	return p, nil
}

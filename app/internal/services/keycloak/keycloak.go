package keycloak

// File overview:
// - Responsibility: encapsulates domain/service orchestration for keycloak, keeping integrations and multi-step business flows out of transport handlers.
// - Key symbols: `HandleAuthKeycloak`, `string`, `OpenIDConfig`, `MtlsEndpointAliases`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	servicelog "git.thebase.vn/base_hub/internal/services/log"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

type OpenIDConfig struct {
	Issuer                                                    string              `json:"issuer"`
	AuthorizationEndpoint                                     string              `json:"authorization_endpoint"`
	TokenEndpoint                                             string              `json:"token_endpoint"`
	IntrospectionEndpoint                                     string              `json:"introspection_endpoint"`
	UserinfoEndpoint                                          string              `json:"userinfo_endpoint"`
	EndSessionEndpoint                                        string              `json:"end_session_endpoint"`
	FrontchannelLogoutSessionSupported                        bool                `json:"frontchannel_logout_session_supported"`
	FrontchannelLogoutSupported                               bool                `json:"frontchannel_logout_supported"`
	JwksURI                                                   string              `json:"jwks_uri"`
	CheckSessionIframe                                        string              `json:"check_session_iframe"`
	GrantTypesSupported                                       []string            `json:"grant_types_supported"`
	AcrValuesSupported                                        []string            `json:"acr_values_supported"`
	ResponseTypesSupported                                    []string            `json:"response_types_supported"`
	SubjectTypesSupported                                     []string            `json:"subject_types_supported"`
	IDTokenSigningAlgValuesSupported                          []string            `json:"id_token_signing_alg_values_supported"`
	IDTokenEncryptionAlgValuesSupported                       []string            `json:"id_token_encryption_alg_values_supported"`
	IDTokenEncryptionEncValuesSupported                       []string            `json:"id_token_encryption_enc_values_supported"`
	UserinfoSigningAlgValuesSupported                         []string            `json:"userinfo_signing_alg_values_supported"`
	UserinfoEncryptionAlgValuesSupported                      []string            `json:"userinfo_encryption_alg_values_supported"`
	UserinfoEncryptionEncValuesSupported                      []string            `json:"userinfo_encryption_enc_values_supported"`
	RequestObjectSigningAlgValuesSupported                    []string            `json:"request_object_signing_alg_values_supported"`
	RequestObjectEncryptionAlgValuesSupported                 []string            `json:"request_object_encryption_alg_values_supported"`
	RequestObjectEncryptionEncValuesSupported                 []string            `json:"request_object_encryption_enc_values_supported"`
	ResponseModesSupported                                    []string            `json:"response_modes_supported"`
	RegistrationEndpoint                                      string              `json:"registration_endpoint"`
	TokenEndpointAuthMethodsSupported                         []string            `json:"token_endpoint_auth_methods_supported"`
	TokenEndpointAuthSigningAlgValuesSupported                []string            `json:"token_endpoint_auth_signing_alg_values_supported"`
	IntrospectionEndpointAuthMethodsSupported                 []string            `json:"introspection_endpoint_auth_methods_supported"`
	IntrospectionEndpointAuthSigningAlgValuesSupported        []string            `json:"introspection_endpoint_auth_signing_alg_values_supported"`
	AuthorizationSigningAlgValuesSupported                    []string            `json:"authorization_signing_alg_values_supported"`
	AuthorizationEncryptionAlgValuesSupported                 []string            `json:"authorization_encryption_alg_values_supported"`
	AuthorizationEncryptionEncValuesSupported                 []string            `json:"authorization_encryption_enc_values_supported"`
	ClaimsSupported                                           []string            `json:"claims_supported"`
	ClaimTypesSupported                                       []string            `json:"claim_types_supported"`
	ClaimsParameterSupported                                  bool                `json:"claims_parameter_supported"`
	ScopesSupported                                           []string            `json:"scopes_supported"`
	RequestParameterSupported                                 bool                `json:"request_parameter_supported"`
	RequestURIParameterSupported                              bool                `json:"request_uri_parameter_supported"`
	RequireRequestURIRegistration                             bool                `json:"require_request_uri_registration"`
	CodeChallengeMethodsSupported                             []string            `json:"code_challenge_methods_supported"`
	TLSClientCertificateBoundAccessTokens                     bool                `json:"tls_client_certificate_bound_access_tokens"`
	RevocationEndpoint                                        string              `json:"revocation_endpoint"`
	RevocationEndpointAuthMethodsSupported                    []string            `json:"revocation_endpoint_auth_methods_supported"`
	RevocationEndpointAuthSigningAlgValuesSupported           []string            `json:"revocation_endpoint_auth_signing_alg_values_supported"`
	BackchannelLogoutSupported                                bool                `json:"backchannel_logout_supported"`
	BackchannelLogoutSessionSupported                         bool                `json:"backchannel_logout_session_supported"`
	DeviceAuthorizationEndpoint                               string              `json:"device_authorization_endpoint"`
	BackchannelTokenDeliveryModesSupported                    []string            `json:"backchannel_token_delivery_modes_supported"`
	BackchannelAuthenticationEndpoint                         string              `json:"backchannel_authentication_endpoint"`
	BackchannelAuthenticationRequestSigningAlgValuesSupported []string            `json:"backchannel_authentication_request_signing_alg_values_supported"`
	RequirePushedAuthorizationRequests                        bool                `json:"require_pushed_authorization_requests"`
	PushedAuthorizationRequestEndpoint                        string              `json:"pushed_authorization_request_endpoint"`
	MtlsEndpointAliases                                       MtlsEndpointAliases `json:"mtls_endpoint_aliases"`
	AuthorizationResponseIssParameterSupported                bool                `json:"authorization_response_iss_parameter_supported"`
}
type MtlsEndpointAliases struct {
	TokenEndpoint                      string `json:"token_endpoint"`
	RevocationEndpoint                 string `json:"revocation_endpoint"`
	IntrospectionEndpoint              string `json:"introspection_endpoint"`
	DeviceAuthorizationEndpoint        string `json:"device_authorization_endpoint"`
	RegistrationEndpoint               string `json:"registration_endpoint"`
	UserinfoEndpoint                   string `json:"userinfo_endpoint"`
	PushedAuthorizationRequestEndpoint string `json:"pushed_authorization_request_endpoint"`
	BackchannelAuthenticationEndpoint  string `json:"backchannel_authentication_endpoint"`
}

type Settings struct {
	KeyKeycloak string
}

var settings = Settings{
	KeyKeycloak: "https://your-keycloak-url.com", // Replace with actual URL
}

func HandleAuthKeycloak(authorization string) (map[string]interface{}, error) {
	parts := strings.Split(authorization, " ")
	if len(parts) != 2 {
		return nil, errors.New("authorization header is invalid")
	}

	encodedStr := parts[1]
	decodedBytes, err := base64.StdEncoding.DecodeString(encodedStr)
	if err != nil {
		servicelog.Errorf("binascii.Error: Incorrect padding")
		return nil, fmt.Errorf("binascii.Error: Incorrect padding")
	}

	decodedStr := string(decodedBytes)
	creds := strings.SplitN(decodedStr, ":", 2)
	if len(creds) != 2 {
		return nil, errors.New("invalid client credentials format")
	}
	clientID := creds[0]
	clientSecret := creds[1]

	log.Printf("client_id === %s\n", clientID)
	log.Printf("client_secret === %s\n", clientSecret)
	log.Printf("url === %s\n", settings.KeyKeycloak)

	// Prepare form data
	data := url.Values{}
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)
	data.Set("grant_type", "client_credentials")

	// Make HTTP POST request
	resp, err := http.PostForm(settings.KeyKeycloak, data)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body) // Replaces ioutil.ReadAll
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{
			StatusCode: 401,
			Detail: map[string]string{
				"error":             "invalid_client",
				"error_description": "Invalid client or Invalid client credentials",
			},
		}
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// HTTPError is a custom error for HTTP responses
type HTTPError struct {
	StatusCode int
	Detail     map[string]string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %v", e.StatusCode, e.Detail)
}

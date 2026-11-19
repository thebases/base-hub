package config

// File overview:
// - Responsibility: normalises legacy and camelCase config keys and values into the canonical structure.
// - Key symbols: `normalizeConfigStructure`, `ensureNestedConfigMap`, `moveConfigValue`, `missingSystemConfigRows`.

import (
	"fmt"
	"strings"
)

func normalizeConfigStructure(configMap map[string]any) map[string]any {
	if configMap == nil {
		return map[string]any{}
	}

	// Fold legacy flat app keys and older server-scoped tenant flags into the
	// nested app block so both old and new YAML shapes decode the same runtime contract.
	appSection := ensureNestedConfigMap(configMap, "app")
	moveConfigValue(configMap, appSection, "app_name")
	moveConfigValue(configMap, appSection, "version")
	moveConfigValue(configMap, appSection, "env")
	moveConfigValue(configMap, appSection, "init_import")
	moveConfigValue(configMap, appSection, "use_multi_tenant")

	serverSection := ensureNestedConfigMap(configMap, "server")
	moveConfigValue(serverSection, appSection, "use_multi_tenant")
	// OAuth issuer/TTL settings are server-owned. Keep legacy top-level
	// oauth.* inputs loadable while collapsing new writes and sys.config_param
	// seeds into server.*.
	if oauthSection, ok := configMap["oauth"].(map[string]any); ok {
		moveConfigValue(oauthSection, serverSection, "issuer")
		moveConfigValue(oauthSection, serverSection, "access_token_ttl_minutes")
		moveConfigValue(oauthSection, serverSection, "refresh_token_ttl_days")
		if len(oauthSection) == 0 {
			delete(configMap, "oauth")
		}
	}

	authServiceSection := ensureNestedConfigMap(configMap, "auth_service")
	// Older dev/runtime config stored builtin OAuth runtime-only key material
	// under auth.oauth.*. Fold those values into authService.* so there is one
	// auth-service namespace going forward, while preserving destination values
	// if an operator has already set the new names.
	if authSection, ok := configMap["auth"].(map[string]any); ok {
		if authOAuthSection, ok := authSection["oauth"].(map[string]any); ok {
			moveConfigValue(authOAuthSection, authServiceSection, "rsa_key_id")
			moveConfigValue(authOAuthSection, authServiceSection, "rsa_private_key")
			if len(authOAuthSection) == 0 {
				delete(authSection, "oauth")
			}
		}
		if len(authSection) == 0 {
			delete(configMap, "auth")
		}
	}
	if len(authServiceSection) == 0 {
		delete(configMap, "auth_service")
	}

	// Keep markdown roots stable while config files migrate between userRoot,
	// docRoot, and the old server.userDocsPath location.
	markdownSection := ensureNestedConfigMap(configMap, "markdown")
	moveConfigValue(serverSection, markdownSection, "user_docs_path")
	if len(serverSection) == 0 {
		delete(configMap, "server")
	}
	if len(markdownSection) == 0 {
		delete(configMap, "markdown")
	}
	if len(appSection) == 0 {
		delete(configMap, "app")
	}
	return configMap
}

func ensureNestedConfigMap(parent map[string]any, key string) map[string]any {
	value, exists := parent[key]
	if !exists {
		nested := make(map[string]any)
		parent[key] = nested
		return nested
	}
	if nested, ok := value.(map[string]any); ok {
		return nested
	}
	nested := make(map[string]any)
	parent[key] = nested
	return nested
}

func moveConfigValue(source map[string]any, destination map[string]any, key string) {
	if source == nil || destination == nil {
		return
	}
	if _, exists := destination[key]; exists {
		delete(source, key)
		return
	}
	value, exists := source[key]
	if !exists {
		return
	}
	destination[key] = value
	delete(source, key)
}

func missingSystemConfigRows(fileRows []systemConfigRow, existingRows []systemConfigRow) []systemConfigRow {
	existingKeys := make(map[string]struct{}, len(existingRows))
	for _, row := range existingRows {
		existingKeys[canonicalSystemConfigKey(row.Name)] = struct{}{}
	}

	missingRows := make([]systemConfigRow, 0)
	for _, row := range fileRows {
		row.Name = canonicalSystemConfigKey(row.Name)
		if _, ok := existingKeys[row.Name]; ok {
			continue
		}
		missingRows = append(missingRows, row)
	}
	return missingRows
}

// cloneConfigMap clones the config map handled by this package.
func cloneConfigMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	clonedValue := normalizeConfigValue(input)
	clonedMap, ok := clonedValue.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return clonedMap
}

func canonicalSystemConfigKey(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, ".")
	for index, part := range parts {
		parts[index] = canonicalConfigKeySegment(part)
	}
	return strings.Join(parts, ".")
}

func canonicalConfigKeySegment(segment string) string {
	normalized := strings.ToLower(strings.TrimSpace(segment))
	switch normalized {
	case "appname", "app_name":
		return "app_name"
	case "initimport", "init_import":
		return "init_import"
	case "usemultitenant", "use_multi_tenant":
		return "use_multi_tenant"
	case "encryptkey", "encrypt_key":
		return "encrypt_key"
	case "deviceprovisioningenable", "device_provisioning_enable":
		return "device_provisioning_enable"
	case "discussinboundwebhooksecret", "discuss_inbound_webhook_secret":
		return "discuss_inbound_webhook_secret"
	case "destinationurl", "destination_url":
		return "destination_url"
	case "authservice", "auth_service":
		return "auth_service"
	case "clientid", "client_id":
		return "client_id"
	case "clientsecret", "client_secret":
		return "client_secret"
	case "servicename", "service_name":
		return "service_name"
	case "jwksurl", "jwks_url":
		return "jwks_url"
	case "roleclaim", "role_claim":
		return "role_claim"
	case "loginclaim", "login_claim":
		return "login_claim"
	case "tenantclaim", "tenant_claim":
		return "tenant_claim"
	case "autoprovision", "auto_provision":
		return "auto_provision"
	case "clockskewsecs", "clock_skew_secs":
		return "clock_skew_secs"
	case "rsakeyid", "rsa_key_id":
		return "rsa_key_id"
	case "rsaprivatekey", "rsa_private_key":
		return "rsa_private_key"
	case "basepath", "base_path":
		return "base_path"
	case "supportflow", "support_flow":
		return "support_flow"
	case "supportapikey", "support_apikey":
		return "support_apikey"
	case "staticpath", "static_path":
		return "static_path"
	case "sharedfolder", "shared_folder":
		return "shared_folder"
	case "accesstokenttlminutes", "access_token_ttl_minutes":
		return "access_token_ttl_minutes"
	case "refreshtokenttldays", "refresh_token_ttl_days":
		return "refresh_token_ttl_days"
	case "publicdocspath", "public_docs_path":
		return "public_docs_path"
	case "username", "user_name":
		return "user_name"
	case "homedbname", "home_db_name":
		return "home_db_name"
	case "templatedbname", "template_db_name":
		return "template_db_name"
	case "dbhost", "db_host":
		return "db_host"
	case "dbport", "db_port":
		return "db_port"
	case "dbuser", "db_user":
		return "db_user"
	case "dbpassword", "db_password":
		return "db_password"
	case "dbsslmode", "db_sslmode":
		return "db_sslmode"
	case "dbfilter", "db_filter":
		return "db_filter"
	case "usessl", "use_ssl":
		return "use_ssl"
	case "isproducer", "is_producer":
		return "is_producer"
	case "iscomsumer", "is_comsumer":
		return "is_comsumer"
	case "usededupe", "use_dedupe":
		return "use_dedupe"
	case "usefile", "use_file":
		return "use_file"
	case "reqpath", "req_path":
		return "req_path"
	case "userroot", "user_root":
		return "user_root"
	case "docroot", "doc_root":
		return "doc_root"
	case "alloworigins", "allow_origins":
		return "allow_origins"
	case "allowmethods", "allow_methods":
		return "allow_methods"
	case "allowheaders", "allow_headers":
		return "allow_headers"
	case "exposeheaders", "expose_headers":
		return "expose_headers"
	case "allowcredentials", "allow_credentials":
		return "allow_credentials"
	case "usetcp", "use_tcp":
		return "use_tcp"
	case "defaultport", "default_port":
		return "default_port"
	case "portmin", "port_min":
		return "port_min"
	case "portmax", "port_max":
		return "port_max"
	case "maxconns", "max_conns":
		return "max_conns"
	case "maxconnsperip", "max_conns_per_ip":
		return "max_conns_per_ip"
	case "maxframebytes", "max_frame_bytes":
		return "max_frame_bytes"
	case "firstframes", "first_frame_s":
		return "first_frame_s"
	case "idletimeouts", "idle_timeout_s":
		return "idle_timeout_s"
	case "readtimeouts", "read_timeout_s":
		return "read_timeout_s"
	case "maxreadlimit", "max_read_limit":
		return "max_read_limit"
	case "outboundallowcidrs", "outbound_allow_cidrs":
		return "outbound_allow_cidrs"
	case "allowcidrs", "allow_cidrs":
		return "allow_cidrs"
	case "proxyprotocol", "proxy_protocol":
		return "proxy_protocol"
	case "trustedproxies", "trusted_proxies":
		return "trusted_proxies"
	case "tlscertfile", "tls_cert_file":
		return "tls_cert_file"
	case "tlskeyfile", "tls_key_file":
		return "tls_key_file"
	case "tlsclientcafile", "tls_client_ca_file":
		return "tls_client_ca_file"
	default:
		return normalized
	}
}

func camelConfigKeySegment(segment string) string {
	switch canonicalConfigKeySegment(segment) {
	case "app_name":
		return "appName"
	case "init_import":
		return "initImport"
	case "use_multi_tenant":
		return "useMultiTenant"
	case "encrypt_key":
		return "encryptKey"
	case "device_provisioning_enable":
		return "deviceProvisioningEnable"
	case "discuss_inbound_webhook_secret":
		return "discussInboundWebhookSecret"
	case "destination_url":
		return "destinationUrl"
	case "auth_service":
		return "authService"
	case "client_id":
		return "clientId"
	case "client_secret":
		return "clientSecret"
	case "service_name":
		return "serviceName"
	case "jwks_url":
		return "jwksUrl"
	case "role_claim":
		return "roleClaim"
	case "login_claim":
		return "loginClaim"
	case "tenant_claim":
		return "tenantClaim"
	case "auto_provision":
		return "autoProvision"
	case "clock_skew_secs":
		return "clockSkewSecs"
	case "rsa_key_id":
		return "rsaKeyId"
	case "rsa_private_key":
		return "rsaPrivateKey"
	case "base_path":
		return "basePath"
	case "support_flow":
		return "supportFlow"
	case "support_apikey":
		return "supportApikey"
	case "static_path":
		return "staticPath"
	case "shared_folder":
		return "sharedFolder"
	case "access_token_ttl_minutes":
		return "accessTokenTtlMinutes"
	case "refresh_token_ttl_days":
		return "refreshTokenTtlDays"
	case "public_docs_path":
		return "publicDocsPath"
	case "user_name":
		return "userName"
	case "home_db_name":
		return "homeDbName"
	case "template_db_name":
		return "templateDbName"
	case "db_host":
		return "dbHost"
	case "db_port":
		return "dbPort"
	case "db_user":
		return "dbUser"
	case "db_password":
		return "dbPassword"
	case "db_sslmode":
		return "dbSslmode"
	case "db_filter":
		return "dbFilter"
	case "use_ssl":
		return "useSsl"
	case "is_producer":
		return "isProducer"
	case "is_comsumer":
		return "isComsumer"
	case "use_dedupe":
		return "useDedupe"
	case "use_file":
		return "useFile"
	case "req_path":
		return "reqPath"
	case "user_root":
		return "userRoot"
	case "doc_root":
		return "docRoot"
	case "allow_origins":
		return "allowOrigins"
	case "allow_methods":
		return "allowMethods"
	case "allow_headers":
		return "allowHeaders"
	case "expose_headers":
		return "exposeHeaders"
	case "allow_credentials":
		return "allowCredentials"
	case "use_tcp":
		return "useTcp"
	case "default_port":
		return "defaultPort"
	case "port_min":
		return "portMin"
	case "port_max":
		return "portMax"
	case "max_conns":
		return "maxConns"
	case "max_conns_per_ip":
		return "maxConnsPerIp"
	case "max_frame_bytes":
		return "maxFrameBytes"
	case "first_frame_s":
		return "firstFrameS"
	case "idle_timeout_s":
		return "idleTimeoutS"
	case "read_timeout_s":
		return "readTimeoutS"
	case "max_read_limit":
		return "maxReadLimit"
	case "outbound_allow_cidrs":
		return "outboundAllowCidrs"
	case "allow_cidrs":
		return "allowCidrs"
	case "proxy_protocol":
		return "proxyProtocol"
	case "trusted_proxies":
		return "trustedProxies"
	case "tls_cert_file":
		return "tlsCertFile"
	case "tls_key_file":
		return "tlsKeyFile"
	case "tls_client_ca_file":
		return "tlsClientCaFile"
	default:
		return canonicalConfigKeySegment(segment)
	}
}

func canonicalizeConfigMapKeys(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		canonical := make(map[string]any, len(typed))
		for key, item := range typed {
			canonical[canonicalConfigKeySegment(key)] = canonicalizeConfigMapKeys(item)
		}
		return canonical
	case map[any]any:
		canonical := make(map[string]any, len(typed))
		for key, item := range typed {
			canonical[canonicalConfigKeySegment(fmt.Sprint(key))] = canonicalizeConfigMapKeys(item)
		}
		return canonical
	case []any:
		cloned := make([]any, 0, len(typed))
		for _, item := range typed {
			cloned = append(cloned, canonicalizeConfigMapKeys(item))
		}
		return cloned
	default:
		return typed
	}
}

func camelizeConfigMapKeys(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		camelized := make(map[string]any, len(typed))
		for key, item := range typed {
			camelized[camelConfigKeySegment(key)] = camelizeConfigMapKeys(item)
		}
		return camelized
	case map[any]any:
		camelized := make(map[string]any, len(typed))
		for key, item := range typed {
			camelized[camelConfigKeySegment(fmt.Sprint(key))] = camelizeConfigMapKeys(item)
		}
		return camelized
	case []any:
		cloned := make([]any, 0, len(typed))
		for _, item := range typed {
			cloned = append(cloned, camelizeConfigMapKeys(item))
		}
		return cloned
	default:
		return typed
	}
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func normalizeConfigValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[key] = normalizeConfigValue(item)
		}
		return cloned
	case map[any]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[fmt.Sprint(key)] = normalizeConfigValue(item)
		}
		return cloned
	case []any:
		cloned := make([]any, 0, len(typed))
		for _, item := range typed {
			cloned = append(cloned, normalizeConfigValue(item))
		}
		return cloned
	case []string:
		cloned := make([]any, 0, len(typed))
		for _, item := range typed {
			cloned = append(cloned, item)
		}
		return cloned
	default:
		return typed
	}
}

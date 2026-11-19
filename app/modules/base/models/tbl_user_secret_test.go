package models

import "testing"

// T19: credential columns on base.users carry the kernel secret flag, so client transports strip
// them and non-sudo callers cannot filter, sort or group on them.
func TestResUserCredentialFieldsAreSecret(t *testing.T) {
	for _, name := range []string{"password", "api_key", "passwordresettoken", "authdata", "twofactorsecret"} {
		field := ResUserDefinition.Fields[name]
		if field == nil || !field.Secret {
			t.Errorf("base.users %s should be secret", name)
		}
	}
	if login := ResUserDefinition.Fields["login"]; login == nil || login.Secret {
		t.Error("login must exist and not be secret")
	}
}

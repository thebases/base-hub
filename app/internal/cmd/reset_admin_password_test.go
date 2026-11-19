package cmd

import "testing"

func TestResolvedResetAdminPasswordValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{name: "rejects omitted", args: nil, wantErr: true},
		{name: "rejects blank", args: []string{"   "}, wantErr: true},
		{name: "keeps provided password", args: []string{"secret-123"}, want: "secret-123"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolvedResetAdminPasswordValue(tc.args)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("resolvedResetAdminPasswordValue(%v) = %q, %v; want %q, wantErr %t", tc.args, got, err, tc.want, tc.wantErr)
			}
		})
	}
}

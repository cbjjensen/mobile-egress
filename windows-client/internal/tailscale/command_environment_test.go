package tailscale

import (
	"reflect"
	"testing"
)

func TestMergeTailscaleEnvironmentReplacesOnlyExactCLIEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base []string
		want []string
	}{
		{name: "nil", want: []string{"TAILSCALE_BE_CLI=1"}},
		{name: "empty", base: []string{}, want: []string{"TAILSCALE_BE_CLI=1"}},
		{
			name: "duplicates and unrelated order",
			base: []string{
				"FIRST=1",
				"TAILSCALE_BE_CLI=0",
				"MIDDLE=2",
				"TAILSCALE_BE_CLI=legacy",
				"LAST=3",
			},
			want: []string{"FIRST=1", "MIDDLE=2", "LAST=3", "TAILSCALE_BE_CLI=1"},
		},
		{
			name: "lookalike names remain",
			base: []string{
				"tailscale_be_cli=0",
				"TAILSCALE_BE_CLI_EXTRA=0",
				"XTAILSCALE_BE_CLI=0",
			},
			want: []string{
				"tailscale_be_cli=0",
				"TAILSCALE_BE_CLI_EXTRA=0",
				"XTAILSCALE_BE_CLI=0",
				"TAILSCALE_BE_CLI=1",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := mergeTailscaleEnvironment(test.base)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("mergeTailscaleEnvironment() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestMergeTailscaleEnvironmentDoesNotAliasInput(t *testing.T) {
	t.Parallel()

	base := []string{"FIRST=1", "TAILSCALE_BE_CLI=0"}
	merged := mergeTailscaleEnvironment(base)
	merged[0] = "CHANGED=1"
	if got := base[0]; got != "FIRST=1" {
		t.Fatalf("input changed to %q", got)
	}
}

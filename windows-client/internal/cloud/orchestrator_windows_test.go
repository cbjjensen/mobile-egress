//go:build windows

package cloud

import (
	"encoding/base64"
	"os"
	"os/exec"
	"testing"
)

func TestNodeTrustBootstrapScriptsParseInWindowsPowerShell(t *testing.T) {
	t.Parallel()

	release := testNodeRelease(t)
	for name, script := range map[string]string{"install": installScript(release), "update": updateScript(release)} {
		script := script
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			command := exec.Command("powershell", "-NoProfile", "-Command", `$tokens = $null
$parseErrors = $null
$script = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($env:MOBILE_EGRESS_TEST_SCRIPT))
$null = [Management.Automation.Language.Parser]::ParseInput($script, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count -ne 0) {
  $parseErrors | ForEach-Object { [Console]::Error.WriteLine($_.Message) }
  exit 1
}`)
			command.Env = append(os.Environ(), "MOBILE_EGRESS_TEST_SCRIPT="+base64.StdEncoding.EncodeToString([]byte(script)))
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("generated %s script did not parse: %v\n%s", name, err, output)
			}
		})
	}
}

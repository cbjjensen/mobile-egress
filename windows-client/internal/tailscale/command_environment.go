package tailscale

import "strings"

const tailscaleCLIEnvironmentPrefix = "TAILSCALE_BE_CLI="

func mergeTailscaleEnvironment(base []string) []string {
	merged := make([]string, 0, len(base)+1)
	for _, entry := range base {
		if strings.HasPrefix(entry, tailscaleCLIEnvironmentPrefix) {
			continue
		}
		merged = append(merged, entry)
	}
	return append(merged, tailscaleCLIEnvironmentPrefix+"1")
}

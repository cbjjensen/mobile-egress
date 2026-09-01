//go:build darwin

package tailscale

func NewDarwinInstaller() DarwinInstaller {
	return DarwinInstaller{
		VerifyPKG:        verifyStagedMacPKGOnDarwin,
		LaunchInstaller:  launchDarwinInstaller,
		FindInstallation: resolveDarwinInstallation,
		Cleanup:          newInstallerCleanupManager(),
		PollInterval:     defaultMacInstallPollInterval,
		PollLimit:        maximumMacInstallPollDuration,
	}
}

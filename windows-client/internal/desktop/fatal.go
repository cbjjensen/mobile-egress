package desktop

type darwinFatalClass uint8

const (
	fatalStartup darwinFatalClass = iota
	fatalRuntime
)

type darwinFatalText struct {
	heading string
	body    string
	stderr  string
}

func darwinFatalMessage(class darwinFatalClass) darwinFatalText {
	if class == fatalRuntime {
		return darwinFatalText{
			heading: "ZFNF Mobile Egress stopped unexpectedly.",
			body:    "Reopen the desktop controller and try again.",
			stderr:  "ZFNF Mobile Egress: desktop runtime failed.",
		}
	}
	return darwinFatalText{
		heading: "ZFNF Mobile Egress could not start.",
		body:    "The desktop controller could not be initialized. Reopen the app and try again.",
		stderr:  "ZFNF Mobile Egress: startup failed.",
	}
}

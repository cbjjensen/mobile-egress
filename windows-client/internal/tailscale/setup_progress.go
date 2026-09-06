package tailscale

import "context"

type setupProgressKey struct{}

// WithSetupProgress attaches an operation-local observer. Background status
// polling and unrelated installations never inherit this callback.
func WithSetupProgress(ctx context.Context, report func(stage, message string)) context.Context {
	return context.WithValue(ctx, setupProgressKey{}, report)
}

func ReportSetupProgress(ctx context.Context, stage, message string) {
	if ctx.Err() != nil {
		return
	}
	if report, ok := ctx.Value(setupProgressKey{}).(func(string, string)); ok && report != nil {
		report(stage, message)
	}
}

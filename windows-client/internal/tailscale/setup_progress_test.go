package tailscale

import (
	"context"
	"reflect"
	"sync"
	"testing"
)

func TestSetupProgressCallbacksStayWithinTheirOperationContext(t *testing.T) {
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			var got []string
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := WithSetupProgress(parent, func(stage, _ string) { got = append(got, stage) })
			ReportSetupProgress(context.Background(), "background", "")
			ReportSetupProgress(ctx, "download", "")
			ReportSetupProgress(ctx, "verify", "")
			cancel()
			ReportSetupProgress(ctx, "install", "")
			if !reflect.DeepEqual(got, []string{"download", "verify"}) {
				t.Errorf("operation progress = %v", got)
			}
		})
	}
	workers.Wait()
}

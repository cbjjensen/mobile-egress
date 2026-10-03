package desktop

import (
	"context"
	"testing"
)

func TestClientDownloadsOpenOfficialReleasePageInNativeBrowser(t *testing.T) {
	var opened string
	app := &DesktopApp{ctx: context.Background(), browserOpenURL: func(_ context.Context, value string) { opened = value }}
	if err := app.OpenClientDownloads(); err != nil {
		t.Fatal(err)
	}
	if opened != "https://github.com/cbjjensen/mobile-egress/releases" {
		t.Fatalf("download destination = %q", opened)
	}
	if err := (&DesktopApp{}).OpenClientDownloads(); err == nil {
		t.Fatal("unavailable native browser action reported success")
	}
}

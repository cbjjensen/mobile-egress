package clientapp

import (
	"context"
	"encoding/base64"
	"errors"
	"mobile-egress/windows-client/internal/nodeservice"
	"sync"

	qrcode "github.com/skip2/go-qrcode"
)

type App struct {
	browser          func(string) error
	service          Service
	clipboard        func(string) error
	mu               sync.Mutex
	invitation       string
	phoneInvitations map[string]string
}

func New(service Service, clipboard func(string) error) *App {
	return &App{service: service, clipboard: clipboard}
}
func (app *App) Status() nodeservice.StandaloneStatus { return app.service.Status() }

type BundleView struct {
	Bundle    string `json:"bundle"`
	QRDataURL string `json:"qrDataUrl"`
}

func renderBundle(bundle string) (BundleView, error) {
	compact, err := compactQRBundle(bundle)
	if err != nil {
		return BundleView{}, errors.New("Unable to display this QR. Use the complete invitation text.")
	}
	// A negative size gives every module exactly four pixels, including the
	// quiet zone. Fixed image sizes and CSS shrinking blur dense invitations.
	png, err := qrcode.Encode(compact, qrcode.Medium, -4)
	if err != nil {
		return BundleView{}, errors.New("Unable to display this QR. Use the complete invitation text.")
	}
	return BundleView{Bundle: bundle, QRDataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)}, nil
}
func (app *App) direct() (DirectService, error) {
	direct, ok := app.service.(DirectService)
	if !ok {
		return nil, errors.New("Update the Client service to use direct pairing.")
	}
	return direct, nil
}
func (app *App) Configure(bindAddress, endpoint, displayName string) error {
	direct, err := app.direct()
	if err != nil {
		return err
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	// Saving the endpoint can succeed before host-firewall setup reports an error.
	// Never let a previous, invalidated invitation survive either outcome.
	app.invitation = ""
	app.phoneInvitations = nil
	return direct.Configure(context.Background(), nodeservice.DirectConfiguration{Transport: "direct", BindAddress: bindAddress, Endpoint: endpoint, DisplayName: displayName})
}
func (app *App) IssueInvitation() (BundleView, error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	direct, err := app.direct()
	if err != nil {
		return BundleView{}, err
	}
	bundle, err := direct.IssueInvitation(context.Background())
	if err != nil {
		return BundleView{}, err
	}
	view, err := renderBundle(bundle)
	if err != nil {
		return BundleView{}, err
	}
	app.invitation = bundle
	return view, nil
}
func (app *App) CopyInvitation() error {
	app.mu.Lock()
	defer app.mu.Unlock()
	bundle := app.invitation
	if bundle == "" {
		return errors.New("Generate a pairing invitation first.")
	}
	service := app.service
	if wrapper, ok := service.(*firewallService); ok {
		service = wrapper.DirectService
	}
	if phones, ok := service.(PhoneService); ok {
		view, err := phones.Phones(context.Background())
		if err != nil {
			return err
		}
		if len(view.Phones) > 1 {
			return errors.New("Choose a phone before copying its pairing invitation.")
		}
	}
	return app.copy(bundle)
}
func (app *App) CancelInvitation() error {
	direct, err := app.direct()
	if err != nil {
		return err
	}
	if err = direct.CancelInvitation(context.Background()); err != nil {
		return err
	}
	app.mu.Lock()
	app.invitation = ""
	app.mu.Unlock()
	return nil
}
func (app *App) ExportEndpointUpdate() (BundleView, error) {
	direct, err := app.direct()
	if err != nil {
		return BundleView{}, err
	}
	bundle, err := direct.ExportEndpointUpdate(context.Background())
	if err != nil {
		return BundleView{}, err
	}
	return renderBundle(bundle)
}
func (app *App) CopyEndpointUpdate() error {
	direct, err := app.direct()
	if err != nil {
		return err
	}
	bundle, err := direct.ExportEndpointUpdate(context.Background())
	if err != nil {
		return err
	}
	return app.copy(bundle)
}
func (app *App) Revoke() error {
	direct, err := app.direct()
	if err != nil {
		return err
	}
	if err = direct.Revoke(context.Background()); err != nil {
		return err
	}
	app.mu.Lock()
	app.invitation = ""
	app.mu.Unlock()
	return nil
}
func (app *App) copy(value string) error {
	if app.clipboard == nil {
		return errors.New("Clipboard is unavailable.")
	}
	return app.clipboard(value)
}
func (app *App) CopyProxy(kind string) error {
	if kind != "http" && kind != "socks" {
		return errors.New("Unknown proxy format.")
	}
	value, err := app.service.Proxy(context.Background(), kind)
	if err != nil {
		return err
	}
	return app.copy(value)
}

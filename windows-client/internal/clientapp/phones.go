package clientapp

import (
	"context"
	"errors"
	"mobile-egress/windows-client/internal/nodeservice"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PhoneService is additive: older service doubles and legacy GUI operations
// retain their original contracts. New operations never choose a phone implicitly.
type PhoneService interface {
	Phones(context.Context) (nodeservice.PhonesStatus, error)
	AddPhone(context.Context, string) (nodeservice.PhoneInvitation, error)
	CancelPhoneInvitation(context.Context, string) error
	RenamePhone(context.Context, string, string) error
	RevokePhone(context.Context, string) error
	PhoneProxy(context.Context, string, string) (string, error)
	ExportPhoneEndpointUpdate(context.Context, string) (string, error)
	RetryPhoneProxy(context.Context, string) error
}

func phoneService(service Service) (PhoneService, error) {
	if phones, ok := service.(PhoneService); ok {
		return phones, nil
	}
	return nil, errors.New("Update the Client service to manage multiple phones.")
}
func validPhoneID(id string) error {
	if id == "" || len(id) > 128 || strings.TrimSpace(id) != id || !utf8.ValidString(id) {
		return errors.New("Choose a saved phone.")
	}
	for _, r := range id {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return errors.New("Invalid phone identifier.")
		}
	}
	return nil
}
func phoneName(name string, allowEmpty bool) (string, error) {
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("Phone names cannot contain control characters.")
		}
	}
	name = strings.TrimSpace(name)
	if (!allowEmpty && name == "") || utf8.RuneCountInString(name) > 64 || !utf8.ValidString(name) {
		return "", errors.New("Use a phone name from 1 to 64 characters.")
	}
	return name, nil
}
func handlePhoneRequest(ctx context.Context, service Service, r Request, response *Response) (bool, error) {
	switch r.Method {
	case "phones", "add-phone", "cancel-phone-invitation", "rename-phone", "revoke-phone", "phone-proxy", "export-phone-update", "retry-phone-proxy":
	default:
		return false, nil
	}
	if r.Value != "" {
		return true, errors.New("Invalid phone request.")
	}
	if r.Method == "phones" || r.Method == "add-phone" {
		if r.PhoneID != "" || r.Kind != "" || (r.Method == "phones" && r.Name != "") {
			return true, errors.New("Invalid phone request.")
		}
	} else {
		if err := validPhoneID(r.PhoneID); err != nil {
			return true, err
		}
		if (r.Method != "rename-phone" && r.Name != "") || (r.Method != "phone-proxy" && r.Kind != "") {
			return true, errors.New("Invalid phone request.")
		}
	}
	var err error
	if r.Method == "add-phone" || r.Method == "rename-phone" {
		r.Name, err = phoneName(r.Name, r.Method == "add-phone")
		if err != nil {
			return true, err
		}
	}
	if r.Method == "phone-proxy" && r.Kind != "http" && r.Kind != "socks" {
		return true, errors.New("Unknown proxy format.")
	}
	s, err := phoneService(service)
	if err != nil {
		return true, err
	}
	switch r.Method {
	case "phones":
		var view nodeservice.PhonesStatus
		view, err = s.Phones(ctx)
		if err == nil {
			if view.Phones == nil {
				view.Phones = []nodeservice.PhoneStatus{}
			}
			response.Phones = &view
		}
	case "add-phone":
		var view nodeservice.PhoneInvitation
		view, err = s.AddPhone(ctx, r.Name)
		if err == nil {
			response.Invitation = &view
		}
	case "cancel-phone-invitation":
		err = s.CancelPhoneInvitation(ctx, r.PhoneID)
	case "rename-phone":
		err = s.RenamePhone(ctx, r.PhoneID, r.Name)
	case "revoke-phone":
		err = s.RevokePhone(ctx, r.PhoneID)
	case "phone-proxy":
		response.Value, err = s.PhoneProxy(ctx, r.PhoneID, r.Kind)
	case "export-phone-update":
		response.Value, err = s.ExportPhoneEndpointUpdate(ctx, r.PhoneID)
	case "retry-phone-proxy":
		err = s.RetryPhoneProxy(ctx, r.PhoneID)
	}
	return true, err
}

type PhoneBundleView struct {
	PhoneID string `json:"phoneId"`
	BundleView
}

func (app *App) Phones() (nodeservice.PhonesStatus, error) {
	s, err := app.phonesService()
	if err != nil {
		return nodeservice.PhonesStatus{}, err
	}
	view, err := s.Phones(app.lifetime)
	if view.Phones == nil {
		view.Phones = []nodeservice.PhoneStatus{}
	}
	return view, err
}

func (app *App) phonesService() (PhoneService, error) {
	if err := app.lifetime.Err(); err != nil {
		return nil, err
	}
	return phoneService(app.service)
}
func (app *App) AddPhone(name string) (PhoneBundleView, error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	name, err := phoneName(name, true)
	if err != nil {
		return PhoneBundleView{}, err
	}
	s, err := app.phonesService()
	if err != nil {
		return PhoneBundleView{}, err
	}
	invitation, err := s.AddPhone(app.lifetime, name)
	if err != nil {
		return PhoneBundleView{}, err
	}
	view, err := renderBundle(invitation.Bundle)
	if err != nil {
		return PhoneBundleView{}, err
	}
	app.invitation = ""
	// Authority permits only one pending invitation. A newly returned ID also
	// discards obsolete cached capabilities from an external cancel/expiry.
	app.phoneInvitations = map[string]string{invitation.PhoneID: invitation.Bundle}
	return PhoneBundleView{PhoneID: invitation.PhoneID, BundleView: view}, nil
}
func (app *App) CopyPhoneInvitation(id string) error {
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.phoneAction(id, func(s PhoneService) error {
		bundle := app.phoneInvitations[id]
		if bundle == "" {
			return errors.New("Show this phone’s pairing invitation first.")
		}
		view, err := s.Phones(app.lifetime)
		if err != nil {
			return err
		}
		if view.PendingPhoneID != id {
			delete(app.phoneInvitations, id)
			return errors.New("This pairing invitation is no longer active.")
		}
		return app.copy(bundle)
	})
}
func (app *App) phoneAction(id string, action func(PhoneService) error) error {
	if err := validPhoneID(id); err != nil {
		return err
	}
	s, err := app.phonesService()
	if err != nil {
		return err
	}
	return action(s)
}
func (app *App) CancelPhoneInvitation(id string) error {
	app.mu.Lock()
	defer app.mu.Unlock()
	err := app.phoneAction(id, func(s PhoneService) error { return s.CancelPhoneInvitation(app.lifetime, id) })
	if err == nil {
		delete(app.phoneInvitations, id)
	}
	return err
}
func (app *App) RenamePhone(id, name string) error {
	name, err := phoneName(name, false)
	if err != nil {
		return err
	}
	return app.phoneAction(id, func(s PhoneService) error { return s.RenamePhone(app.lifetime, id, name) })
}
func (app *App) RevokePhone(id string) error {
	app.mu.Lock()
	defer app.mu.Unlock()
	err := app.phoneAction(id, func(s PhoneService) error { return s.RevokePhone(app.lifetime, id) })
	if err == nil {
		delete(app.phoneInvitations, id)
	}
	return err
}
func (app *App) RetryPhoneProxy(id string) error {
	return app.phoneAction(id, func(s PhoneService) error { return s.RetryPhoneProxy(app.lifetime, id) })
}
func (app *App) CopyPhoneProxy(id, kind string) error {
	if kind != "http" && kind != "socks" {
		return errors.New("Unknown proxy format.")
	}
	return app.phoneAction(id, func(s PhoneService) error {
		value, err := s.PhoneProxy(app.lifetime, id, kind)
		if err != nil {
			return err
		}
		return app.copy(value)
	})
}
func (app *App) ExportPhoneEndpointUpdate(id string) (BundleView, error) {
	var view BundleView
	err := app.phoneAction(id, func(s PhoneService) error {
		bundle, err := s.ExportPhoneEndpointUpdate(app.lifetime, id)
		if err != nil {
			return err
		}
		view, err = renderBundle(bundle)
		return err
	})
	return view, err
}
func (app *App) CopyPhoneEndpointUpdate(id string) error {
	return app.phoneAction(id, func(s PhoneService) error {
		bundle, err := s.ExportPhoneEndpointUpdate(app.lifetime, id)
		if err != nil {
			return err
		}
		return app.copy(bundle)
	})
}

func (s *firewallService) Phones(ctx context.Context) (nodeservice.PhonesStatus, error) {
	p, err := phoneService(s.DirectService)
	if err != nil {
		return nodeservice.PhonesStatus{}, err
	}
	return p.Phones(ctx)
}
func (s *firewallService) AddPhone(ctx context.Context, name string) (nodeservice.PhoneInvitation, error) {
	p, err := phoneService(s.DirectService)
	if err != nil {
		return nodeservice.PhoneInvitation{}, err
	}
	return p.AddPhone(ctx, name)
}
func (s *firewallService) CancelPhoneInvitation(ctx context.Context, id string) error {
	p, err := phoneService(s.DirectService)
	if err != nil {
		return err
	}
	return p.CancelPhoneInvitation(ctx, id)
}
func (s *firewallService) RenamePhone(ctx context.Context, id, name string) error {
	p, err := phoneService(s.DirectService)
	if err != nil {
		return err
	}
	return p.RenamePhone(ctx, id, name)
}
func (s *firewallService) RevokePhone(ctx context.Context, id string) error {
	p, err := phoneService(s.DirectService)
	if err != nil {
		return err
	}
	return p.RevokePhone(ctx, id)
}
func (s *firewallService) PhoneProxy(ctx context.Context, id, kind string) (string, error) {
	p, err := phoneService(s.DirectService)
	if err != nil {
		return "", err
	}
	return p.PhoneProxy(ctx, id, kind)
}
func (s *firewallService) ExportPhoneEndpointUpdate(ctx context.Context, id string) (string, error) {
	p, err := phoneService(s.DirectService)
	if err != nil {
		return "", err
	}
	return p.ExportPhoneEndpointUpdate(ctx, id)
}
func (s *firewallService) RetryPhoneProxy(ctx context.Context, id string) error {
	p, err := phoneService(s.DirectService)
	if err != nil {
		return err
	}
	return p.RetryPhoneProxy(ctx, id)
}

func (client LocalClient) Phones(ctx context.Context) (nodeservice.PhonesStatus, error) {
	r, err := client.callRequest(ctx, Request{Method: "phones"})
	if err != nil {
		return nodeservice.PhonesStatus{}, err
	}
	if r.Phones == nil {
		return nodeservice.PhonesStatus{}, errors.New("Client phone status is unavailable.")
	}
	return *r.Phones, nil
}
func (client LocalClient) AddPhone(ctx context.Context, name string) (nodeservice.PhoneInvitation, error) {
	r, err := client.callRequest(ctx, Request{Method: "add-phone", Name: name})
	if err != nil {
		return nodeservice.PhoneInvitation{}, err
	}
	if r.Invitation == nil {
		return nodeservice.PhoneInvitation{}, errors.New("Client phone invitation is unavailable.")
	}
	return *r.Invitation, nil
}
func (client LocalClient) CancelPhoneInvitation(ctx context.Context, id string) error {
	_, err := client.callRequest(ctx, Request{Method: "cancel-phone-invitation", PhoneID: id})
	return err
}
func (client LocalClient) RenamePhone(ctx context.Context, id, name string) error {
	_, err := client.callRequest(ctx, Request{Method: "rename-phone", PhoneID: id, Name: name})
	return err
}
func (client LocalClient) RevokePhone(ctx context.Context, id string) error {
	_, err := client.callRequest(ctx, Request{Method: "revoke-phone", PhoneID: id})
	return err
}
func (client LocalClient) PhoneProxy(ctx context.Context, id, kind string) (string, error) {
	r, err := client.callRequest(ctx, Request{Method: "phone-proxy", PhoneID: id, Kind: kind})
	return r.Value, err
}
func (client LocalClient) ExportPhoneEndpointUpdate(ctx context.Context, id string) (string, error) {
	r, err := client.callRequest(ctx, Request{Method: "export-phone-update", PhoneID: id})
	return r.Value, err
}
func (client LocalClient) RetryPhoneProxy(ctx context.Context, id string) error {
	_, err := client.callRequest(ctx, Request{Method: "retry-phone-proxy", PhoneID: id})
	return err
}

package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	outboundproxy "github.com/fynhq/windwatch/internal/proxy"
	"github.com/fynhq/windwatch/internal/store"
)

const proxyTestTarget = "example.com:443"

type proxyFormValues struct {
	Name     string
	Type     model.ProxyType
	Address  string
	Username string
	Password string
}

// proxyOption is the template-facing view of a proxy. It deliberately omits
// PasswordEnc so page models cannot accidentally render a stored secret.
type proxyOption struct {
	ID      int64
	Name    string
	Type    model.ProxyType
	Address string
}

func proxyOptions(proxies []model.Proxy) []proxyOption {
	options := make([]proxyOption, 0, len(proxies))
	for _, proxy := range proxies {
		options = append(options, proxyOption{
			ID:      proxy.ID,
			Name:    proxy.Name,
			Type:    proxy.Type,
			Address: proxy.Address,
		})
	}
	return options
}

type proxySettingsPage struct {
	settingsSectionPage
	Proxies          []proxyOption
	ProxyForm        proxyFormValues
	ProxyFormOpen    bool
	ProxyFormEdit    bool
	ProxyFormAction  string
	ProxyTestAction  string
	ProxyTestDetail  string
	ProxyTypeOptions []model.ProxyType
}

func (s *Server) loadProxySettingsPage(r *http.Request, auth model.AuthInfo) (proxySettingsPage, error) {
	base := s.newSettingsSectionPage(r, auth, "proxies")
	proxies, err := s.store.ListProxies(r.Context())
	if err != nil {
		return proxySettingsPage{}, err
	}
	return proxySettingsPage{
		settingsSectionPage: base,
		Proxies:             proxyOptions(proxies),
		ProxyFormAction:     "/settings/proxies",
		ProxyTestAction:     "/settings/proxies/test",
		ProxyTypeOptions:    []model.ProxyType{model.ProxyHTTP, model.ProxySOCKS5},
	}, nil
}

func (s *Server) handleProxyTest(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	if !s.authorizedPost(w, r, auth) {
		return
	}
	var existing *model.Proxy
	if idValue := r.PathValue("id"); idValue != "" {
		id, err := strconv.ParseInt(idValue, 10, 64)
		if err != nil || id <= 0 {
			s.renderError(w, r, http.StatusNotFound, "error.proxyNotFound")
			return
		}
		proxy, err := s.store.ProxyByID(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			s.renderError(w, r, http.StatusNotFound, "error.proxyNotFound")
			return
		}
		if err != nil {
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
		existing = &proxy
	}

	values := parseProxyForm(r)
	password := values.Password
	if password == "" && existing != nil && existing.PasswordEnc != "" {
		decrypted, err := s.cipher.Decrypt(existing.PasswordEnc)
		if err != nil {
			s.log.Error("decrypt proxy password for test", "proxy_id", existing.ID, "error", err)
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
		password = decrypted
	}
	config, err := outboundproxy.NewConfig(values.Type, strings.TrimSpace(values.Address), values.Username, password)
	if err != nil {
		key := "error.proxyAddress"
		if values.Type != model.ProxyHTTP && values.Type != model.ProxySOCKS5 {
			key = "error.proxyType"
		}
		s.renderProxyTestError(w, r, auth, values, existing, key, "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := outboundproxy.CheckConnectivity(ctx, config, proxyTestTarget, 8*time.Second); err != nil {
		s.log.Warn("proxy connectivity test failed", "address", config.Address, "error", err)
		s.renderProxyTestError(w, r, auth, values, existing, "error.proxyTestFailed", truncateDetail(err.Error()))
		return
	}

	s.renderProxyTestSuccess(w, r, auth, values, existing)
}

func (s *Server) renderProxyTestSuccess(w http.ResponseWriter, r *http.Request, auth model.AuthInfo, values proxyFormValues, existing *model.Proxy) {
	page, err := s.loadProxySettingsPage(r, auth)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	page.ProxyFormOpen = true
	page.ProxyForm = values
	page.Notice = page.I18n.T("notice.proxyTestOK")
	if existing != nil {
		page.ProxyFormEdit = true
		page.ProxyFormAction = "/settings/proxies/" + strconv.FormatInt(existing.ID, 10)
		page.ProxyTestAction = page.ProxyFormAction + "/test"
	}
	s.render(w, "settings_proxies", http.StatusOK, page)
}

func (s *Server) renderProxyTestError(w http.ResponseWriter, r *http.Request, auth model.AuthInfo, values proxyFormValues, existing *model.Proxy, errorKey, detail string) {
	page, err := s.loadProxySettingsPage(r, auth)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	page.ProxyFormOpen = true
	page.ProxyForm = values
	page.Error = page.I18n.T(errorKey)
	page.ProxyTestDetail = detail
	if existing != nil {
		page.ProxyFormEdit = true
		page.ProxyFormAction = "/settings/proxies/" + strconv.FormatInt(existing.ID, 10)
		page.ProxyTestAction = page.ProxyFormAction + "/test"
	}
	s.render(w, "settings_proxies", http.StatusBadRequest, page)
}

func truncateDetail(value string) string {
	return model.TruncateText(value, 256)
}

func (s *Server) handleProxyCreate(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	if !s.authorizedPost(w, r, auth) {
		return
	}
	values := parseProxyForm(r)
	proxy, err := s.proxyFromValues(r, values, nil)
	if err != nil {
		s.renderSettingsError(w, r, auth, values, 0, formErrorKey(err))
		return
	}
	if _, err := s.store.CreateProxy(r.Context(), proxy, s.now().UTC()); err != nil {
		if errors.Is(err, store.ErrProxyNameTaken) {
			s.renderSettingsError(w, r, auth, values, 0, "error.proxyNameTaken")
			return
		}
		s.log.Error("create proxy", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	http.Redirect(w, r, "/settings/proxies?notice=proxy_created", http.StatusSeeOther)
}

func (s *Server) handleProxyUpdate(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.proxyNotFound")
		return
	}
	if !s.authorizedPost(w, r, auth) {
		return
	}
	existing, err := s.store.ProxyByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "error.proxyNotFound")
		return
	}
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	values := parseProxyForm(r)
	proxy, err := s.proxyFromValues(r, values, &existing)
	if err != nil {
		s.renderSettingsError(w, r, auth, values, id, formErrorKey(err))
		return
	}
	if err := s.store.UpdateProxy(r.Context(), proxy, s.now().UTC()); err != nil {
		if errors.Is(err, store.ErrProxyNameTaken) {
			s.renderSettingsError(w, r, auth, values, id, "error.proxyNameTaken")
			return
		}
		s.log.Error("update proxy", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	http.Redirect(w, r, "/settings/proxies?notice=proxy_updated", http.StatusSeeOther)
}

func (s *Server) handleProxyDelete(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.proxyNotFound")
		return
	}
	if !s.authorizedPost(w, r, auth) {
		return
	}
	if err := s.store.DeleteProxy(r.Context(), id); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("delete proxy", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	http.Redirect(w, r, "/settings/proxies?notice=proxy_deleted", http.StatusSeeOther)
}

func (s *Server) proxyFromValues(r *http.Request, values proxyFormValues, existing *model.Proxy) (model.Proxy, error) {
	proxy := model.Proxy{
		Name:     strings.TrimSpace(values.Name),
		Type:     values.Type,
		Address:  strings.TrimSpace(values.Address),
		Username: values.Username,
	}
	if existing != nil {
		proxy.ID = existing.ID
		proxy.CreatedAt = existing.CreatedAt
		proxy.PasswordEnc = existing.PasswordEnc
	}
	if values.Password != "" {
		encrypted, err := s.cipher.Encrypt(values.Password)
		if err != nil {
			return model.Proxy{}, err
		}
		proxy.PasswordEnc = encrypted
	}
	if err := proxy.Validate(); err != nil {
		return model.Proxy{}, newFormError(proxyValidationKey(err))
	}
	return proxy, nil
}

func parseProxyForm(r *http.Request) proxyFormValues {
	return proxyFormValues{
		Name:     strings.TrimSpace(r.PostFormValue("name")),
		Type:     model.ProxyType(r.PostFormValue("type")),
		Address:  strings.TrimSpace(r.PostFormValue("address")),
		Username: strings.TrimSpace(r.PostFormValue("username")),
		Password: r.PostFormValue("password"),
	}
}

func proxyValidationKey(err error) string {
	switch {
	case errors.Is(err, model.ErrProxyName):
		return "error.proxyName"
	case errors.Is(err, model.ErrProxyType):
		return "error.proxyType"
	case errors.Is(err, model.ErrProxyAddress):
		return "error.proxyAddress"
	default:
		return "error.invalidRequest"
	}
}

func (s *Server) renderSettingsError(w http.ResponseWriter, r *http.Request, auth model.AuthInfo, values proxyFormValues, editingID int64, errorKey string) {
	page, err := s.loadProxySettingsPage(r, auth)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	page.ProxyFormOpen = true
	page.ProxyFormEdit = editingID > 0
	page.ProxyForm = values
	if editingID > 0 {
		page.ProxyFormAction = "/settings/proxies/" + strconv.FormatInt(editingID, 10)
		page.ProxyTestAction = page.ProxyFormAction + "/test"
	}
	page.Error = page.I18n.T(errorKey)
	s.render(w, "settings_proxies", http.StatusBadRequest, page)
}

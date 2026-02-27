package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/a-h/templ"
	"github.com/shelterkin/shelterkin/components"
	"github.com/shelterkin/shelterkin/internal/apperror"
)

type Handler struct {
	service       *Service
	sessionSecret string
	secure        bool
	csrfToken     func(context.Context) string
}

func NewHandler(service *Service, sessionSecret string, secure bool, csrfToken func(context.Context) string) *Handler {
	return &Handler{
		service:       service,
		sessionSecret: sessionSecret,
		secure:        secure,
		csrfToken:     csrfToken,
	}
}

func (h *Handler) HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	if GetUser(r.Context()) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	page := withLayout("Sign in", h.csrfToken(r.Context()), LoginPage(LoginPageData{
		CSRFToken: h.csrfToken(r.Context()),
	}))
	page.Render(r.Context(), w)
}

func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")

	session, appErr := h.service.Login(r.Context(), email, password, ClientIP(r), r.UserAgent())
	if appErr != nil {
		h.renderLoginError(w, r, appErr, email)
		return
	}

	SetSessionCookie(w, session.ID, h.sessionSecret, h.secure)

	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) HandleRegisterPage(w http.ResponseWriter, r *http.Request) {
	if GetUser(r.Context()) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	inviteToken := r.URL.Query().Get("token")
	page := withLayout("Create account", h.csrfToken(r.Context()), RegisterPage(RegisterPageData{
		InviteToken: inviteToken,
		CSRFToken:   h.csrfToken(r.Context()),
	}))
	page.Render(r.Context(), w)
}

func (h *Handler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	input := RegisterInput{
		Email:         r.FormValue("email"),
		Password:      r.FormValue("password"),
		DisplayName:   r.FormValue("display_name"),
		InviteToken:   r.FormValue("invite_token"),
		HouseholdName: r.FormValue("household_name"),
		IPAddress:     ClientIP(r),
		UserAgent:     r.UserAgent(),
	}

	session, appErr := h.service.Register(r.Context(), input)
	if appErr != nil {
		h.renderRegisterError(w, r, appErr, input)
		return
	}

	SetSessionCookie(w, session.ID, h.sessionSecret, h.secure)

	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user != nil {
		if appErr := h.service.Logout(r.Context(), user.SessionID); appErr != nil {
			slog.Error("logout failed", "session_id", user.SessionID, "error", appErr)
		}
	}

	ClearSessionCookie(w, h.secure)

	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/login")
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *Handler) renderLoginError(w http.ResponseWriter, r *http.Request, appErr *apperror.Error, email string) {
	status := apperror.HTTPStatus(appErr)

	if appErr.Type == apperror.TypeRateLimited {
		w.Header().Set("Retry-After", strconv.Itoa(int(appErr.RetryAfter.Seconds())))
	}

	data := LoginPageData{
		Error:     appErr,
		Email:     email,
		CSRFToken: h.csrfToken(r.Context()),
	}

	if isHTMX(r) {
		renderHTML(w, r, status, LoginPage(data))
		return
	}

	renderHTML(w, r, status, withLayout("Sign in", h.csrfToken(r.Context()), LoginPage(data)))
}

func (h *Handler) renderRegisterError(w http.ResponseWriter, r *http.Request, appErr *apperror.Error, input RegisterInput) {
	status := apperror.HTTPStatus(appErr)

	data := RegisterPageData{
		Error:         appErr,
		InviteToken:   input.InviteToken,
		Email:         input.Email,
		DisplayName:   input.DisplayName,
		HouseholdName: input.HouseholdName,
		CSRFToken:     h.csrfToken(r.Context()),
	}

	if isHTMX(r) {
		renderHTML(w, r, status, RegisterPage(data))
		return
	}

	renderHTML(w, r, status, withLayout("Create account", h.csrfToken(r.Context()), RegisterPage(data)))
}

func renderHTML(w http.ResponseWriter, r *http.Request, status int, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	component.Render(r.Context(), w)
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func withLayout(title, csrfToken string, content templ.Component) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return components.Layout(title, csrfToken).Render(
			templ.WithChildren(ctx, content), w,
		)
	})
}

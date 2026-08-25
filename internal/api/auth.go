package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	traioauth "github.com/nite/traio/internal/auth"
	"github.com/nite/traio/internal/store"
)

const principalKey = "traio.principal"
const authServiceKey = "traio.auth.service"

func authenticationMiddleware(deps Deps) gin.HandlerFunc {
	if deps.Auth == nil || deps.Auth.Mode() == traioauth.ModeLocal {
		return localAPIMiddleware(deps.APIToken, deps.AllowedAPIHosts...)
	}
	if deps.Auth.Mode() == traioauth.ModeDisabledDev {
		return func(c *gin.Context) {
			setPrincipal(c, traioauth.LocalPrincipal())
			c.Next()
		}
	}
	return sessionMiddleware(deps.Auth, deps.AllowedAPIHosts)
}

func sessionMiddleware(service *traioauth.Service, allowedHosts []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		host := requestHost(c.Request.Host)
		if !isAllowedAPIHost(host, allowedHosts) {
			c.AbortWithStatusJSON(http.StatusMisdirectedRequest, gin.H{"error": "API host is not allowed"})
			return
		}
		rawToken, err := c.Cookie(service.CookieName())
		if err != nil || rawToken == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		principal, session, err := service.Authenticate(c.Request.Context(), rawToken)
		if err != nil {
			clearAuthCookies(c, service)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "session is invalid or expired"})
			return
		}
		c.Set(authServiceKey, service)
		if unsafeMethod(c.Request.Method) {
			csrfCookie, _ := c.Cookie(service.CSRFCookieName())
			csrfHeader := c.GetHeader("X-CSRF-Token")
			if csrfCookie == "" || csrfHeader == "" || csrfCookie != csrfHeader || !service.ValidateCSRF(session, csrfHeader) {
				service.Audit(c.Request.Context(), store.AuditEvent{WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Action: "auth.csrf_rejected", RemoteAddr: c.ClientIP()})
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "CSRF validation failed"})
				return
			}
		}
		setPrincipal(c, principal)
		c.Next()
	}
}

func requirePermission(permission traioauth.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := currentPrincipal(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		if !traioauth.Allows(principal.Role, permission) {
			if value, ok := c.Get(authServiceKey); ok {
				if service, ok := value.(*traioauth.Service); ok {
					service.Audit(c.Request.Context(), store.AuditEvent{WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Action: "auth.permission_denied", TargetType: "route", TargetID: c.Request.Method + " " + c.FullPath(), RemoteAddr: c.ClientIP(), Metadata: `{"permission":"` + string(permission) + `"}`})
				}
			}
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "permission denied", "permission": permission})
			return
		}
		c.Next()
		if permission != traioauth.PermissionView && c.Writer.Status() < http.StatusBadRequest {
			if value, ok := c.Get(authServiceKey); ok {
				if service, ok := value.(*traioauth.Service); ok {
					service.Audit(c.Request.Context(), store.AuditEvent{WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Action: "api." + string(permission), TargetType: "route", TargetID: c.Request.Method + " " + c.FullPath(), RemoteAddr: c.ClientIP()})
				}
			}
		}
	}
}

func setPrincipal(c *gin.Context, principal traioauth.Principal) {
	c.Set(principalKey, principal)
	c.Request = c.Request.WithContext(traioauth.WithPrincipal(c.Request.Context(), principal))
}

func currentPrincipal(c *gin.Context) (traioauth.Principal, bool) {
	value, ok := c.Get(principalKey)
	if !ok {
		return traioauth.Principal{}, false
	}
	principal, ok := value.(traioauth.Principal)
	return principal, ok
}

func registerAuthRoutes(r *gin.Engine, deps Deps) {
	r.GET("/auth/mode", func(c *gin.Context) {
		mode := traioauth.ModeLocal
		if deps.Auth != nil {
			mode = deps.Auth.Mode()
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"mode": mode})
	})
	r.GET("/auth/login", beginOIDCLogin(deps.Auth))
	r.GET("/auth/callback", completeOIDCLogin(deps.Auth))
	r.POST("/auth/password/login", passwordLogin(deps.Auth, deps.AllowedAPIHosts, newPasswordLoginLimiter()))

	protected := r.Group("/auth", authenticationMiddleware(deps))
	protected.GET("/me", authMe)
	protected.POST("/logout", authLogout(deps.Auth))
	if deps.Auth != nil {
		protected.GET("/broker/callback", requirePermission(traioauth.PermissionBrokerManage), completeBrokerOAuth(deps.Auth, deps.Connections))
	}
}

type passwordLoginAttempt struct {
	count     int
	windowEnd time.Time
}

type passwordLoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]passwordLoginAttempt
	now      func() time.Time
}

func newPasswordLoginLimiter() *passwordLoginLimiter {
	return &passwordLoginLimiter{attempts: make(map[string]passwordLoginAttempt), now: time.Now}
}

func (l *passwordLoginLimiter) allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	attempt, ok := l.attempts[key]
	if !ok || !attempt.windowEnd.After(now) {
		delete(l.attempts, key)
		return true
	}
	return attempt.count < 5
}

func (l *passwordLoginLimiter) failed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	attempt := l.attempts[key]
	if !attempt.windowEnd.After(now) {
		attempt = passwordLoginAttempt{windowEnd: now.Add(5 * time.Minute)}
	}
	attempt.count++
	l.attempts[key] = attempt
	if len(l.attempts) > 2048 {
		for candidate, value := range l.attempts {
			if !value.windowEnd.After(now) {
				delete(l.attempts, candidate)
			}
		}
	}
}

func (l *passwordLoginLimiter) succeeded(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

func passwordLogin(service *traioauth.Service, allowedHosts []string, limiter *passwordLoginLimiter) gin.HandlerFunc {
	type request struct {
		Username string `json:"username"`
		Password string `json:"password"`
		ReturnTo string `json:"return_to"`
	}
	return func(c *gin.Context) {
		if service == nil || service.Mode() != traioauth.ModePassword {
			c.JSON(http.StatusNotFound, gin.H{"error": "built-in login is not enabled"})
			return
		}
		if !isAllowedAPIHost(requestHost(c.Request.Host), allowedHosts) {
			c.JSON(http.StatusMisdirectedRequest, gin.H{"error": "API host is not allowed"})
			return
		}
		remoteKey := requestHost(c.Request.RemoteAddr)
		if !limiter.allowed(remoteKey) {
			c.Header("Retry-After", "300")
			service.Audit(c.Request.Context(), store.AuditEvent{Action: "auth.login_rate_limited", RemoteAddr: remoteKey})
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many login attempts; try again later"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var body request
		if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Username) == "" || body.Password == "" {
			limiter.failed(remoteKey)
			c.JSON(http.StatusBadRequest, gin.H{"error": "username and password are required"})
			return
		}
		result, err := service.LoginWithPassword(c.Request.Context(), body.Username, body.Password, body.ReturnTo)
		if err != nil {
			if errors.Is(err, traioauth.ErrInvalidCredentials) {
				limiter.failed(remoteKey)
				service.Audit(c.Request.Context(), store.AuditEvent{Action: "auth.login_failed", RemoteAddr: remoteKey})
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "login unavailable"})
			return
		}
		limiter.succeeded(remoteKey)
		setAuthCookies(c, service, result.SessionToken, result.CSRFToken, result.ExpiresAt)
		service.Audit(c.Request.Context(), store.AuditEvent{WorkspaceID: result.Identity.Workspace.ID, UserID: result.Identity.User.ID, Action: "auth.login_succeeded", RemoteAddr: remoteKey})
		c.Header("Cache-Control", "no-store")
		c.Status(http.StatusNoContent)
	}
}

func completeBrokerOAuth(service *traioauth.Service, runtime brokerConnectionRuntime) gin.HandlerFunc {
	return func(c *gin.Context) {
		if runtime == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "broker runtime unavailable"})
			return
		}
		if providerError := strings.TrimSpace(c.Query("error")); providerError != "" {
			c.Redirect(http.StatusFound, "/brokers?oauth=failed")
			return
		}
		principal, _ := currentPrincipal(c)
		connectionID, err := service.ConsumeBrokerOAuth(c.Request.Context(), principal, c.Query("state"))
		if err != nil {
			c.Redirect(http.StatusFound, "/brokers?oauth=invalid_state")
			return
		}
		code := strings.TrimSpace(c.Query("code"))
		if code == "" {
			c.Redirect(http.StatusFound, "/brokers?oauth=missing_code")
			return
		}
		if err := runtime.ExchangeConnectionOAuthCode(c.Request.Context(), connectionID, code); err != nil {
			c.Redirect(http.StatusFound, "/brokers?oauth=exchange_failed")
			return
		}
		service.Audit(c.Request.Context(), store.AuditEvent{WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Action: "broker.oauth_authenticated", TargetType: "broker_connection", TargetID: strconv.FormatInt(connectionID, 10), RemoteAddr: c.ClientIP()})
		c.Redirect(http.StatusFound, "/brokers?oauth=success")
	}
}

func beginOIDCLogin(service *traioauth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service == nil || service.Mode() != traioauth.ModeOIDC {
			c.JSON(http.StatusNotFound, gin.H{"error": "OIDC login is not enabled"})
			return
		}
		loginURL, err := service.BeginLogin(c.Request.Context(), c.Query("return_to"))
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "could not start login"})
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Redirect(http.StatusFound, loginURL)
	}
}

func completeOIDCLogin(service *traioauth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service == nil || service.Mode() != traioauth.ModeOIDC {
			c.JSON(http.StatusNotFound, gin.H{"error": "OIDC login is not enabled"})
			return
		}
		if providerError := strings.TrimSpace(c.Query("error")); providerError != "" {
			c.Redirect(http.StatusFound, "/login?error=provider_error")
			return
		}
		result, err := service.CompleteLogin(c.Request.Context(), c.Query("state"), c.Query("code"))
		if err != nil {
			service.Audit(c.Request.Context(), store.AuditEvent{Action: "auth.login_failed", RemoteAddr: c.ClientIP()})
			c.Redirect(http.StatusFound, "/login?error=login_failed")
			return
		}
		setAuthCookies(c, service, result.SessionToken, result.CSRFToken, result.ExpiresAt)
		service.Audit(c.Request.Context(), store.AuditEvent{WorkspaceID: result.Identity.Workspace.ID, UserID: result.Identity.User.ID, Action: "auth.login_succeeded", RemoteAddr: c.ClientIP()})
		c.Redirect(http.StatusFound, result.ReturnTo)
	}
}

func authMe(c *gin.Context) {
	principal, ok := currentPrincipal(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, principal)
}

func authLogout(service *traioauth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, _ := currentPrincipal(c)
		if service != nil && service.UsesSessions() {
			rawToken, _ := c.Cookie(service.CookieName())
			_ = service.Logout(c.Request.Context(), rawToken)
			service.Audit(c.Request.Context(), store.AuditEvent{WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Action: "auth.logout", RemoteAddr: c.ClientIP()})
			clearAuthCookies(c, service)
		}
		c.Status(http.StatusNoContent)
	}
}

func listWorkspaceMembers(service *traioauth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, _ := currentPrincipal(c)
		members, err := service.ListMembers(c.Request.Context(), principal.WorkspaceID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "list workspace members"})
			return
		}
		c.JSON(http.StatusOK, members)
	}
}

func inviteWorkspaceMember(service *traioauth.Service) gin.HandlerFunc {
	type request struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	return func(c *gin.Context) {
		principal, _ := currentPrincipal(c)
		var body request
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "email and role are required"})
			return
		}
		if err := service.InviteMember(c.Request.Context(), principal, body.Email, body.Role); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		service.Audit(c.Request.Context(), store.AuditEvent{WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Action: "workspace.member_invited", TargetType: "email", TargetID: strings.ToLower(strings.TrimSpace(body.Email)), RemoteAddr: c.ClientIP()})
		c.Status(http.StatusNoContent)
	}
}

func updateWorkspaceMemberRole(service *traioauth.Service) gin.HandlerFunc {
	type request struct {
		Role string `json:"role"`
	}
	return func(c *gin.Context) {
		principal, _ := currentPrincipal(c)
		userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
		if err != nil || userID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
			return
		}
		var body request
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "role is required"})
			return
		}
		if principal.Role == "admin" {
			ownerTarget, lookupErr := workspaceMemberIsOwner(c, service, principal.WorkspaceID, userID)
			if lookupErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup workspace member"})
				return
			}
			if body.Role == "owner" || userID == principal.UserID || ownerTarget {
				c.JSON(http.StatusForbidden, gin.H{"error": "admin cannot manage owners or its own role"})
				return
			}
		}
		if err := service.UpdateMemberRole(c.Request.Context(), principal.WorkspaceID, userID, body.Role); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		service.Audit(c.Request.Context(), store.AuditEvent{WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Action: "workspace.member_role_updated", TargetType: "user", TargetID: strconv.FormatInt(userID, 10), RemoteAddr: c.ClientIP()})
		c.Status(http.StatusNoContent)
	}
}

func deleteWorkspaceMemberHandler(service *traioauth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, _ := currentPrincipal(c)
		userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
		if err != nil || userID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
			return
		}
		if principal.Role == "admin" {
			ownerTarget, lookupErr := workspaceMemberIsOwner(c, service, principal.WorkspaceID, userID)
			if lookupErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup workspace member"})
				return
			}
			if userID == principal.UserID || ownerTarget {
				c.JSON(http.StatusForbidden, gin.H{"error": "admin cannot remove itself or an owner"})
				return
			}
		}
		if err := service.DeleteMember(c.Request.Context(), principal.WorkspaceID, userID); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		service.Audit(c.Request.Context(), store.AuditEvent{WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Action: "workspace.member_removed", TargetType: "user", TargetID: strconv.FormatInt(userID, 10), RemoteAddr: c.ClientIP()})
		c.Status(http.StatusNoContent)
	}
}

func workspaceMemberIsOwner(c *gin.Context, service *traioauth.Service, workspaceID, userID int64) (bool, error) {
	members, err := service.ListMembers(c.Request.Context(), workspaceID)
	if err != nil {
		return false, err
	}
	for _, member := range members {
		if member.UserID == userID {
			return member.Role == "owner", nil
		}
	}
	return false, nil
}

func setAuthCookies(c *gin.Context, service *traioauth.Service, sessionToken, csrfToken string, expiresAt time.Time) {
	maxAge := int(time.Until(expiresAt).Seconds())
	setAuthCookieSameSite(c, service)
	c.SetCookie(service.CookieName(), sessionToken, maxAge, "/", "", service.CookieSecure(), true)
	c.SetCookie(service.CSRFCookieName(), csrfToken, maxAge, "/", "", service.CookieSecure(), false)
}

func clearAuthCookies(c *gin.Context, service *traioauth.Service) {
	setAuthCookieSameSite(c, service)
	c.SetCookie(service.CookieName(), "", -1, "/", "", service.CookieSecure(), true)
	c.SetCookie(service.CSRFCookieName(), "", -1, "/", "", service.CookieSecure(), false)
}

func setAuthCookieSameSite(c *gin.Context, service *traioauth.Service) {
	if service.CookieSecure() {
		c.SetSameSite(http.SameSiteNoneMode)
		return
	}
	c.SetSameSite(http.SameSiteLaxMode)
}

func unsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func requestHost(value string) string {
	if index := strings.LastIndex(value, ":"); index > -1 && !strings.Contains(value[index+1:], "]") {
		if host := strings.Trim(value[:index], "[]"); host != "" {
			return host
		}
	}
	return strings.Trim(value, "[]")
}

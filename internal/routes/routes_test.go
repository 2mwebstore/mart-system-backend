package routes

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/config"
	"com-mart/backend/internal/handlers"
	"com-mart/backend/internal/middleware"
	"com-mart/backend/internal/models"
	"com-mart/backend/internal/utils"
)

const testSecret = "unit-test-secret-unit-test-secret"

// The only routes reachable without signing in. Adding one has to be a decision
// someone makes here, not something that happens by forgetting a middleware.
var publicRoutes = map[string]bool{
	"GET /healthz":                             true,
	"POST /api/v1/auth/login":                  true,
	"POST /api/v1/auth/pin-login":              true,
	"POST /api/v1/auth/refresh":                true,
	"POST /api/v1/auth/logout":                 true,
	"GET /api/v1/auth/tills":                   true,
	"GET /api/v1/auth/branding":                true,
	"GET /api/v1/auth/branding/logo":           true,
	"GET /api/v1/public/tables/:token/menu":    true,
	"POST /api/v1/public/tables/:token/orders": true,
	"GET /api/v1/public/table-orders/:token":   true,
}

// The only signed-in routes with no permission gate: data every screen needs.
var openToAnyUser = map[string]bool{
	"GET /api/v1/auth/me":         true,
	"GET /api/v1/branches":        true,
	"GET /api/v1/payment-methods": true,
	"GET /api/v1/tax-rates":       true,
	"GET /api/v1/settings":        true,
	"GET /api/v1/exchange-rate":   true,
}

// A signed-in company user with an active account and no permissions at all.
func noPermissions(id, _ uint64) (*middleware.Principal, error) {
	return &middleware.Principal{UserID: id, CompanyID: 1, RoleID: 2, RoleName: "Nothing", Permissions: []string{}, BranchIDs: []uint64{1}}, nil
}

// router has no database: a route that reached a handler it shouldn't would
// panic, and the recovery middleware would turn that into a 500 the test catches.
func router(mutate func(*config.Config)) *gin.Engine {
	return routerFor(noPermissions, mutate)
}

func routerFor(principal middleware.PrincipalLoader, mutate func(*config.Config)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		AppEnv:             "test",
		JWTAccessSecret:    testSecret,
		JWTRefreshSecret:   "unused",
		CORSAllowedOrigins: []string{"https://admin.example.com"},
	}
	if mutate != nil {
		mutate(cfg)
	}
	return Setup(Dependencies{
		Config:          cfg,
		AuthHandler:     handlers.NewAuthHandler(nil),
		API:             &handlers.API{},
		PrincipalLoader: principal,
	})
}

var pathParam = regexp.MustCompile(`:[A-Za-z_]+`)

func do(r *gin.Engine, method, path, token string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestEveryRouteIsProtected(t *testing.T) {
	r := router(nil)
	token, err := utils.GenerateAccessToken(testSecret, time.Minute, utils.AccessClaims{UserID: 42})
	if err != nil {
		t.Fatal(err)
	}

	routes := r.Routes()
	if len(routes) < 100 {
		t.Fatalf("expected the full route table, got %d routes", len(routes))
	}
	declared := map[string]bool{}
	for _, route := range routes {
		id := route.Method + " " + route.Path
		declared[id] = true
		if publicRoutes[id] {
			continue
		}
		path := pathParam.ReplaceAllString(route.Path, "1")

		if w := do(r, route.Method, path, "", nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s without a token: want 401, got %d", id, w.Code)
		}
		if w := do(r, route.Method, path, "not.a.jwt", nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s with a bad token: want 401, got %d", id, w.Code)
		}
		if openToAnyUser[id] {
			continue
		}
		if w := do(r, route.Method, path, token, nil); w.Code != http.StatusForbidden {
			t.Errorf("%s with no permissions: want 403, got %d (is its permission gate missing?)", id, w.Code)
		}
	}
	for id := range publicRoutes {
		if !declared[id] {
			t.Errorf("public route %s is listed here but not declared any more", id)
		}
	}
	for id := range openToAnyUser {
		if !declared[id] {
			t.Errorf("open route %s is listed here but not declared any more", id)
		}
	}
}

// The super admin has no company: every company route refuses them, even
// the ones open to any company user, and only the /platform routes (with
// /auth/me) let them through.
func TestTheSuperAdminReachesNoCompanyRoute(t *testing.T) {
	superAdmin := func(id, _ uint64) (*middleware.Principal, error) {
		return &middleware.Principal{UserID: id, RoleID: 1, RoleName: models.RoleSuperAdmin, Permissions: []string{}}, nil
	}
	r := routerFor(superAdmin, nil)
	token, err := utils.GenerateAccessToken(testSecret, time.Minute, utils.AccessClaims{UserID: 42})
	if err != nil {
		t.Fatal(err)
	}
	platformRoutes := 0
	for _, route := range r.Routes() {
		id := route.Method + " " + route.Path
		if publicRoutes[id] || id == "GET /api/v1/auth/me" {
			continue
		}
		if strings.HasPrefix(route.Path, "/api/v1/platform/") {
			platformRoutes++
			continue
		}
		path := pathParam.ReplaceAllString(route.Path, "1")
		w := do(r, route.Method, path, token, nil)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "NO_COMPANY") {
			t.Errorf("%s as the super admin: want 403 NO_COMPANY, got %d %s", id, w.Code, w.Body.String())
		}
	}
	if platformRoutes < 10 {
		t.Fatalf("expected the /platform routes, found %d", platformRoutes)
	}
}

// Every /platform route refuses a company user, an Owner included, and a
// super admin's token from a till (there's no such thing, but a scoped
// session must never pass).
func TestPlatformRoutesAreTheSuperAdminsAlone(t *testing.T) {
	owner := func(id, _ uint64) (*middleware.Principal, error) {
		return &middleware.Principal{UserID: id, CompanyID: 1, RoleID: 3, RoleName: models.RoleOwner, Permissions: []string{"system.manage", "system.reset"}, BranchIDs: []uint64{1}}, nil
	}
	for name, loader := range map[string]middleware.PrincipalLoader{"an Owner": owner, "a user with no permissions": noPermissions} {
		r := routerFor(loader, nil)
		token, err := utils.GenerateAccessToken(testSecret, time.Minute, utils.AccessClaims{UserID: 42})
		if err != nil {
			t.Fatal(err)
		}
		for _, route := range r.Routes() {
			if !strings.HasPrefix(route.Path, "/api/v1/platform/") {
				continue
			}
			path := pathParam.ReplaceAllString(route.Path, "1")
			w := do(r, route.Method, path, token, nil)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "NOT_SUPER_ADMIN") {
				t.Errorf("%s as %s: want 403 NOT_SUPER_ADMIN, got %d", route.Method+" "+route.Path, name, w.Code)
			}
		}
	}
}

func TestPathTricksDontGetPastAuth(t *testing.T) {
	r := router(nil)
	for _, path := range []string{
		"/api/v1/users/", "/api/v1//users", "/API/V1/users", "/api/v1/Users", "/api/v1/users/.",
		"/api/v1/./users", "/api/v1/x/../users", "/api/v1/users%2f", "/api/v1/users%00", "/api/v1/users;x=1",
	} {
		req := httptest.NewRequest(http.MethodGet, "http://x/", nil)
		req.URL.Opaque = path
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Errorf("GET %s without a token returned 200", path)
		}
	}
	for _, method := range []string{http.MethodHead, http.MethodPatch, http.MethodPut, http.MethodDelete} {
		if w := do(r, method, "/api/v1/users", "", nil); w.Code == http.StatusOK {
			t.Errorf("%s /api/v1/users without a token returned 200", method)
		}
	}
}

func TestClientsCannotClaimAnotherAddress(t *testing.T) {
	r := router(func(c *config.Config) { c.AuthRateLimitPerMinute = 2 })
	login := func(mutate func(*http.Request)) int {
		return do(r, http.MethodPost, "/api/v1/auth/login", "", mutate).Code
	}
	spoof := func(ip string) func(*http.Request) {
		return func(req *http.Request) {
			req.Header.Set("X-Forwarded-For", ip)
			req.Header.Set("X-Real-IP", ip)
			req.Header.Set("CF-Connecting-IP", ip)
		}
	}
	// An empty body fails validation before any database work, and still counts.
	login(spoof("203.0.113.1"))
	login(spoof("203.0.113.2"))
	if code := login(spoof("203.0.113.3")); code != http.StatusTooManyRequests {
		t.Fatalf("changing forwarded headers must not reset the limit: want 429, got %d", code)
	}
}

func TestConfiguredProxyHeaderIsUsed(t *testing.T) {
	r := router(func(c *config.Config) {
		c.AuthRateLimitPerMinute = 1
		c.ClientIPHeader = "X-Real-IP"
	})
	from := func(ip string) func(*http.Request) {
		return func(req *http.Request) { req.Header.Set("X-Real-IP", ip) }
	}
	do(r, http.MethodPost, "/api/v1/auth/login", "", from("198.51.100.1"))
	if w := do(r, http.MethodPost, "/api/v1/auth/login", "", from("198.51.100.2")); w.Code == http.StatusTooManyRequests {
		t.Fatal("a second client behind the proxy should have its own allowance")
	}
	if w := do(r, http.MethodPost, "/api/v1/auth/login", "", from("198.51.100.1")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the first client is over its limit: want 429, got %d", w.Code)
	}
}

func TestRateLimitsCoverEverySignInRoute(t *testing.T) {
	limits := map[string]int{"login": 1, "pin-login": 1, "refresh": 2, "logout": 2}
	for route, allowed := range limits {
		r := router(func(c *config.Config) { c.AuthRateLimitPerMinute = 1 })
		path := "/api/v1/auth/" + route
		for i := 0; i < allowed; i++ {
			if w := do(r, http.MethodPost, path, "", nil); w.Code == http.StatusTooManyRequests {
				t.Fatalf("%s: request %d of %d was limited", route, i+1, allowed)
			}
		}
		if w := do(r, http.MethodPost, path, "", nil); w.Code != http.StatusTooManyRequests {
			t.Errorf("%s: want 429 after %d requests, got %d", route, allowed, w.Code)
		}
	}
}

func TestPublicBodiesAreCapped(t *testing.T) {
	r := router(nil)
	big := `{"username":"` + strings.Repeat("x", maxPublicBodyBytes) + `"}`
	send := func(knownLength bool) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(big))
		req.Header.Set("Content-Type", "application/json")
		if !knownLength {
			req.ContentLength = -1
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	if code := send(true); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("declared length: want 413, got %d", code)
	}
	if code := send(false); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked: want 413, got %d", code)
	}
}

func TestBindErrorsDontExposeInternals(t *testing.T) {
	r := router(nil)
	for _, body := range []string{`{"username":1,"password":2}`, `{"username":`, `[]`} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: want 422, got %d", body, w.Code)
		}
		for _, leak := range []string{"Go struct", "unmarshal", "LoginRequest", "dto."} {
			if strings.Contains(w.Body.String(), leak) {
				t.Errorf("%s: response exposes %q: %s", body, leak, w.Body.String())
			}
		}
	}
}

func TestSecurityHeadersOnEveryKindOfResponse(t *testing.T) {
	r := router(nil)
	for _, path := range []string{"/api/v1/users", "/api/v1/no-such-route"} {
		w := do(r, http.MethodGet, path, "", nil)
		if w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: security headers missing (status %d)", path, w.Code)
		}
	}
}

func TestCORS(t *testing.T) {
	r := router(nil)
	origin := func(o string) *httptest.ResponseRecorder {
		return do(r, http.MethodGet, "/api/v1/users", "", func(req *http.Request) { req.Header.Set("Origin", o) })
	}
	if got := origin("https://admin.example.com").Header().Get("Access-Control-Allow-Origin"); got != "https://admin.example.com" {
		t.Fatalf("configured origin should be allowed exactly, got %q", got)
	}
	for _, bad := range []string{"https://evil.example", "null", "https://admin.example.com.evil.example", "http://admin.example.com"} {
		if got := origin(bad).Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("origin %q was allowed: %q", bad, got)
		}
	}

	// The admin app names its active branch on every call.
	preflight := do(r, http.MethodOptions, "/api/v1/users", "", func(req *http.Request) {
		req.Header.Set("Origin", "https://admin.example.com")
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		req.Header.Set("Access-Control-Request-Headers", "authorization,x-branch-id")
	})
	if got := strings.ToLower(preflight.Header().Get("Access-Control-Allow-Headers")); !strings.Contains(got, "x-branch-id") {
		t.Errorf("X-Branch-Id should be allowed, got %q", got)
	}
}

// Integration tests: real HTTP requests through the real router into a real
// MySQL. They need a database that exists only for tests, because they drop
// every table in it. Without one they skip, so `go test ./...` stays green on a
// machine with no Docker running; CI sets REQUIRE_TEST_DB=1 so that a broken
// database connection is a failure instead of a silent skip.
//
// `make test-integration` starts a throwaway MySQL and runs them.
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"com-mart/backend/internal/config"
	"com-mart/backend/internal/database"
	"com-mart/backend/internal/handlers"
	"com-mart/backend/internal/models"
	"com-mart/backend/internal/repositories"
	"com-mart/backend/internal/routes"
	"com-mart/backend/internal/services"
)

const (
	testPassword = "correct-horse-battery"
	testPIN      = "4321"
)

var allPermissions = []string{
	"pos.sell", "pos.discount", "pos.void", "pos.edit_sale", "pos.refund", "pos.open_drawer",
	"shift.own", "shift.close_others", "shift.see_expected",
	"product.view", "product.create", "product.update", "product.delete",
	"category.view", "category.create", "category.update", "category.delete",
	"supplier.view", "supplier.create", "supplier.update", "supplier.delete",
	"inventory.view", "inventory.adjust", "inventory.transfer",
	"purchase_order.view", "purchase_order.create", "purchase_order.update", "purchase_order.delete", "purchase_order.receive",
	"customer.view", "customer.create", "customer.update", "customer.delete",
	"invoice.view", "invoice.create", "invoice.update", "invoice.delete",
	"expense.view", "expense.create", "expense.update", "expense.delete",
	"report.sales", "report.profit_loss", "report.export", "report.activity_log",
	"user.view", "user.create", "user.update", "user.delete",
	"role.view", "role.create", "role.update", "role.delete",
	"branch.view", "branch.create", "branch.update", "branch.delete",
	"table.view", "table.create", "table.update", "table.delete",
	"payment_method.create", "payment_method.update", "payment_method.delete",
	"tax_rate.create", "tax_rate.update", "tax_rate.delete",
	"settings.rate", "settings.company", "system.manage", "system.reset",
}

var managerPermissions = []string{
	"pos.sell", "pos.discount", "pos.void", "pos.edit_sale", "pos.refund", "pos.open_drawer",
	"shift.own", "shift.close_others", "shift.see_expected",
	"product.view", "product.create", "product.update", "product.delete",
	"category.view", "category.create", "category.update", "category.delete",
	"supplier.view", "supplier.create", "supplier.update", "supplier.delete",
	"purchase_order.view", "purchase_order.create", "purchase_order.update", "purchase_order.delete", "purchase_order.receive",
	"inventory.view", "inventory.adjust", "inventory.transfer",
	"report.sales", "report.export", "report.activity_log",
	"customer.view", "customer.create", "customer.update", "customer.delete",
	"expense.view", "expense.create", "expense.update", "expense.delete",
	"invoice.view", "invoice.create", "invoice.update", "invoice.delete",
}

var cashierPermissions = []string{
	"pos.sell", "shift.own", "product.view", "category.view", "inventory.view",
	"customer.view", "customer.create", "customer.update",
}

// env is built once in TestMain. skipReason says why it's nil.
var (
	env        *testEnv
	skipReason string
	uniqueSeq  atomic.Int64
)

type testEnv struct {
	db     *gorm.DB
	cfg    *config.Config
	router *gin.Engine
	auth   *services.AuthService

	passwordHash string
	pinHash      string

	// Every fixture below is company A's. Company B is another business on
	// the same install, there to prove nothing crosses between them.
	companyA     uint64
	codeA        string
	b            otherCompany
	superAdmin   namedUser
	roleSuperAdm uint64

	branchTK, branchBKK                 uint64
	tillTK, tillTK2, tillBKK            string
	roleOwner, roleManager, roleCashier uint64
	owner, managerTK, managerBKK        namedUser
	cashierTK, disabledCashier          namedUser
	product, supplier, customer         uint64
}

type namedUser struct {
	ID       uint64
	Username string
}

// otherCompany is company B: the same kinds of things company A has.
type otherCompany struct {
	id, branch                    uint64
	code, till                    string
	roleOwner, roleManager        uint64
	owner, manager                namedUser
	product, supplier, customer   uint64
	category, paymentMethod, rate uint64
}

func TestMain(m *testing.M) {
	zerolog.SetGlobalLevel(zerolog.Disabled)
	gin.SetMode(gin.TestMode)

	built, err := buildEnv()
	if err != nil {
		if os.Getenv("REQUIRE_TEST_DB") == "1" {
			fmt.Fprintln(os.Stderr, "integration tests can't run:", err)
			os.Exit(1)
		}
		skipReason = err.Error()
	}
	env = built
	os.Exit(m.Run())
}

// setup skips without a test database and clears the lockout state, which is
// shared by every test.
func setup(t *testing.T) *testEnv {
	t.Helper()
	if env == nil {
		t.Skipf("skipping integration test: %s", skipReason)
	}
	if err := env.db.Exec("DELETE FROM login_attempts").Error; err != nil {
		t.Fatal(err)
	}
	return env
}

func buildEnv() (*testEnv, error) {
	// These tests sign their own tokens; they don't need real secrets.
	for name, value := range map[string]string{
		"JWT_ACCESS_SECRET":  "integration-test-access-secret-0123456789",
		"JWT_REFRESH_SECRET": "integration-test-refresh-secret-0123456789",
	} {
		if os.Getenv(name) == "" {
			os.Setenv(name, value)
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("config not loadable: %w", err)
	}
	// Everything in the database named here is about to be dropped.
	if !strings.HasSuffix(cfg.DBName, "_test") {
		return nil, fmt.Errorf("DB_NAME %q doesn't end in _test", cfg.DBName)
	}
	if cfg.DBHost != "127.0.0.1" && cfg.DBHost != "localhost" {
		return nil, fmt.Errorf("DB_HOST %q isn't local", cfg.DBHost)
	}
	cfg.AppEnv = "test"
	cfg.LoginMaxAttempts = 5
	cfg.LoginLockoutMinutes = 5
	cfg.AuthRateLimitPerMinute = 0 // has its own tests; it would trip all the rest
	cfg.CORSAllowedOrigins = []string{"https://admin.example.com"}

	loc, err := time.LoadLocation(cfg.AppTimezone)
	if err != nil {
		return nil, err
	}
	time.Local = loc

	db, err := database.Connect(cfg)
	if err != nil {
		return nil, fmt.Errorf("no test database reachable: %w", err)
	}
	if err := dropAllTables(db); err != nil {
		return nil, fmt.Errorf("resetting the test database: %w", err)
	}
	if err := database.Migrate(cfg); err != nil {
		return nil, fmt.Errorf("migrating the test database: %w", err)
	}

	authService := services.NewAuthService(
		cfg,
		repositories.NewUserRepository(db),
		repositories.NewRefreshTokenRepository(db),
		repositories.NewDeviceRepository(db),
		repositories.NewBranchRepository(db),
		repositories.NewLoginAttemptRepository(db),
	)
	e := &testEnv{db: db, cfg: cfg, auth: authService}
	e.router = routes.Setup(routes.Dependencies{
		Config:      cfg,
		AuthHandler: handlers.NewAuthHandler(authService),
		API:         handlers.NewAPI(db, cfg),
	})
	if err := e.seed(); err != nil {
		return nil, fmt.Errorf("building fixtures: %w", err)
	}
	return e, nil
}

func dropAllTables(db *gorm.DB) error {
	return db.Connection(func(conn *gorm.DB) error {
		var tables []string
		if err := conn.Raw("SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()").Scan(&tables).Error; err != nil {
			return err
		}
		if err := conn.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
			return err
		}
		for _, table := range tables {
			if err := conn.Exec("DROP TABLE `" + table + "`").Error; err != nil {
				return err
			}
		}
		return conn.Exec("SET FOREIGN_KEY_CHECKS = 1").Error
	})
}

// seed builds two small businesses from scratch. Company A has two branches,
// tills, the standard roles, a few people and something to sell; company B has
// one of each, using the same names and codes wherever they're per company.
// And a super admin above both.
func (e *testEnv) seed() error {
	hash := func(s string) (string, error) {
		b, err := bcrypt.GenerateFromPassword([]byte(s), bcrypt.DefaultCost)
		return string(b), err
	}
	var err error
	if e.passwordHash, err = hash(testPassword); err != nil {
		return err
	}
	if e.pinHash, err = hash(testPIN); err != nil {
		return err
	}

	for _, key := range allPermissions {
		p := models.Permission{Key: key, Group: strings.SplitN(key, ".", 2)[0]}
		if err := e.db.Where("`key` = ?", key).FirstOrCreate(&p).Error; err != nil {
			return err
		}
	}

	if e.companyA, err = createCompany(e.db, "Test Company A", testCompanyCode); err != nil {
		return err
	}
	e.codeA = testCompanyCode
	if err := e.db.Raw("SELECT id FROM roles WHERE company_id IS NULL AND name = ?", models.RoleSuperAdmin).Scan(&e.roleSuperAdm).Error; err != nil || e.roleSuperAdm == 0 {
		return fmt.Errorf("no Super admin role from the migration: %v", err)
	}
	if e.superAdmin, err = e.createUser("test.superadmin", e.roleSuperAdm, true); err != nil {
		return err
	}

	tk := models.Branch{CompanyID: e.companyA, Name: "Toul Kork", Code: "TK", Active: true}
	bkk := models.Branch{CompanyID: e.companyA, Name: "BKK1", Code: "BKK1", Active: true}
	if err := e.db.Create(&tk).Error; err != nil {
		return err
	}
	if err := e.db.Create(&bkk).Error; err != nil {
		return err
	}
	e.branchTK, e.branchBKK = tk.ID, bkk.ID

	e.tillTK, e.tillTK2, e.tillBKK = "TEST-TK-1", "TEST-TK-2", "TEST-BKK-1"
	for key, branch := range map[string]uint64{e.tillTK: tk.ID, e.tillTK2: tk.ID, e.tillBKK: bkk.ID} {
		if err := e.db.Create(&models.Device{CompanyID: e.companyA, BranchID: branch, Name: key, DeviceKey: key, Active: true}).Error; err != nil {
			return err
		}
	}

	if e.roleOwner, err = e.createRole(models.RoleOwner, true, 100, allPermissions); err != nil {
		return err
	}
	if e.roleManager, err = e.createRole("Manager", false, 20, managerPermissions); err != nil {
		return err
	}
	if e.roleCashier, err = e.createRole("Cashier", false, 5, cashierPermissions); err != nil {
		return err
	}

	create := func(username string, role uint64, active bool, branches ...uint64) (namedUser, error) {
		u, err := e.createUser(username, role, active, branches...)
		return u, err
	}
	if e.owner, err = create("test.owner", e.roleOwner, true); err != nil {
		return err
	}
	if e.managerTK, err = create("test.manager.tk", e.roleManager, true, tk.ID); err != nil {
		return err
	}
	if e.managerBKK, err = create("test.manager.bkk", e.roleManager, true, bkk.ID); err != nil {
		return err
	}
	if e.cashierTK, err = create("test.cashier.tk", e.roleCashier, true, tk.ID); err != nil {
		return err
	}
	if e.disabledCashier, err = create("test.cashier.disabled", e.roleCashier, false, tk.ID); err != nil {
		return err
	}

	if err := e.db.Create(&models.ExchangeRate{CompanyID: e.companyA, RateRielPerUSD: 4100, SetByUserID: e.owner.ID, EffectiveAt: time.Now()}).Error; err != nil {
		return err
	}

	supplier := models.Supplier{CompanyID: e.companyA, Name: "Test Supplier"}
	if err := e.db.Create(&supplier).Error; err != nil {
		return err
	}
	e.supplier = supplier.ID
	phone := "012000000"
	customer := models.Customer{CompanyID: e.companyA, Name: "Test Customer", Phone: phone, Tier: "MEMBER"}
	if err := e.db.Create(&customer).Error; err != nil {
		return err
	}
	e.customer = customer.ID
	product := models.Product{CompanyID: e.companyA, SKU: "TEST-1", NameEn: "Test product", Unit: "pcs", Type: models.ProductType("STANDARD"), Active: true}
	if err := e.db.Create(&product).Error; err != nil {
		return err
	}
	e.product = product.ID
	if err := e.db.Create(&models.ProductPrice{ProductID: product.ID, PriceCents: 1000, CostCents: 500, EffectiveAt: time.Now().Add(-time.Hour)}).Error; err != nil {
		return err
	}
	for _, branch := range []uint64{tk.ID, bkk.ID} {
		if err := e.db.Create(&models.BranchStock{BranchID: branch, ProductID: product.ID, Qty: 100000}).Error; err != nil {
			return err
		}
	}
	return e.seedCompanyB()
}

// seedCompanyB gives company B a branch, a till, an Owner and a manager, and
// one of each catalog and reference row, with company A's codes: branch "TK",
// SKU "TEST-1", phone "012000000" and role names are all per company.
func (e *testEnv) seedCompanyB() error {
	b := &e.b
	var err error
	b.code = "TB"
	if b.id, err = createCompany(e.db, "Test Company B", b.code); err != nil {
		return err
	}
	branch := models.Branch{CompanyID: b.id, Name: "B Toul Kork", Code: "TK", Active: true}
	if err := e.db.Create(&branch).Error; err != nil {
		return err
	}
	b.branch = branch.ID
	b.till = "TEST-TK-1"
	if err := e.db.Create(&models.Device{CompanyID: b.id, BranchID: b.branch, Name: "B till", DeviceKey: b.till, Active: true}).Error; err != nil {
		return err
	}
	if b.roleOwner, err = e.createRoleIn(b.id, models.RoleOwner, true, 100, allPermissions); err != nil {
		return err
	}
	if b.roleManager, err = e.createRoleIn(b.id, "Manager", false, 20, managerPermissions); err != nil {
		return err
	}
	if b.owner, err = e.createUser("test.b.owner", b.roleOwner, true); err != nil {
		return err
	}
	if b.manager, err = e.createUser("test.b.manager", b.roleManager, true, b.branch); err != nil {
		return err
	}
	rate := models.ExchangeRate{CompanyID: b.id, RateRielPerUSD: 4000, SetByUserID: b.owner.ID, EffectiveAt: time.Now()}
	if err := e.db.Create(&rate).Error; err != nil {
		return err
	}
	b.rate = rate.ID
	category := models.Category{CompanyID: b.id, NameEn: "B category"}
	if err := e.db.Create(&category).Error; err != nil {
		return err
	}
	b.category = category.ID
	supplier := models.Supplier{CompanyID: b.id, Name: "B Supplier"}
	if err := e.db.Create(&supplier).Error; err != nil {
		return err
	}
	b.supplier = supplier.ID
	customer := models.Customer{CompanyID: b.id, Name: "B Customer", Phone: "012000000", Tier: "MEMBER"}
	if err := e.db.Create(&customer).Error; err != nil {
		return err
	}
	b.customer = customer.ID
	method := models.PaymentMethodRow{CompanyID: b.id, Name: "B Cash", Type: "CASH", Enabled: true}
	if err := e.db.Create(&method).Error; err != nil {
		return err
	}
	b.paymentMethod = method.ID
	product := models.Product{CompanyID: b.id, SKU: "TEST-1", NameEn: "B product", Unit: "pcs", Type: models.ProductType("STANDARD"), Active: true}
	if err := e.db.Create(&product).Error; err != nil {
		return err
	}
	b.product = product.ID
	if err := e.db.Create(&models.ProductPrice{ProductID: product.ID, PriceCents: 700, CostCents: 300, EffectiveAt: time.Now().Add(-time.Hour)}).Error; err != nil {
		return err
	}
	return e.db.Create(&models.BranchStock{BranchID: b.branch, ProductID: product.ID, Qty: 100000}).Error
}

// createCompany adds a company with its own document numbering, as the
// platform's company creation does.
func createCompany(db *gorm.DB, name, code string) (uint64, error) {
	company := models.Company{Name: name, Code: code, Active: true}
	if err := db.Create(&company).Error; err != nil {
		return 0, err
	}
	for docType, prefix := range map[string]string{"SALE": "INV", "EXPENSE": "EXP", "PURCHASE_ORDER": "PO", "INVOICE": "SI"} {
		if err := db.Create(&models.NumberSequence{CompanyID: company.ID, DocType: docType, Prefix: prefix, NextNumber: 1}).Error; err != nil {
			return 0, err
		}
	}
	return company.ID, nil
}

// companyOfBranch is the company a branch belongs to.
func (e *testEnv) companyOfBranch(branch uint64) (uint64, error) {
	var id uint64
	err := e.db.Raw("SELECT company_id FROM branches WHERE id = ?", branch).Scan(&id).Error
	return id, err
}

// createRole makes a role in company A.
func (e *testEnv) createRole(name string, locked bool, maxDiscount int, permissions []string) (uint64, error) {
	return e.createRoleIn(e.companyA, name, locked, maxDiscount, permissions)
}

func (e *testEnv) createRoleIn(company uint64, name string, locked bool, maxDiscount int, permissions []string) (uint64, error) {
	role := models.Role{CompanyID: &company, Name: name, IsLocked: locked}
	if err := e.db.Omit("Permissions", "Limits").Create(&role).Error; err != nil {
		return 0, err
	}
	if err := e.db.Create(&models.RoleLimit{RoleID: role.ID, MaxDiscountPercent: maxDiscount}).Error; err != nil {
		return 0, err
	}
	if len(permissions) > 0 {
		var perms []models.Permission
		if err := e.db.Where("`key` IN ?", permissions).Find(&perms).Error; err != nil {
			return 0, err
		}
		for _, p := range perms {
			if err := e.db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: p.ID}).Error; err != nil {
				return 0, err
			}
		}
	}
	return role.ID, nil
}

// createUser makes an account in the company its role belongs to (none for
// the Super admin role).
func (e *testEnv) createUser(username string, role uint64, active bool, branches ...uint64) (namedUser, error) {
	pin := e.pinHash
	var company *uint64
	if err := e.db.Raw("SELECT company_id FROM roles WHERE id = ?", role).Scan(&company).Error; err != nil {
		return namedUser{}, err
	}
	user := models.User{CompanyID: company, FullName: username, Username: username, RoleID: role, PasswordHash: e.passwordHash, PINHash: &pin, Active: active}
	if err := e.db.Omit("Role", "Branches").Create(&user).Error; err != nil {
		return namedUser{}, err
	}
	for _, branch := range branches {
		if err := e.db.Create(&models.UserBranch{UserID: user.ID, BranchID: branch}).Error; err != nil {
			return namedUser{}, err
		}
	}
	return namedUser{ID: user.ID, Username: username}, nil
}

func unique(prefix string) string {
	return fmt.Sprintf("%s.%d", prefix, uniqueSeq.Add(1))
}

// newRole makes a role for one test.
func newRole(t *testing.T, permissions ...string) uint64 {
	t.Helper()
	id, err := env.createRole(unique("role"), false, 0, permissions)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// newUser makes an account for one test and retires it afterwards, so that no
// later test finds an extra active Owner.
func newUser(t *testing.T, role uint64, branches ...uint64) namedUser {
	t.Helper()
	user, err := env.createUser(unique("user"), role, true, branches...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		env.db.Exec("UPDATE users SET active = 0, deleted_at = ? WHERE id = ?", time.Now(), user.ID)
	})
	return user
}

// newTill makes a till for one test: a till holds one open shift at a time.
func newTill(t *testing.T, branch uint64) string {
	t.Helper()
	key := unique("TILL")
	company, err := env.companyOfBranch(branch)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.Create(&models.Device{CompanyID: company, BranchID: branch, Name: key, DeviceKey: key, Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	return key
}

type response struct {
	Status int
	Raw    string
	Header http.Header
	Data   json.RawMessage
	Error  *struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields"`
	}
	Meta map[string]interface{}
}

func (r response) String() string { return fmt.Sprintf("%d %s", r.Status, r.Raw) }

func (r response) code() string {
	if r.Error == nil {
		return ""
	}
	return r.Error.Code
}

func (r response) field(t *testing.T, name string) interface{} {
	t.Helper()
	var data map[string]interface{}
	if err := json.Unmarshal(r.Data, &data); err != nil {
		t.Fatalf("response isn't an object: %s", r)
	}
	return data[name]
}

func (r response) id(t *testing.T) uint64 {
	t.Helper()
	v, ok := r.field(t, "id").(float64)
	if !ok {
		t.Fatalf("response has no id: %s", r)
	}
	return uint64(v)
}

func (r response) rows(t *testing.T) []map[string]interface{} {
	t.Helper()
	var rows []map[string]interface{}
	if err := json.Unmarshal(r.Data, &rows); err != nil {
		t.Fatalf("response isn't a list: %s", r)
	}
	return rows
}

// call sends one request through the real router. A path starting with "/api"
// or "/healthz" is used as it is; anything else is under /api/v1.
func call(t *testing.T, method, path, token string, body interface{}, headers ...string) response {
	t.Helper()
	var reader *bytes.Reader
	switch b := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case []byte:
		reader = bytes.NewReader(b)
	default:
		encoded, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	if !strings.HasPrefix(path, "/api") && !strings.HasPrefix(path, "/healthz") {
		path = "/api/v1" + path
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)

	r := response{Status: w.Code, Raw: w.Body.String(), Header: w.Header()}
	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Fields  map[string]string `json:"fields"`
		} `json:"error"`
		Meta map[string]interface{} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err == nil {
		r.Data, r.Error, r.Meta = envelope.Data, envelope.Error, envelope.Meta
	}
	return r
}

func wantStatus(t *testing.T, r response, status int) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("want %d, got %s", status, r)
	}
}

func wantDenied(t *testing.T, r response, status int, code string) {
	t.Helper()
	if r.Status != status || r.code() != code {
		t.Fatalf("want %d %s, got %s", status, code, r)
	}
}

type session struct {
	Access      string   `json:"access_token"`
	Refresh     string   `json:"refresh_token"`
	Permissions []string `json:"permissions"`
	User        struct {
		ID       uint64 `json:"id"`
		RoleName string `json:"role_name"`
	} `json:"user"`
}

func decodeSession(t *testing.T, r response) session {
	t.Helper()
	wantStatus(t, r, http.StatusOK)
	var s session
	if err := json.Unmarshal(r.Data, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func tryLogin(t *testing.T, username, password string, headers ...string) response {
	t.Helper()
	return call(t, http.MethodPost, "/auth/login", "", gin.H{"username": username, "password": password}, headers...)
}

func login(t *testing.T, user namedUser) session {
	t.Helper()
	return decodeSession(t, tryLogin(t, user.Username, testPassword))
}

// testCompanyCode is company A's: every till test signs in there unless it
// says otherwise with tryPINAt.
const testCompanyCode = "TA"

func tryPIN(t *testing.T, till, username, pin string) response {
	t.Helper()
	return tryPINAt(t, testCompanyCode, till, username, pin)
}

func tryPINAt(t *testing.T, companyCode, till, username, pin string) response {
	t.Helper()
	return call(t, http.MethodPost, "/auth/pin-login", "", gin.H{"company_code": companyCode, "device_key": till, "username": username, "pin": pin})
}

func tryRefresh(t *testing.T, refreshToken string) response {
	t.Helper()
	return call(t, http.MethodPost, "/auth/refresh", "", gin.H{"refresh_token": refreshToken})
}

// updateBody is what the Users screen sends: everything, every time.
func updateBody(username string, role uint64, active bool, branches []uint64, extra ...string) gin.H {
	body := gin.H{"full_name": username, "username": username, "role_id": role, "branch_ids": branches, "active": active}
	for i := 0; i+1 < len(extra); i += 2 {
		body[extra[i]] = extra[i+1]
	}
	return body
}

func today() string { return time.Now().Format("2006-01-02") }

func dbInt(t *testing.T, query string, args ...interface{}) int64 {
	t.Helper()
	var n int64
	if err := env.db.Raw(query, args...).Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

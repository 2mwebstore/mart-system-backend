package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func respond(t *testing.T, err error) (int, string, ErrorBody) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	RespondError(c, err)
	var env struct {
		Error ErrorBody `json:"error"`
	}
	if jsonErr := json.Unmarshal(w.Body.Bytes(), &env); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	return w.Code, w.Body.String(), env.Error
}

func TestRespondErrorHidesUnexpectedErrors(t *testing.T) {
	secret := "Error 1452: Cannot add a foreign key constraint on `com_mart`.`users` at /srv/app/db.go"
	code, body, e := respond(t, errors.New(secret))
	if code != http.StatusInternalServerError || e.Code != "INTERNAL_ERROR" {
		t.Fatalf("got %d %s", code, e.Code)
	}
	for _, leak := range []string{"1452", "foreign key", "com_mart", "/srv"} {
		if strings.Contains(body, leak) {
			t.Errorf("response leaks %q: %s", leak, body)
		}
	}
}

func TestRespondErrorKeepsDomainErrors(t *testing.T) {
	code, _, e := respond(t, ErrForbidden)
	if code != http.StatusForbidden || e.Code != "FORBIDDEN" {
		t.Fatalf("got %d %s", code, e.Code)
	}
}

func TestRespondErrorFindsDomainErrorsWhenWrapped(t *testing.T) {
	cases := map[string]error{
		"wrapped": fmt.Errorf("saving: %w", ErrForbidden),
		"joined":  errors.Join(nil, ErrForbidden, ErrNotFound),
	}
	for name, err := range cases {
		code, _, e := respond(t, err)
		if code != http.StatusForbidden || e.Code != "FORBIDDEN" {
			t.Errorf("%s: got %d %s", name, code, e.Code)
		}
	}
}

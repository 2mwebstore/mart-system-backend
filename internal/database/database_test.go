package database

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

const loggedSQL = "SELECT * FROM users WHERE username = 'typed-by-a-stranger'"

func traced(production bool, err error) string {
	var out bytes.Buffer
	l := newLogger(log.New(&out, "", 0), production)
	l.Trace(context.Background(), time.Now(), func() (string, int64) { return loggedSQL, 0 }, err)
	return out.String()
}

func TestProductionDoesNotLogARowNotBeingFound(t *testing.T) {
	if got := traced(true, gorm.ErrRecordNotFound); got != "" {
		t.Errorf("logged a failed lookup: %s", got)
	}
}

func TestDevelopmentLogsEveryQuery(t *testing.T) {
	if got := traced(false, nil); !strings.Contains(got, "typed-by-a-stranger") {
		t.Errorf("query wasn't logged: %q", got)
	}
}

func TestProductionLogsErrorsButNotQuietQueries(t *testing.T) {
	if got := traced(true, errors.New("connection lost")); !strings.Contains(got, "connection lost") {
		t.Errorf("error wasn't logged: %q", got)
	}
	if got := traced(true, nil); got != "" {
		t.Errorf("an ordinary query was logged: %q", got)
	}
}

func TestProductionLeavesQueryValuesOutOfTheLogs(t *testing.T) {
	filter := func(production bool) []interface{} {
		l, ok := newLogger(log.New(&bytes.Buffer{}, "", 0), production).(gorm.ParamsFilter)
		if !ok {
			t.Fatal("logger can't filter values")
		}
		_, values := l.ParamsFilter(context.Background(), "SELECT ?", "a-password-hash")
		return values
	}
	if got := filter(true); len(got) != 0 {
		t.Errorf("production kept the values: %v", got)
	}
	if got := filter(false); len(got) != 1 {
		t.Errorf("development lost the values: %v", got)
	}
}

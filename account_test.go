package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func accountTestMux(s *server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/register", s.register)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/reset-password", s.resetPassword)
	mux.HandleFunc("POST /api/user/password", s.requireLevel(1, s.changePassword))
	mux.HandleFunc("POST /api/user/delete", s.requireLevel(1, s.deleteAccount))
	return mux
}

func accountRequest(t *testing.T, h http.Handler, path, token string, body map[string]string, status int) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", path, strings.NewReader(string(data)))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s: got %d want %d: %s", path, w.Code, status, w.Body.String())
	}
	return w
}

func insertTestEmailCode(t *testing.T, s *server, email, purpose string) {
	t.Helper()
	mustExec(t, s.appDB, `insert into email_verification_codes(email,purpose,code_hash,expires_at,created_at) values(?,?,?,?,?)`, email, purpose, emailCodeHash(email, purpose, "123456"), appTime(time.Now().Add(time.Hour)), appNow())
}

func TestPasswordConfirmation(t *testing.T) {
	s, _ := testServer(t)
	h := accountTestMux(s)
	password := "LocalPassword_123"
	registerBody := map[string]string{"username": "confirm_user", "email": "confirm@example.invalid", "password": password, "code": "123456"}
	insertTestEmailCode(t, s, registerBody["email"], "register")
	for _, confirmation := range []string{"", "DifferentPassword_123"} {
		registerBody["confirm_password"] = confirmation
		accountRequest(t, h, "/api/auth/register", "", registerBody, 400)
	}
	var count int
	if err := s.appDB.QueryRow("select count(*) from users").Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid registration wrote user", err)
	}
	registerBody["confirm_password"] = password
	w := accountRequest(t, h, "/api/auth/register", "", registerBody, 200)
	token := w.Result().Cookies()[0].Value
	newPassword := "NewLocalPassword_456"
	changeBody := map[string]string{"old_password": password, "new_password": newPassword}
	for _, confirmation := range []string{"", "WrongPassword_123"} {
		changeBody["confirm_password"] = confirmation
		accountRequest(t, h, "/api/user/password", token, changeBody, 400)
	}
	accountRequest(t, h, "/api/auth/login", "", map[string]string{"login": "confirm_user", "password": password}, 200)
	changeBody["confirm_password"] = newPassword
	accountRequest(t, h, "/api/user/password", token, changeBody, 200)
	accountRequest(t, h, "/api/auth/login", "", map[string]string{"login": "confirm_user", "password": password}, 401)
	accountRequest(t, h, "/api/auth/login", "", map[string]string{"login": "confirm_user", "password": newPassword}, 200)
	insertTestEmailCode(t, s, registerBody["email"], "reset_password")
	resetBody := map[string]string{"email": registerBody["email"], "password": password, "code": "123456"}
	for _, confirmation := range []string{"", "WrongPassword_123"} {
		resetBody["confirm_password"] = confirmation
		accountRequest(t, h, "/api/auth/reset-password", "", resetBody, 400)
	}
	resetBody["confirm_password"] = password
	accountRequest(t, h, "/api/auth/reset-password", "", resetBody, 200)
	accountRequest(t, h, "/api/auth/login", "", map[string]string{"login": "confirm_user", "password": password}, 200)
}

func TestAccountCancellationPreservesHistory(t *testing.T) {
	s, resourceHandler := testServer(t)
	h := accountTestMux(s)
	token := testUser(t, s, 1, 1)
	password := "LocalPassword_123"
	hash, err := hashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, s.appDB, "update users set password_hash=? where id=1", hash)
	tx, err := s.appDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.grantMembershipTx(tx, 1, "vip_month", "test", "cancel-test"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, resourceHandler, "POST", "/api/resources/video/v1/unlock", token, 200)
	insertTestEmailCode(t, s, "test1@example.invalid", "reset_password")
	counts := map[string]int{}
	for _, table := range []string{"point_accounts", "point_lots", "point_records", "point_operations", "user_memberships", "vip_point_cycles", "orders", "resource_unlocks", "user_sessions"} {
		var count int
		if err = s.appDB.QueryRow("select count(*) from " + table + " where user_id=1").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatalf("missing fixture rows in %s", table)
		}
		counts[table] = count
	}
	accountRequest(t, h, "/api/user/delete", token, map[string]string{"password": "incorrect"}, 400)
	requireStatus(t, resourceHandler, "GET", "/api/cases/v1", token, 200)
	w := accountRequest(t, h, "/api/user/delete", token, map[string]string{"password": password}, 200)
	if w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("session cookie not cleared")
	}
	var flag int
	var deleted sql.NullString
	if err = s.appDB.QueryRow("select delete_flag,deleted_at from users where id=1").Scan(&flag, &deleted); err != nil || flag != 1 || !deleted.Valid {
		t.Fatal("account not soft deleted", err)
	}
	for table, before := range counts {
		var count int
		if err = s.appDB.QueryRow("select count(*) from " + table + " where user_id=1").Scan(&count); err != nil || count != before {
			t.Fatalf("%s history changed: got %d want %d: %v", table, count, before, err)
		}
	}
	var usable int
	if err = s.appDB.QueryRow("select count(*) from user_sessions where user_id=1 and revoked_at is null").Scan(&usable); err != nil || usable != 0 {
		t.Fatal("sessions not revoked", err)
	}
	if err = s.appDB.QueryRow("select count(*) from email_verification_codes where email='test1@example.invalid' and used_at is null").Scan(&usable); err != nil || usable != 0 {
		t.Fatal("email codes not invalidated", err)
	}
	accountRequest(t, h, "/api/auth/login", "", map[string]string{"login": "user1", "password": password}, 401)
	requireStatus(t, resourceHandler, "GET", "/api/cases/v1", token, 401)
	if _, err = s.adminLoadUser(1); err != sql.ErrNoRows {
		t.Fatal("cancelled user available to admin", err)
	}
	if _, err = s.resolveUserID(1, "", ""); err != sql.ErrNoRows {
		t.Fatal("cancelled user can receive grants", err)
	}
	// A new account with the same identity must not inherit old balances or unlocks.
	insertTestEmailCode(t, s, "test1@example.invalid", "register")
	w = accountRequest(t, h, "/api/auth/register", "", map[string]string{"username": "user1", "email": "test1@example.invalid", "password": password, "confirm_password": password, "code": "123456"}, 200)
	newToken := w.Result().Cookies()[0].Value
	requireStatus(t, resourceHandler, "GET", "/api/cases/v1", newToken, 402)
}

func TestLegacyDeletionMigration(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	mustExec(t, db, `create table users(id integer primary key,username text,email text,deleted_at text)`)
	mustExec(t, db, `create unique index uk_users_username on users(username) where deleted_at is null`)
	mustExec(t, db, `create unique index uk_users_email on users(email) where deleted_at is null`)
	mustExec(t, db, `insert into users values(1,'active','active@example.invalid',null),(2,'cancelled','cancelled@example.invalid','2026-01-01')`)
	for i := 0; i < 2; i++ {
		if err = initAppDB(db); err != nil {
			t.Fatal(err)
		}
		var active, deleted int
		if err = db.QueryRow("select delete_flag from users where id=1").Scan(&active); err != nil || active != 0 {
			t.Fatal("active user migration failed", err)
		}
		if err = db.QueryRow("select delete_flag from users where id=2").Scan(&deleted); err != nil || deleted != 1 {
			t.Fatal("deleted user migration failed", err)
		}
	}
	if _, err = db.Exec(`insert into users(id,username,email) values(3,'active','another@example.invalid')`); err == nil {
		t.Fatal("active username uniqueness lost")
	}
	mustExec(t, db, `insert into users(id,username,email) values(3,'cancelled','cancelled@example.invalid')`)
	mustExec(t, db, `update users set delete_flag=1 where id=1`)
	mustExec(t, db, `insert into users(id,username,email) values(4,'active','active@example.invalid')`)
}

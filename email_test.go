package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"
)

func emailTestMux(s *server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/email-code", s.sendEmailCode)
	mux.HandleFunc("POST /api/user/email", s.requireLevel(1, s.changeEmail))
	mux.Handle("/", accountTestMux(s))
	return mux
}

func TestEmailRegistrationGiftAndSingleUse(t *testing.T) {
	s, _ := testServer(t)
	h := emailTestMux(s)
	const email = "registration@example.invalid"
	var code string
	s.emailSender = func(_ context.Context, recipient, purpose, value string) error {
		if recipient != email || purpose != "register" || len(value) != 6 {
			t.Fatal("invalid delivery parameters")
		}
		code = value
		return nil
	}
	w := accountRequest(t, h, "/api/auth/email-code", "", map[string]string{"email": " Registration@Example.Invalid ", "purpose": "register"}, 200)
	if strings.Contains(w.Body.String(), code) || strings.Contains(w.Body.String(), "devCode") {
		t.Fatal("response leaked code")
	}
	var stored, status string
	if err := s.appDB.QueryRow(`select code_hash,delivery_status from email_verification_codes`).Scan(&stored, &status); err != nil || stored != emailCodeHash(email, "register", code) || status != "sent" {
		t.Fatal("code was not stored as a hash after delivery", err)
	}
	body := map[string]string{"username": "new_user", "email": email, "password": "TestPassword_123", "confirm_password": "TestPassword_123", "code": code}
	accountRequest(t, h, "/api/auth/register", "", body, 200)
	var paid, grant int64
	var level int
	if err := s.appDB.QueryRow(`select paid_points,grant_points,role_level from point_accounts join users on users.id=user_id`).Scan(&paid, &grant, &level); err != nil || paid != registerGiftPoints || grant != 0 || level != 1 {
		t.Fatalf("incorrect signup balance or role: paid=%d grant=%d level=%d err=%v", paid, grant, level, err)
	}
	var count int
	if err := s.appDB.QueryRow(`select count(*) from point_lots where point_type='paid' and source_type='register_gift' and expire_at is null and total_amount=?`, registerGiftPoints).Scan(&count); err != nil || count != 1 {
		t.Fatal("registration gift must be non-expiring paid points", err)
	}
	if err := s.appDB.QueryRow(`select count(*) from point_records where point_type='paid' and biz_type='register_gift' and grant_amount=0 and paid_amount=?`, registerGiftPoints).Scan(&count); err != nil || count != 1 {
		t.Fatal("registration ledger has wrong point type", err)
	}
	accountRequest(t, h, "/api/auth/register", "", body, 400)
	usage, err := s.loadUserUsage(context.Background(), 1, 10, time.Now())
	if err != nil || usage.Quota.Total != 0 || usage.Quota.Remaining != 0 {
		t.Fatal("registration gift appeared in quota statistics", err)
	}
	if err = s.appDB.QueryRow(`select count(*) from orders`).Scan(&count); err != nil || count != 0 {
		t.Fatal("registration gift created a paid order", err)
	}
	if err := verifyEmailCode(s.appDB, email, "register", code); err == nil {
		t.Fatal("used code was accepted")
	}
}

func TestEmailCodeResetAndChangeEmail(t *testing.T) {
	s, _ := testServer(t)
	h := emailTestMux(s)
	token := testUser(t, s, 1, 1)
	var code string
	s.emailSender = func(_ context.Context, _, _, value string) error { code = value; return nil }
	accountRequest(t, h, "/api/auth/email-code", "", map[string]string{"email": "test1@example.invalid", "purpose": "reset_password"}, 200)
	password := "TestPassword_123"
	body := map[string]string{"email": "test1@example.invalid", "code": code, "password": password, "confirm_password": password}
	accountRequest(t, h, "/api/auth/reset-password", "", body, 200)
	accountRequest(t, h, "/api/auth/reset-password", "", body, 400)
	accountRequest(t, h, "/api/user/email", token, map[string]string{"email": "changed@example.invalid", "code": code}, 401)
	w := accountRequest(t, h, "/api/auth/login", "", map[string]string{"login": "user1", "password": password}, 200)
	token = w.Result().Cookies()[0].Value
	accountRequest(t, h, "/api/auth/email-code", token, map[string]string{"email": "changed@example.invalid", "purpose": "change_email"}, 200)
	accountRequest(t, h, "/api/user/email", token, map[string]string{"email": "changed@example.invalid", "code": code}, 200)
	accountRequest(t, h, "/api/user/email", token, map[string]string{"email": "changed@example.invalid", "code": code}, 400)
	accountRequest(t, h, "/api/auth/login", "", map[string]string{"login": "changed@example.invalid", "password": password}, 200)
}

func TestEmailCodeFailuresAndLimits(t *testing.T) {
	s, _ := testServer(t)
	h := emailTestMux(s)
	const email = "codes@example.invalid"
	code := ""
	failSend := true
	s.emailSender = func(_ context.Context, _, _, value string) error {
		code = value
		if failSend {
			return errors.New("test SMTP unavailable")
		}
		return nil
	}
	body := map[string]string{"email": email, "purpose": "register"}
	accountRequest(t, h, "/api/auth/email-code", "", body, 502)
	if err := verifyEmailCode(s.appDB, email, "register", code); err == nil {
		t.Fatal("failed delivery code is usable")
	}
	w := accountRequest(t, h, "/api/auth/email-code", "", body, 429)
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("rate limit missing Retry-After")
	}
	age := func() {
		mustExec(t, s.appDB, `update email_verification_codes set created_at=?`, appTime(time.Now().Add(-2*time.Minute)))
	}
	age()
	failSend = false
	accountRequest(t, h, "/api/auth/email-code", "", body, 200)
	if err := verifyEmailCode(s.appDB, email, "register", code); err != nil {
		t.Fatal(err)
	}
	if err := verifyEmailCode(s.appDB, email, "reset_password", code); err == nil {
		t.Fatal("code valid for another purpose")
	}
	if err := verifyEmailCode(s.appDB, "someone@example.invalid", "register", code); err == nil {
		t.Fatal("code valid for another email")
	}
	for range emailCodeAttempts {
		if err := verifyEmailCode(s.appDB, email, "register", "not-a-code"); err == nil {
			t.Fatal("wrong code accepted")
		}
	}
	if err := verifyEmailCode(s.appDB, email, "register", code); err == nil {
		t.Fatal("code accepted after five failures")
	}
	for range 3 {
		age()
		accountRequest(t, h, "/api/auth/email-code", "", body, 200)
	}
	age()
	accountRequest(t, h, "/api/auth/email-code", "", body, 429)
	mustExec(t, s.appDB, `update email_verification_codes set expires_at=?`, appTime(time.Now().Add(-time.Second)))
	if err := verifyEmailCode(s.appDB, email, "register", code); err == nil {
		t.Fatal("expired code accepted")
	}
}

func TestEmailCodeResendAndConsumptionRace(t *testing.T) {
	s, _ := testServer(t)
	email := "resend@example.invalid"
	first, _, err := s.reserveEmailCode(email, "register", "123456", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err = verifyEmailCode(s.appDB, email, "register", "123456"); err == nil {
		t.Fatal("pending code accepted")
	}
	if err = s.activateEmailCode(first, email, "register"); err != nil {
		t.Fatal(err)
	}
	mustExec(t, s.appDB, `update email_verification_codes set created_at=?`, appTime(time.Now().Add(-2*time.Minute)))
	second, _, err := s.reserveEmailCode(email, "register", "654321", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.activateEmailCode(second, email, "register"); err != nil {
		t.Fatal(err)
	}
	if err = verifyEmailCode(s.appDB, email, "register", "123456"); err == nil {
		t.Fatal("old code survived resend")
	}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := s.appDB.Begin()
			if err != nil {
				results <- false
				return
			}
			defer tx.Rollback()
			err = markEmailCodeUsedTx(tx, email, "register", "654321")
			results <- err == nil && tx.Commit() == nil
		}()
	}
	wg.Wait()
	if a, b := <-results, <-results; a == b {
		t.Fatal("code must be consumed exactly once")
	}
}

func TestEmailCodeAuthorizationAndIPLimit(t *testing.T) {
	s, _ := testServer(t)
	h := emailTestMux(s)
	var sent int
	s.emailSender = func(context.Context, string, string, string) error { sent++; return nil }
	accountRequest(t, h, "/api/auth/email-code", "", map[string]string{"email": "new@example.invalid", "purpose": "change_email"}, 401)
	token := testUser(t, s, 1, 1)
	accountRequest(t, h, "/api/auth/email-code", token, map[string]string{"email": "test1@example.invalid", "purpose": "register"}, 409)
	accountRequest(t, h, "/api/auth/email-code", token, map[string]string{"email": "test1@example.invalid", "purpose": "change_email"}, 409)
	mustExec(t, s.appDB, `update users set status=1 where id=1`)
	accountRequest(t, h, "/api/auth/email-code", "", map[string]string{"email": "test1@example.invalid", "purpose": "reset_password"}, 400)
	accountRequest(t, h, "/api/auth/email-code", token, map[string]string{"email": "new@example.invalid", "purpose": "change_email"}, 401)
	for range 20 {
		mustExec(t, s.appDB, `insert into email_verification_codes(email,purpose,code_hash,expires_at,created_at,request_ip) values('another@example.invalid','register','hash',?,?,?)`, appTime(time.Now().Add(time.Minute)), appNow(), "192.0.2.1")
	}
	accountRequest(t, h, "/api/auth/email-code", "", map[string]string{"email": "other@example.invalid", "purpose": "register"}, 429)
	if sent != 0 {
		t.Fatal("unauthorized or rate-limited request sent email")
	}
}

func TestSMTPConfigurationAndMessage(t *testing.T) {
	for key, value := range map[string]string{"SMTP_HOST": "smtp.example.invalid", "SMTP_PORT": "465", "SMTP_USER": "sender@example.invalid", "SMTP_PASSWORD": "test-secret", "SMTP_FROM": "sender@example.invalid", "SMTP_SECURITY": ""} {
		t.Setenv(key, value)
	}
	c, err := loadSMTPConfig()
	if err != nil || c.security != "tls" {
		t.Fatal("465 must use implicit TLS", err)
	}
	t.Setenv("SMTP_PORT", "587")
	c, err = loadSMTPConfig()
	if err != nil || c.security != "starttls" {
		t.Fatal("587 must require STARTTLS", err)
	}
	t.Setenv("SMTP_SECURITY", "none")
	if _, err = loadSMTPConfig(); err == nil {
		t.Fatal("plaintext SMTP permitted")
	}
	t.Setenv("SMTP_HOST", "")
	s, _ := testServer(t)
	w := accountRequest(t, emailTestMux(s), "/api/auth/email-code", "", map[string]string{"email": "test@example.invalid", "purpose": "register"}, 503)
	if strings.Contains(w.Body.String(), "devCode") {
		t.Fatal("SMTP absence exposed a development code")
	}
	msg, err := verificationMessage("sender@example.invalid", "recipient@example.invalid", "register", "123456")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(msg))
	if err != nil {
		t.Fatal(err)
	}
	subject, err := (&mime.WordDecoder{}).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || !strings.Contains(subject, "注册账号") {
		t.Fatal("invalid encoded subject", err)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	if err != nil || !strings.Contains(string(body), "123456") || !strings.Contains(string(body), "10 分钟") {
		t.Fatal("invalid message body", err)
	}
}

func TestSMTPRejectsUntrustedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	c := smtpConfig{host: "127.0.0.1", port: strings.Split(server.Listener.Addr().String(), ":")[1], security: "tls"}
	err := c.send(context.Background(), "recipient@example.invalid", "register", "123456")
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("untrusted SMTP certificate not rejected: %v", err)
	}
}

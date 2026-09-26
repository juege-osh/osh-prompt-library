package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testServer(t *testing.T) (*server, http.Handler) {
	t.Helper()
	root := t.TempDir()
	open := func(name string) *sql.DB {
		db, err := sql.Open("sqlite", filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { db.Close() })
		return db
	}
	s := &server{appDB: open("app.sqlite3"), db: open("catalog.sqlite3"), imageDB: open("images.sqlite3"), root: root}
	if err := initAppDB(s.appDB); err != nil {
		t.Fatal(err)
	}
	mustExec(t, s.db, `create table cases(id text primary key,video text,poster text,input_images text,prompt_zh text,missing_reference integer,has_output_audio integer)`)
	mustExec(t, s.db, `insert into cases values('v1','video.mp4','poster.jpg','["input.jpg"]','private-video-prompt',0,0),('v2','video.mp4','poster.jpg','[]','second-prompt',0,0)`)
	mustExec(t, s.db, `alter table cases add column title text default 'Test video'`)
	mustExec(t, s.db, `alter table cases add column model text default 'Test model'`)
	mustExec(t, s.imageDB, `create table images(id text primary key,source_code text,path text,thumb_path text,prompt_zh text,prompt_original text,model text,width integer,height integer)`)
	mustExec(t, s.imageDB, `insert into images values('i1','test','input.jpg','','private-image-prompt','original-prompt','test',1024,768)`)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, 1024, 768)), nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"input.jpg", "poster.jpg", "video.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), out.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/assets", s.requireLevel(1, s.purchasedAssets))
	mux.HandleFunc("GET /api/pricing", s.pricing)
	mux.HandleFunc("POST /api/admin/pricing", s.requireLevel(3, s.updatePricing))
	mux.HandleFunc("GET /api/resources/{kind}/{id}/access", s.resourceAccess)
	mux.HandleFunc("POST /api/resources/{kind}/{id}/unlock", s.requireLevel(1, s.unlockResource))
	mux.HandleFunc("GET /api/cases/", s.detail)
	mux.HandleFunc("GET /api/images/", s.imageDetail)
	mux.HandleFunc("GET /media/", s.media)
	mux.HandleFunc("GET /image-media/", s.imageMedia)
	return s, mux
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func testUser(t *testing.T, s *server, id int64, level int) string {
	t.Helper()
	now := appNow()
	mustExec(t, s.appDB, `insert into users(id,username,email,password_hash,role_level,created_at,updated_at) values(?,?,?,?,?,?,?)`, id, fmt.Sprintf("user%d", id), fmt.Sprintf("test%d@example.invalid", id), "unused", level, now, now)
	token := fmt.Sprintf("test-session-%d", id)
	mustExec(t, s.appDB, `insert into user_sessions(user_id,session_hash,expires_at,created_at) values(?,?,?,?)`, id, tokenHash(token), appTime(time.Now().Add(time.Hour)), now)
	return token
}

func testGrant(t *testing.T, s *server, id, amount int64, kind string, expire *time.Time) {
	t.Helper()
	tx, err := s.appDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	bizNo, err := randomToken(16)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = grantPointsTx(tx, id, amount*pointScale, kind, "test", bizNo, expire, ""); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func testRequest(h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(`{"price_version":1}`))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func requireStatus(t *testing.T, h http.Handler, method, path, token string, status int) *httptest.ResponseRecorder {
	t.Helper()
	w := testRequest(h, method, path, token)
	if w.Code != status {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	return w
}

func TestResourceAuthorizationAndPrices(t *testing.T) {
	s, h := testServer(t)
	token := testUser(t, s, 1, 1)
	other := testUser(t, s, 2, 1)
	admin := testUser(t, s, 3, 3)
	expire := time.Now().Add(time.Hour)
	testGrant(t, s, 1, 3, "grant", &expire)
	testGrant(t, s, 1, 5, "paid", nil)
	paths := []string{"/api/cases/v1", "/api/images/i1", "/media/v1/video", "/media/v1/video?preview=1", "/media/v1/input/0", "/image-media/i1/original", "/image-media/i1/original?preview=1"}
	for _, path := range paths {
		requireStatus(t, h, "GET", path, "", 401)
		requireStatus(t, h, "GET", path, token, 402)
		requireStatus(t, h, "HEAD", path, "", 401)
		requireStatus(t, h, "GET", path, admin, 200)
	}
	for _, path := range []string{"/media/v1/poster", "/image-media/i1/thumb", "/media/v1/input/0?preview=1"} {
		w := requireStatus(t, h, "GET", path, "", 200)
		config, _, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if path != "/media/v1/poster" && (config.Width > 320 || config.Height > 320) {
			t.Fatalf("preview leaked original dimensions: %+v", config)
		}
	}
	requireStatus(t, h, "POST", "/api/resources/video/missing/unlock", token, 404)
	requireStatus(t, h, "POST", "/api/resources/video/v1/unlock", token, 200)
	requireStatus(t, h, "POST", "/api/resources/video/v1/unlock", token, 200)
	requireStatus(t, h, "GET", "/api/cases/v1", token, 200)
	requireStatus(t, h, "GET", "/api/cases/v1", other, 402)
	balance, err := s.loadPointBalance(1)
	if err != nil || balance.TotalPoints != 300 || balance.GrantPoints != 0 || balance.PaidPoints != 300 {
		t.Fatalf("wrong debit order: %+v %v", balance, err)
	}
	var ledgerEnd int64
	if err := s.appDB.QueryRow(`select sum(before_balance-after_balance) from point_records where user_id=1 and change_type='expense'`).Scan(&ledgerEnd); err != nil || ledgerEnd != 500 {
		t.Fatalf("lot ledger must add up to the debit: %d %v", ledgerEnd, err)
	}
	requireStatus(t, h, "POST", "/api/resources/image/i1/unlock", token, 200)
	balance, _ = s.loadPointBalance(1)
	if balance.TotalPoints != 200 {
		t.Fatalf("image must cost 1: %+v", balance)
	}
	requireStatus(t, h, "POST", "/api/resources/video/v2/unlock", token, 402)
	mustExec(t, s.appDB, "update users set status=1 where id=1")
	requireStatus(t, h, "GET", "/api/cases/v1", token, 401)
	requireStatus(t, h, "POST", "/api/resources/video/v1/unlock", token, 401)
	var count int
	if err = s.appDB.QueryRow("select count(*) from point_records where user_id=3").Scan(&count); err != nil || count != 0 {
		t.Fatal("admin was charged", err)
	}
	if err = s.softDeleteUser(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if err = s.appDB.QueryRow("select count(*) from resource_unlocks where user_id=1").Scan(&count); err != nil || count != 2 {
		t.Fatal("soft deletion must retain unlock history", err)
	}
}

func TestConcurrentUnlockAndRollback(t *testing.T) {
	s, h := testServer(t)
	token := testUser(t, s, 1, 1)
	testGrant(t, s, 1, 5, "paid", nil)
	var wg sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); responses <- testRequest(h, "POST", "/api/resources/video/v1/unlock", token) }()
	}
	wg.Wait()
	close(responses)
	for w := range responses {
		if w.Code != 200 {
			t.Fatalf("concurrent unlock: %d %s", w.Code, w.Body.String())
		}
	}
	balance, _ := s.loadPointBalance(1)
	if balance.TotalPoints != 0 {
		t.Fatal("wrong balance", balance)
	}
	requireStatus(t, h, "GET", "/media/v1/video", token, 200)
	var count int
	s.appDB.QueryRow("select count(*) from point_operations where biz_type='resource_unlock'").Scan(&count)
	if count != 1 {
		t.Fatalf("expected one debit, got %d", count)
	}
	testGrant(t, s, 1, 1, "paid", nil)
	mustExec(t, s.appDB, `create trigger reject_unlock before insert on resource_unlocks begin select raise(abort,'test failure'); end`)
	requireStatus(t, h, "POST", "/api/resources/image/i1/unlock", token, 500)
	balance, _ = s.loadPointBalance(1)
	if balance.TotalPoints != 100 {
		t.Fatal("failed unlock charged points", balance)
	}
	requireStatus(t, h, "GET", "/api/images/i1", token, 402)
}

func TestVIPQuotaAndCSRF(t *testing.T) {
	s, h := testServer(t)
	token := testUser(t, s, 1, 2)
	now := time.Now().UTC()
	mustExec(t, s.appDB, `insert into user_memberships(user_id,plan_code,start_at,expire_at,status,source_type,source_no,created_at,updated_at) values(1,'vip_year',?,?,'active','test','test',?,?)`, appTime(now.AddDate(0, -1, 0)), appTime(now.AddDate(1, 0, 0)), appNow(), appNow())
	expire := now.Add(-time.Hour)
	testGrant(t, s, 1, 10, "grant", &expire)
	requireStatus(t, h, "GET", "/api/resources/video/v1/access", token, 200)
	balance, _ := s.loadPointBalance(1)
	if balance.TotalPoints != 188800 {
		t.Fatalf("monthly quota missing: %+v", balance)
	}
	r := httptest.NewRequest("POST", "/api/resources/video/v1/unlock", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://untrusted.invalid")
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("cross-origin debit accepted: %d", w.Code)
	}
	requireStatus(t, h, "POST", "/api/resources/video/v1/unlock", token, 200)
	balance, _ = s.loadPointBalance(1)
	if balance.TotalPoints != 188300 {
		t.Fatalf("VIP must pay 5 points: %+v", balance)
	}
	mustExec(t, s.appDB, "update point_lots set remaining_amount=0, status='exhausted' where user_id=1")
	mustExec(t, s.appDB, "update point_accounts set total_points=0,grant_points=0,paid_points=0 where user_id=1")
	requireStatus(t, h, "POST", "/api/resources/video/v2/unlock", token, 402)
}

type mediaRoundTrip func(*http.Request) (*http.Response, error)

func (f mediaRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMediaProxyRangeAndRevocation(t *testing.T) {
	s, h := testServer(t)
	token := testUser(t, s, 1, 3)
	s.r2Access, s.r2Secret = "test-access", "test-secret"
	calls := 0
	s.mediaClient = &http.Client{Transport: mediaRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Cookie") != "" || r.Header.Get("Range") != "bytes=0-3" {
			t.Error("wrong upstream headers", r.Header)
		}
		return &http.Response{StatusCode: 206, Header: http.Header{"Content-Type": {"video/mp4"}, "Content-Length": {"4"}, "Content-Range": {"bytes 0-3/100"}}, Body: io.NopCloser(strings.NewReader("test"))}, nil
	})}
	r := httptest.NewRequest("GET", "/media/v1/video", nil)
	r.Header.Set("Range", "bytes=0-3")
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "test" || w.Header().Get("Location") != "" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("bad media proxy: %+v", w)
	}
	mustExec(t, s.appDB, "update user_sessions set revoked_at=? where user_id=1", appNow())
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 || calls != 1 {
		t.Fatalf("revoked session reached R2: status=%d calls=%d", w.Code, calls)
	}
}

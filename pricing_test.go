package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func requestJSON(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPointsJSON(t *testing.T) {
	for _, raw := range []string{"0.1", "1.23", "888", "-0.01", "0.00"} {
		var p Points
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatal(raw, err)
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Points
		if err = json.Unmarshal(encoded, &decoded); err != nil || p != decoded {
			t.Fatalf("roundtrip %s: %s %v", raw, encoded, err)
		}
	}
	for _, raw := range []string{"0.001", "null", "true", `"0.5"`, "1e100", "1e-9999999999", "92233720368547759"} {
		var p Points
		if err := json.Unmarshal([]byte(raw), &p); err == nil {
			t.Fatalf("accepted invalid points %s", raw)
		}
	}
}

func TestPricingAuthorizationPrecisionAndStaleQuotes(t *testing.T) {
	s, h := testServer(t)
	token := testUser(t, s, 1, 1)
	admin := testUser(t, s, 2, 3)
	testGrant(t, s, 1, 1, "paid", nil)
	for _, tc := range []struct {
		token, body string
		status      int
	}{
		{"", `{"multiplier":0.1,"version":1}`, 401},
		{token, `{"multiplier":0.1,"version":1}`, 403},
		{admin, `{"multiplier":0.09,"version":1}`, 400},
		{admin, `{"multiplier":1.01,"version":1}`, 400},
		{admin, `{"multiplier":0.101,"version":1}`, 400},
		{admin, `{"multiplier":0.1,"version":1}`, 200},
		{admin, `{"multiplier":0.2,"version":1}`, 409},
	} {
		w := requestJSON(h, "POST", "/api/admin/pricing", tc.token, tc.body)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body.String())
		}
	}
	w := requireStatus(t, h, "GET", "/api/pricing", "", 200)
	var p resourcePricing
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.Video != 50 || p.Image != 10 || p.Multiplier != 10 || p.Version != 2 {
		t.Fatalf("bad pricing: %s %v", w.Body.String(), err)
	}
	requireStatus(t, h, "POST", "/api/resources/video/v1/unlock", token, 409)
	for _, kind := range []string{"video/v1", "image/i1"} {
		w = requestJSON(h, "POST", "/api/resources/"+kind+"/unlock", token, `{"price_version":2}`)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	balance, _ := s.loadPointBalance(1)
	if balance.TotalPoints != 40 {
		t.Fatal("expected exactly 0.40 remaining", balance)
	}
	// Previously owned assets remain available even with an old quote.
	requireStatus(t, h, "POST", "/api/resources/video/v1/unlock", token, 200)
	var paid int64
	if err := s.appDB.QueryRow(`select sum(points) from resource_unlocks where user_id=1`).Scan(&paid); err != nil || paid != 60 {
		t.Fatal(paid, err)
	}
	var changes int
	if err := s.appDB.QueryRow(`select count(*) from pricing_changes where operator_id=2`).Scan(&changes); err != nil || changes != 1 {
		t.Fatal(changes, err)
	}
}

func TestPurchasedAssetsIsolationAndPagination(t *testing.T) {
	s, h := testServer(t)
	token := testUser(t, s, 1, 1)
	other := testUser(t, s, 2, 1)
	admin := testUser(t, s, 3, 3)
	for i := 0; i < 26; i++ {
		mustExec(t, s.appDB, `insert into resource_unlocks(user_id,resource_kind,resource_id,points,created_at) values(1,'video',?,50,?)`, fmt.Sprintf("v%d", i), appNow())
	}
	mustExec(t, s.appDB, `insert into resource_unlocks(user_id,resource_kind,resource_id,points,created_at) values(1,'image','i1',10,?)`, appNow())
	requireStatus(t, h, "GET", "/api/assets", "", 401)
	requireStatus(t, h, "GET", "/api/assets?kind=invalid", token, 400)
	for _, tc := range []struct {
		path, token  string
		total, count int
	}{
		{"/api/assets", token, 27, 24}, {"/api/assets?kind=video&page=2", token, 26, 2},
		{"/api/assets?kind=image", token, 1, 1}, {"/api/assets", other, 0, 0}, {"/api/assets", admin, 0, 0},
	} {
		w := requireStatus(t, h, "GET", tc.path, tc.token, 200)
		var result struct {
			Total int              `json:"total"`
			Items []purchasedAsset `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Total != tc.total || len(result.Items) != tc.count {
			t.Fatalf("%s %s", tc.path, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private-image-prompt") {
			t.Fatal("assets leaked prompt")
		}
	}
}

func TestPointMigrationIsAtomicAndIdempotent(t *testing.T) {
	s, _ := testServer(t)
	testUser(t, s, 1, 1)
	testGrant(t, s, 1, 5, "paid", nil)
	// Simulate pre-migration storage, including existing balances and history.
	for _, q := range []string{
		`delete from app_migrations where name='point_hundredths_v1'`,
		`update point_accounts set total_points=total_points/100,grant_points=grant_points/100,paid_points=paid_points/100`,
		`update point_lots set total_amount=total_amount/100,remaining_amount=remaining_amount/100`,
		`update point_records set change_amount=change_amount/100,grant_amount=grant_amount/100,paid_amount=paid_amount/100,before_balance=before_balance/100,after_balance=after_balance/100`,
		`update member_plans set monthly_points=monthly_points/100`,
	} {
		mustExec(t, s.appDB, q)
	}
	mustExec(t, s.appDB, `insert into resource_unlocks(user_id,resource_kind,resource_id,points,created_at) values(1,'video','v1',5,?)`, appNow())
	mustExec(t, s.appDB, `create trigger fail_migration before update on point_lots begin select raise(abort,'test rollback'); end`)
	if err := migratePointPrecision(s.appDB); err == nil {
		t.Fatal("expected migration rollback")
	}
	balance, _ := s.loadPointBalance(1)
	if balance.TotalPoints != 5 {
		t.Fatal("partial migration", balance)
	}
	mustExec(t, s.appDB, `drop trigger fail_migration`)
	for i := 0; i < 2; i++ {
		if err := initAppDB(s.appDB); err != nil {
			t.Fatal(err)
		}
	}
	balance, _ = s.loadPointBalance(1)
	if balance.TotalPoints != 500 {
		t.Fatal("balance changed twice", balance)
	}
	for _, q := range []string{`select points from resource_unlocks where user_id=1`, `select change_amount from point_records where user_id=1`, `select total_amount from point_lots where user_id=1`} {
		var amount int64
		if err := s.appDB.QueryRow(q).Scan(&amount); err != nil || amount != 500 {
			t.Fatal(q, amount, err)
		}
	}
}

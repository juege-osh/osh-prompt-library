package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestUsageHistoryAndShanghaiDayBoundaries(t *testing.T) {
	s, _ := testServer(t)
	testUser(t, s, 1, 1)
	testUser(t, s, 2, 1)
	now := time.Date(2026, 9, 26, 3, 0, 0, 0, time.UTC)
	fixtures := []struct {
		kind, at string
		points   int64
		user     int64
	}{
		{"video", "2026-09-01T12:00:00Z", 500, 1},
		{"video", "2026-09-16T15:59:59Z", 100, 1},
		{"video", "2026-09-16T16:00:00Z", 50, 1},
		{"image", "2026-09-17T15:59:59Z", 10, 1},
		{"image", "2026-09-17T16:00:00Z", 25, 1},
		{"video", "2026-09-26T02:00:00Z", 150, 1},
		{"video", "2026-09-27T02:00:00Z", 500, 1},
		{"video", "2026-09-26T02:00:00Z", 999, 2},
	}
	for i, f := range fixtures {
		mustExec(t, s.appDB, `insert into resource_unlocks(user_id,resource_kind,resource_id,points,created_at) values(?,?,?,?,?)`, f.user, f.kind, fmt.Sprintf("usage%d", i), f.points, f.at)
	}
	// Price changes must not alter historical charges or purchase counts.
	mustExec(t, s.appDB, `update resource_pricing set multiplier=10`)
	result, err := s.loadUserUsage(context.Background(), 1, 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.TimeZone != "Asia/Shanghai" || len(result.Daily) != 10 || result.Daily[0].Date != "2026-09-17" || result.Daily[9].Date != "2026-09-26" {
		t.Fatalf("wrong window: %+v", result)
	}
	if result.Distribution[0].Purchases != 4 || result.Distribution[0].Points != 800 || result.Distribution[1].Purchases != 2 || result.Distribution[1].Points != 35 {
		t.Fatalf("wrong lifetime distribution: %+v", result.Distribution)
	}
	if result.Daily[0].Video != 50 || result.Daily[0].Image != 10 || result.Daily[1].Image != 25 || result.Daily[9].Video != 150 {
		t.Fatalf("wrong daily charges: %+v", result.Daily)
	}
	for i := 2; i < 9; i++ {
		if result.Daily[i].Video != 0 || result.Daily[i].Image != 0 {
			t.Fatal("missing days not zero-filled", result.Daily)
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Daily []struct {
			Video float64 `json:"video"`
			Image float64 `json:"image"`
		} `json:"daily"`
	}
	if err = json.Unmarshal(encoded, &wire); err != nil || wire.Daily[0].Video != 0.5 || wire.Daily[1].Image != 0.25 {
		t.Fatalf("scaled units leaked: %s", encoded)
	}
	short, err := s.loadUserUsage(context.Background(), 1, 1, now)
	if err != nil || len(short.Daily) != 1 || short.Daily[0].Video != 150 {
		t.Fatal(short, err)
	}
	if _, err = s.loadUserUsage(context.Background(), 1, 11, now); err == nil {
		t.Fatal("accepted more than ten days")
	}
}

func TestUsageQuotaIncludesExhaustedUnexpiredGrants(t *testing.T) {
	s, _ := testServer(t)
	testUser(t, s, 1, 1)
	testUser(t, s, 2, 1)
	now := time.Now().UTC()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	testGrant(t, s, 1, 100, "grant", nil)
	testGrant(t, s, 1, 50, "grant", &future)
	testGrant(t, s, 1, 20, "grant", &past)
	testGrant(t, s, 1, 70, "paid", nil)
	consume := func(amount int64, biz string) {
		tx, err := s.appDB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err = consumePointsTx(tx, 1, amount*pointScale, "test_usage", biz, ""); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	consume(100, "partial")
	result, err := s.loadUserUsage(context.Background(), 1, 10, now)
	if err != nil || result.Quota.Total != 15000 || result.Quota.Remaining != 5000 {
		t.Fatalf("quota denominator excluded exhausted lot or included paid/expired: %+v %v", result, err)
	}
	if result.Distribution[0].Purchases != 0 || result.Distribution[1].Points != 0 {
		t.Fatal("non-resource expenses counted")
	}
	consume(50, "exhausted")
	result, err = s.loadUserUsage(context.Background(), 1, 10, now)
	if err != nil || result.Quota.Total != 15000 || result.Quota.Remaining != 0 {
		t.Fatal("exhausted quota should show 0/150", result, err)
	}
	result, err = s.loadUserUsage(context.Background(), 1, 10, future.Add(time.Second))
	if err != nil || result.Quota.Total != 10000 {
		t.Fatal("expired denominator persisted", result, err)
	}
	result, err = s.loadUserUsage(context.Background(), 2, 10, now)
	if err != nil || result.Quota.Total != 0 || result.Quota.Remaining != 0 || len(result.Daily) != 10 {
		t.Fatal("empty account", result, err)
	}
}

func TestUsageEndpointPermissionsAndLimits(t *testing.T) {
	s, _ := testServer(t)
	token := testUser(t, s, 1, 1)
	other := testUser(t, s, 2, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/user/usage", s.requireLevel(1, s.userUsage))
	mustExec(t, s.appDB, `insert into resource_unlocks(user_id,resource_kind,resource_id,points,created_at) values(1,'image','private',33,?)`, appTime(time.Now().Add(-time.Minute)))
	requireStatus(t, mux, "GET", "/api/user/usage", "", 401)
	for _, days := range []string{"0", "11", "-1", "abc", "1.5"} {
		requireStatus(t, mux, "GET", "/api/user/usage?days="+days, token, 400)
	}
	for _, tc := range []struct {
		token string
		count int64
	}{{token, 1}, {other, 0}} {
		w := requireStatus(t, mux, "GET", "/api/user/usage?user_id=1", tc.token, 200)
		var result userUsageVO
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Distribution[1].Purchases != tc.count {
			t.Fatal("user isolation failed", w.Body.String(), err)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("usage may be cached")
		}
	}
	mustExec(t, s.appDB, `update users set status=1 where id=1`)
	requireStatus(t, mux, "GET", "/api/user/usage", token, 401)
	mustExec(t, s.appDB, `update users set delete_flag=1 where id=2`)
	requireStatus(t, mux, "GET", "/api/user/usage", other, 401)
}

func TestUsageCountsOnePurchaseAcrossPointLotsAndRepeatAccess(t *testing.T) {
	s, h := testServer(t)
	token := testUser(t, s, 1, 1)
	admin := testUser(t, s, 2, 3)
	expire := time.Now().Add(time.Hour)
	testGrant(t, s, 1, 3, "grant", &expire)
	testGrant(t, s, 1, 5, "paid", nil)
	for i := 0; i < 2; i++ {
		requireStatus(t, h, "POST", "/api/resources/video/v1/unlock", token, 200)
	}
	requireStatus(t, h, "POST", "/api/resources/video/v1/unlock", admin, 200)
	var records int
	if err := s.appDB.QueryRow(`select count(*) from point_records where user_id=1 and change_type='expense'`).Scan(&records); err != nil || records != 2 {
		t.Fatal("expected a debit spanning two lots", records, err)
	}
	result, err := s.loadUserUsage(context.Background(), 1, 10, time.Now())
	if err != nil || result.Distribution[0].Purchases != 1 || result.Distribution[0].Points != 500 || result.Daily[9].Video != 500 {
		t.Fatal("one purchase was counted more than once", result, err)
	}
	if result.Quota.Total != 300 || result.Quota.Remaining != 0 {
		t.Fatal("exhausted grant quota lost denominator", result.Quota)
	}
	result, err = s.loadUserUsage(context.Background(), 2, 10, time.Now())
	if err != nil || result.Distribution[0].Purchases != 0 || result.Daily[9].Video != 0 {
		t.Fatal("administrator free access counted as purchase", result, err)
	}
}

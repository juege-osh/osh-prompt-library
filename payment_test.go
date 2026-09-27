package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func paymentTestMux(s *server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/payments/products", s.paymentProducts)
	mux.HandleFunc("GET /api/admin/payment-prices", s.requireLevel(3, s.adminPaymentPrices))
	mux.HandleFunc("POST /api/admin/payment-prices", s.requireLevel(3, s.adminPaymentPrices))
	mux.HandleFunc("POST /api/payments/orders", s.requireLevel(1, s.createPaymentOrder))
	mux.HandleFunc("GET /api/payments/{orderNo}", s.requireLevel(1, s.paymentStatus))
	mux.HandleFunc("POST /api/payments/{orderNo}/cancel", s.requireLevel(1, s.cancelPayment))
	mux.HandleFunc("POST /api/payments/{orderNo}/mock-success", s.requireLevel(1, s.mockPaymentSuccess))
	return mux
}

func TestAdminCanChangePaymentPrices(t *testing.T) {
	s, _ := testServer(t)
	admin := testUser(t, s, 1, 3)
	normal := testUser(t, s, 2, 1)
	h := paymentTestMux(s)

	paymentJSONRequest(t, h, "GET", "/api/admin/payment-prices", admin, "", http.StatusOK)
	paymentJSONRequest(t, h, "GET", "/api/admin/payment-prices", normal, "", http.StatusForbidden)
	paymentJSONRequest(t, h, "POST", "/api/admin/payment-prices", normal,
		`{"kind":"plan","code":"vip_month","price_cents":100}`, http.StatusForbidden)
	paymentJSONRequest(t, h, "POST", "/api/admin/payment-prices", admin,
		`{"kind":"plan","code":"vip_month","price_cents":100}`, http.StatusOK)
	paymentJSONRequest(t, h, "POST", "/api/admin/payment-prices", admin,
		`{"kind":"plan","code":"vip_month","price_cents":100}`, http.StatusOK)
	paymentJSONRequest(t, h, "POST", "/api/admin/payment-prices", admin,
		`{"kind":"package","code":"points_1000","price_cents":200}`, http.StatusOK)

	var planPrice, packagePrice int
	if err := s.appDB.QueryRow(`select price_cents from member_plans where plan_code='vip_month'`).Scan(&planPrice); err != nil {
		t.Fatal(err)
	}
	if err := s.appDB.QueryRow(`select price_cents from point_packages where package_code='points_1000'`).Scan(&packagePrice); err != nil {
		t.Fatal(err)
	}
	if planPrice != 100 || packagePrice != 200 {
		t.Fatalf("unexpected payment prices: plan=%d package=%d", planPrice, packagePrice)
	}

	if err := initAppDB(s.appDB); err != nil {
		t.Fatal(err)
	}
	if err := s.appDB.QueryRow(`select price_cents from member_plans where plan_code='vip_month'`).Scan(&planPrice); err != nil {
		t.Fatal(err)
	}
	if planPrice != 100 {
		t.Fatalf("plan price was reset after initialization: %d", planPrice)
	}
}

func paymentJSONRequest(t *testing.T, h http.Handler, method, path, token string, body string, status int) map[string]any {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHuifuPCTradeType(t *testing.T) {
	if got := huifuPCTradeType("alipay"); got != "A_JSAPI" {
		t.Fatalf("alipay trade type = %q, want A_JSAPI", got)
	}
	if got := huifuPCTradeType("wechat"); got != "T_JSAPI" {
		t.Fatalf("wechat trade type = %q, want T_JSAPI", got)
	}
}

func TestMockPaymentSettlementAndIdempotency(t *testing.T) {
	s, _ := testServer(t)
	s.payProvider = "mock"
	token := testUser(t, s, 1, 1)
	h := paymentTestMux(s)

	products := paymentJSONRequest(t, h, "GET", "/api/payments/products", "", "", http.StatusOK)
	if products["provider"] != "mock" {
		t.Fatalf("wrong provider: %#v", products["provider"])
	}

	created := paymentJSONRequest(t, h, "POST", "/api/payments/orders", token,
		`{"order_type":"subscription","plan_code":"vip_month","channel":"alipay"}`, http.StatusOK)
	order := created["order"].(map[string]any)
	orderNo := order["orderNo"].(string)
	if order["status"] != "pending" || order["amountCents"].(float64) != 880 {
		t.Fatalf("unexpected pending order: %#v", order)
	}

	paymentJSONRequest(t, h, "POST", "/api/payments/"+orderNo+"/mock-success", token, `{}`, http.StatusOK)
	var level int
	if err := s.appDB.QueryRow("select role_level from users where id=1").Scan(&level); err != nil || level != levelVIP {
		t.Fatalf("membership not granted: level=%d err=%v", level, err)
	}
	var balance int64
	if err := s.appDB.QueryRow("select total_points from point_accounts where user_id=1").Scan(&balance); err != nil || balance != 1288*pointScale {
		t.Fatalf("membership quota not granted: balance=%d err=%v", balance, err)
	}
	var paymentStatus, orderStatus string
	if err := s.appDB.QueryRow("select status from payments where order_no=?", orderNo).Scan(&paymentStatus); err != nil {
		t.Fatal(err)
	}
	if err := s.appDB.QueryRow("select status from orders where order_no=?", orderNo).Scan(&orderStatus); err != nil {
		t.Fatal(err)
	}
	if paymentStatus != "success" || orderStatus != "success" {
		t.Fatalf("wrong settled statuses: %s/%s", paymentStatus, orderStatus)
	}

	// Replaying the local payment completion must not issue another VIP cycle.
	paymentJSONRequest(t, h, "POST", "/api/payments/"+orderNo+"/mock-success", token, `{}`, http.StatusOK)
	var cycleCount, membershipCount int
	if err := s.appDB.QueryRow("select count(*) from vip_point_cycles where user_id=1").Scan(&cycleCount); err != nil {
		t.Fatal(err)
	}
	if err := s.appDB.QueryRow("select count(*) from user_memberships where user_id=1").Scan(&membershipCount); err != nil {
		t.Fatal(err)
	}
	if cycleCount != 1 || membershipCount != 1 {
		t.Fatalf("duplicate settlement changed entitlements: cycles=%d memberships=%d", cycleCount, membershipCount)
	}

	created = paymentJSONRequest(t, h, "POST", "/api/payments/orders", token,
		`{"order_type":"recharge","package_code":"points_1000","channel":"alipay"}`, http.StatusOK)
	orderNo = created["order"].(map[string]any)["orderNo"].(string)
	paymentJSONRequest(t, h, "POST", "/api/payments/"+orderNo+"/mock-success", token, `{}`, http.StatusOK)
	if err := s.appDB.QueryRow("select paid_points from point_accounts where user_id=1").Scan(&balance); err != nil || balance != 1000*pointScale {
		t.Fatalf("recharge points not granted: balance=%d err=%v", balance, err)
	}

	created = paymentJSONRequest(t, h, "POST", "/api/payments/orders", token,
		`{"order_type":"subscription","plan_code":"vip_year","channel":"alipay"}`, http.StatusOK)
	orderNo = created["order"].(map[string]any)["orderNo"].(string)
	paymentJSONRequest(t, h, "POST", "/api/payments/"+orderNo+"/cancel", token, `{}`, http.StatusOK)
	if err := s.appDB.QueryRow("select status from orders where order_no=?", orderNo).Scan(&orderStatus); err != nil {
		t.Fatal(err)
	}
	if orderStatus != "closed" {
		t.Fatalf("order was not closed: %s", orderStatus)
	}
}

func TestHuifuNotificationVerificationAndIdempotency(t *testing.T) {
	s, _ := testServer(t)
	s.payProvider = "mock"
	token := testUser(t, s, 1, 1)
	h := paymentTestMux(s)
	created := paymentJSONRequest(t, h, "POST", "/api/payments/orders", token,
		`{"order_type":"subscription","plan_code":"vip_month","channel":"alipay"}`, http.StatusOK)
	order := created["order"].(map[string]any)
	orderNo := order["orderNo"].(string)
	paymentNo := order["paymentNo"].(string)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s.payProvider = "huifu"
	s.huifu = &huifuPayClient{
		baseURL: "https://api.huifu.test", sysID: "sys", productID: "product", huifuID: "merchant",
		privateKey: key, publicKey: &key.PublicKey, notifyURL: "https://example.invalid/notify",
	}
	mustExec(t, s.appDB, "update payments set provider='huifu' where payment_no=?", paymentNo)
	notifyData := map[string]any{
		"huifu_id": "merchant", "org_req_seq_id": paymentNo, "hf_seq_id": "HF-1", "trans_stat": "S",
		"order_stat": "1", "trans_amt": "8.80", "pay_type": "A_NATIVE",
	}
	rawBytes, err := canonicalJSON(notifyData)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(rawBytes)
	signature, err := signHuifuRaw(raw, key)
	if err != nil {
		t.Fatal(err)
	}
	notify := func() *httptest.ResponseRecorder {
		form := url.Values{"resp_code": {"10000"}, "resp_desc": {"OK"}, "sign": {signature}, "resp_data": {raw}}
		r := httptest.NewRequest("POST", "/api/payments/notify/huifu", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.huifuNotify(w, r)
		return w
	}
	if response := notify(); response.Code != http.StatusOK || response.Body.String() != "RECV_ORD_ID_"+paymentNo {
		t.Fatalf("notification rejected: %d %s", response.Code, response.Body.String())
	}
	if response := notify(); response.Code != http.StatusOK {
		t.Fatalf("duplicate notification rejected: %d %s", response.Code, response.Body.String())
	}
	var paymentStatus string
	if err = s.appDB.QueryRow("select status from payments where payment_no=?", paymentNo).Scan(&paymentStatus); err != nil || paymentStatus != "success" {
		t.Fatalf("notification did not settle payment: %s %v", paymentStatus, err)
	}
	var notifyCount, operationCount int
	if err = s.appDB.QueryRow("select count(*) from payment_notify_logs where payment_no=?", paymentNo).Scan(&notifyCount); err != nil {
		t.Fatal(err)
	}
	if err = s.appDB.QueryRow("select count(*) from point_operations where user_id=1 and biz_type='vip_cycle'").Scan(&operationCount); err != nil {
		t.Fatal(err)
	}
	if notifyCount != 1 || operationCount != 1 {
		t.Fatalf("notification idempotency failed: logs=%d operations=%d", notifyCount, operationCount)
	}
	_ = orderNo
}

func TestHuifuWebhookProbeReturnsSuccess(t *testing.T) {
	s, _ := testServer(t)
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "/api/payments/notify/huifu", strings.NewReader(""))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.huifuNotify(w, r)
		if w.Code != http.StatusOK || w.Body.String() != "success" {
			t.Fatalf("%s webhook probe rejected: %d %s", method, w.Code, w.Body.String())
		}
	}
}

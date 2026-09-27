package main

import "testing"

func TestSeedAppDBDoesNotOverwritePlanPrices(t *testing.T) {
	s, _ := testServer(t)
	mustExec(t, s.appDB, `update member_plans set price_cents=1 where plan_code='vip_month'`)
	if err := initAppDB(s.appDB); err != nil {
		t.Fatal(err)
	}
	var price int
	if err := s.appDB.QueryRow(`select price_cents from member_plans where plan_code='vip_month'`).Scan(&price); err != nil {
		t.Fatal(err)
	}
	if price != 1 {
		t.Fatalf("custom plan price was overwritten: got %d", price)
	}
}

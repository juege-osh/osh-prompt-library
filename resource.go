package main

import (
	"database/sql"
	"errors"
	"net/http"
)

func resourcePrice(kind string) int64 {
	if kind == "video" {
		return 5 * pointScale
	}
	if kind == "image" {
		return pointScale
	}
	return 0
}

func (s *server) resourceExists(kind, id string) bool {
	if !validID.MatchString(id) {
		return false
	}
	var found int
	if kind == "video" {
		return s.db.QueryRow("select 1 from cases where id=?", id).Scan(&found) == nil
	}
	if kind == "image" && s.imageDB != nil {
		return s.imageDB.QueryRow("select 1 from images where id=?", id).Scan(&found) == nil
	}
	return false
}

func (s *server) resourceUnlocked(user *currentUser, kind, id string) (bool, error) {
	if user.Level >= levelAdmin {
		return true, nil
	}
	var found int
	err := s.appDB.QueryRow(`select 1 from resource_unlocks where user_id=? and resource_kind=? and resource_id=?`, user.ID, kind, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *server) requireResource(w http.ResponseWriter, r *http.Request, kind, id string) bool {
	setMediaHeaders(w, false)
	user, err := s.currentUser(r)
	if err != nil {
		apiError(w, http.StatusUnauthorized, "请先登录，使用积分解锁资源；积分不足时请充值或订阅会员")
		return false
	}
	if user.Level < levelNormal {
		apiError(w, http.StatusForbidden, "权限不足")
		return false
	}
	ok, err := s.resourceUnlocked(user, kind, id)
	if err != nil {
		apiError(w, 500, "资源权限读取失败")
		return false
	}
	if !ok {
		apiError(w, http.StatusPaymentRequired, "请先使用积分解锁；积分不足时请充值或订阅会员")
		return false
	}
	return true
}

func (s *server) resourceAccess(w http.ResponseWriter, r *http.Request) {
	kind, id := r.PathValue("kind"), r.PathValue("id")
	if !s.resourceExists(kind, id) {
		http.NotFound(w, r)
		return
	}
	pricing, err := readPricing(s.appDB)
	if err != nil {
		apiError(w, 500, "价格读取失败")
		return
	}
	result := map[string]any{"kind": kind, "id": id, "cost": Points(resourcePrice(kind) * int64(pricing.Multiplier) / pointScale), "price_version": pricing.Version, "requiredLevel": levelNormal, "authenticated": false, "unlocked": false, "balance": Points(0)}
	user, err := s.currentUser(r)
	if err == nil {
		unlocked, err := s.resourceUnlocked(user, kind, id)
		if err != nil {
			apiError(w, 500, "资源权限读取失败")
			return
		}
		result["authenticated"], result["unlocked"], result["balance"], result["level"] = true, unlocked, user.TotalPoints, user.Level
	}
	jsonResponse(w, result)
}

func (s *server) unlockResource(w http.ResponseWriter, r *http.Request, user *currentUser) {
	if !sameOriginJSON(w, r) {
		return
	}
	var req struct {
		PriceVersion int64 `json:"price_version"`
	}
	if err := decodeJSON(r, &req); err != nil {
		apiError(w, 400, "请求格式不正确")
		return
	}
	kind, id := r.PathValue("kind"), r.PathValue("id")
	if !s.resourceExists(kind, id) {
		http.NotFound(w, r)
		return
	}
	if user.Level >= levelAdmin {
		jsonResponse(w, map[string]any{"unlocked": true, "charged": 0})
		return
	}
	tx, err := s.appDB.BeginTx(r.Context(), nil)
	if err != nil {
		apiError(w, 500, "解锁失败")
		return
	}
	defer tx.Rollback()
	// Serialize the entitlement check and debit with SQLite's writer lock.
	_, err = tx.Exec(`update point_accounts set updated_at=updated_at where user_id=?`, user.ID)
	if err != nil {
		apiError(w, 500, "解锁失败")
		return
	}
	var found int
	err = tx.QueryRow(`select 1 from resource_unlocks where user_id=? and resource_kind=? and resource_id=?`, user.ID, kind, id).Scan(&found)
	if err == nil {
		jsonResponse(w, map[string]any{"unlocked": true, "charged": 0})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		apiError(w, 500, "解锁失败")
		return
	}
	var available int64
	err = tx.QueryRow(`select coalesce(sum(remaining_amount),0) from point_lots where user_id=? and status='available' and (expire_at is null or expire_at>?)`, user.ID, appNow()).Scan(&available)
	if err != nil {
		apiError(w, 500, "积分读取失败")
		return
	}
	pricing, err := readPricing(tx)
	if err != nil {
		apiError(w, 500, "价格读取失败")
		return
	}
	if req.PriceVersion != pricing.Version {
		apiError(w, 409, "资源价格已更新，请重新点击并确认价格")
		return
	}
	cost := resourcePrice(kind) * int64(pricing.Multiplier) / pointScale
	if available < cost {
		apiError(w, http.StatusPaymentRequired, "积分不足，请充值或订阅会员后解锁")
		return
	}
	if err = consumePointsTx(tx, user.ID, cost, "resource_unlock", kind+":"+id, "解锁资源 "+kind+":"+id); err != nil {
		apiError(w, 500, "扣费失败，请重试")
		return
	}
	_, err = tx.Exec(`insert into resource_unlocks (user_id,resource_kind,resource_id,points,created_at) values(?,?,?,?,?)`, user.ID, kind, id, cost, appNow())
	if err != nil {
		apiError(w, 500, "解锁失败")
		return
	}
	if err = tx.Commit(); err != nil {
		apiError(w, 500, "解锁失败")
		return
	}
	jsonResponse(w, map[string]any{"unlocked": true, "charged": Points(cost)})
}

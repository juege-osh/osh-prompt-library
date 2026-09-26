package main

import (
	"net/http"
	"strconv"
)

type purchasedAsset struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Paid        Points `json:"paidPoints"`
	PurchasedAt string `json:"purchasedAt"`
	Title       string `json:"title"`
	Model       string `json:"model"`
	Thumbnail   string `json:"thumbnail"`
	Available   bool   `json:"available"`
}

func (s *server) purchasedAssets(w http.ResponseWriter, r *http.Request, user *currentUser) {
	kind := r.URL.Query().Get("kind")
	if kind != "" && kind != "all" && kind != "video" && kind != "image" {
		apiError(w, 400, "资源类型无效")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		page = 100000
	}
	where, args := " where user_id=?", []any{user.ID}
	if kind == "video" || kind == "image" {
		where += " and resource_kind=?"
		args = append(args, kind)
	}
	var total int
	if err := s.appDB.QueryRow("select count(*) from resource_unlocks"+where, args...).Scan(&total); err != nil {
		apiError(w, 500, "资产读取失败")
		return
	}
	rows, err := s.appDB.Query("select resource_kind,resource_id,points,created_at from resource_unlocks"+where+" order by created_at desc,rowid desc limit 24 offset ?", append(args, (page-1)*24)...)
	if err != nil {
		apiError(w, 500, "资产读取失败")
		return
	}
	items := []purchasedAsset{}
	for rows.Next() {
		var item purchasedAsset
		if err = rows.Scan(&item.Kind, &item.ID, &item.Paid, &item.PurchasedAt); err != nil {
			rows.Close()
			apiError(w, 500, "资产读取失败")
			return
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		apiError(w, 500, "资产读取失败")
		return
	}
	for i := range items {
		x := &items[i]
		if x.Kind == "video" {
			var poster string
			if err = s.db.QueryRow("select title,model,poster from cases where id=?", x.ID).Scan(&x.Title, &x.Model, &poster); err == nil {
				x.Available = true
				x.Thumbnail = s.mediaURL(x.ID, "poster", poster)
			}
		} else if s.imageDB != nil {
			if err = s.imageDB.QueryRow("select model from images where id=?", x.ID).Scan(&x.Model); err == nil {
				x.Available = true
				x.Title = "图片提示词"
				x.Thumbnail = "/image-media/" + x.ID + "/thumb"
			}
		}
		if !x.Available {
			x.Title = "资源暂不可用"
		}
	}
	jsonResponse(w, map[string]any{"items": items, "page": page, "size": 24, "total": total})
}

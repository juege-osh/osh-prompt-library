package main

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (s *server) imageList(w http.ResponseWriter, r *http.Request) {
	if s.imageDB == nil {
		http.NotFound(w, r)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		page = 100000
	}
	term := textParam(r, "q", 100)
	source := textParam(r, "source", 30)
	where := []string{}
	args := []any{}
	if id := textParam(r, "resource", 120); id != "" {
		where = append(where, "id=?")
		args = append(args, id)
	}
	if source == "diffusiondb" || source == "qwen" || source == "tdeh" {
		where = append(where, "source_code=?")
		args = append(args, source)
	}
	if term != "" {
		where = append(where, "(prompt_zh like ? or prompt_original like ? or model like ?)")
		for i := 0; i < 3; i++ {
			args = append(args, "%"+term+"%")
		}
	}
	clause := ""
	if len(where) > 0 {
		clause = " where " + strings.Join(where, " and ")
	}
	var total, all, translated int
	if err := s.imageDB.QueryRow("select count(*) from images"+clause, args...).Scan(&total); err != nil {
		fail(w, err)
		return
	}
	if err := s.imageDB.QueryRow("select count(*),sum(translated) from images").Scan(&all, &translated); err != nil {
		fail(w, err)
		return
	}
	queryArgs := append(append([]any{}, args...), 24, (page-1)*24)
	rows, err := s.imageDB.Query("select id,source_code,model,width,height,translated from images"+clause+" order by translated desc,source_code,image_name limit ? offset ?", queryArgs...)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, code, model string
		var width, height, translated int
		if err := rows.Scan(&id, &code, &model, &width, &height, &translated); err != nil {
			fail(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "source": code, "model": model, "translated": translated != 0, "width": width, "height": height, "thumbnail": "/image-media/" + id + "/thumb"})
	}
	pricing, err := readPricing(s.appDB)
	if err != nil {
		apiError(w, 500, "价格读取失败")
		return
	}
	jsonResponse(w, map[string]any{"cost": pricing.Image, "total": total, "all": all, "translated": translated, "page": page, "size": 24, "items": items})
}

func (s *server) imageDetail(w http.ResponseWriter, r *http.Request) {
	if s.imageDB == nil {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/images/")
	if !validID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	var zh, originalPrompt, code, model string
	var width, height int
	err := s.imageDB.QueryRow("select prompt_zh,prompt_original,source_code,model,width,height from images where id=?", id).Scan(&zh, &originalPrompt, &code, &model, &width, &height)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !s.requireResource(w, r, "image", id) {
		return
	}
	jsonResponse(w, map[string]any{"id": id, "prompt_zh": zh, "prompt_original": originalPrompt, "source": code, "model": model, "width": width, "height": height, "image": "/image-media/" + id + "/original"})
}

func (s *server) imageMedia(w http.ResponseWriter, r *http.Request) {
	if s.imageDB == nil {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/image-media/"), "/")
	if len(parts) != 2 || !validID.MatchString(parts[0]) || (parts[1] != "thumb" && parts[1] != "original") {
		http.NotFound(w, r)
		return
	}
	id, kind := parts[0], parts[1]
	var code, original, thumb string
	err := s.imageDB.QueryRow("select source_code,path,thumb_path from images where id=?", id).Scan(&code, &original, &thumb)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if kind == "original" && !s.requireResource(w, r, "image", id) {
		return
	}
	if kind == "thumb" && thumb == "" {
		var body []byte
		if s.imageDB.QueryRow("select webp from embedded_thumbs where id=?", id).Scan(&body) == nil && len(body) > 0 {
			w.Header().Set("Content-Type", "image/webp")
			setMediaHeaders(w, true)
			_, _ = w.Write(body)
			return
		}
	}
	if s.r2Access != "" && s.r2Secret != "" {
		ext := ".webp"
		if kind == "original" {
			ext = strings.ToLower(filepath.Ext(original))
		}
		key := fmt.Sprintf("v1/images/%s/%s/%s%s", code, id, kind, ext)
		s.proxyMedia(w, r, key, kind == "thumb")
		return
	}
	relative := original
	if kind == "thumb" {
		if thumb == "" {
			s.servePreview(w, r, "", original)
			return
		}
		relative = thumb
	}
	if !filepath.IsLocal(relative) {
		http.NotFound(w, r)
		return
	}
	path, err := filepath.EvalSymlinks(filepath.Join(s.root, relative))
	if err != nil || !strings.HasPrefix(path, s.root+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(path)))
	setMediaHeaders(w, kind == "thumb")
	http.ServeContent(w, r, filepath.Base(path), stat.ModTime(), file)
}

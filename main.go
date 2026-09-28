package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	_ "modernc.org/sqlite"
)

//go:embed index.html style.css app.js images.html images.js images.css account.js account.css shell.js site.css workspace.html usage.js web-vendor/lucide.min.js web-vendor/chart.umd.js web-vendor/qrcode.min.js
var assets embed.FS

var validID = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,119}$`)
var words = regexp.MustCompile(`[\pL\pN_-]+`)

type server struct {
	db          *sql.DB
	imageDB     *sql.DB
	appDB       *sql.DB
	root        string
	r2Access    string
	r2Secret    string
	mediaClient *http.Client
	emailSender func(context.Context, string, string, string) error
	payProvider string
	huifu       *huifuPayClient
}

func hmacSHA256(key []byte, value string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(value))
	return h.Sum(nil)
}

func signedR2URL(access, secret, key string, now time.Time, ttl int) string {
	const host = "645fe29b30ebce3e940e06365ce74872.r2.cloudflarestorage.com"
	date := now.UTC().Format("20060102")
	stamp := now.UTC().Format("20060102T150405Z")
	scope := date + "/auto/s3/aws4_request"
	query := url.Values{
		"X-Amz-Algorithm":     {"AWS4-HMAC-SHA256"},
		"X-Amz-Credential":    {access + "/" + scope},
		"X-Amz-Date":          {stamp},
		"X-Amz-Expires":       {strconv.Itoa(ttl)},
		"X-Amz-SignedHeaders": {"host"},
	}
	path := "/osh-prompt/" + key
	canonical := "GET\n" + path + "\n" + query.Encode() + "\nhost:" + host + "\n\nhost\nUNSIGNED-PAYLOAD"
	hash := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hex.EncodeToString(hash[:])
	signingKey := hmacSHA256([]byte("AWS4"+secret), date)
	signingKey = hmacSHA256(signingKey, "auto")
	signingKey = hmacSHA256(signingKey, "s3")
	signingKey = hmacSHA256(signingKey, "aws4_request")
	query.Set("X-Amz-Signature", hex.EncodeToString(hmacSHA256(signingKey, toSign)))
	return "https://" + host + path + "?" + query.Encode()
}

func (s *server) mediaURL(id, kind, relative string) string {
	if relative == "" {
		return ""
	}
	if s.r2Access != "" && s.r2Secret != "" {
		return "/media/" + id + "/" + kind
	}
	if fileExists(filepath.Join(s.root, relative)) {
		return "/media/" + id + "/" + strings.ReplaceAll(kind, "inputs/image-", "input/")
	}
	return ""
}

type item struct {
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	Category       string          `json:"category"`
	Style          string          `json:"style"`
	Model          string          `json:"model"`
	GenerationType string          `json:"generation_type"`
	Duration       sql.NullFloat64 `json:"-"`
	Poster         string          `json:"poster"`
	Video          string          `json:"video"`
}

func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, err error) {
	log.Print(err)
	http.Error(w, "暂时无法加载", http.StatusInternalServerError)
}
func textParam(r *http.Request, key string, n int) string {
	s := strings.TrimSpace(r.URL.Query().Get(key))
	if len([]rune(s)) > n {
		s = string([]rune(s)[:n])
	}
	return s
}
func (s *server) list(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		page = 100000
	}
	term := textParam(r, "q", 100)
	category := textParam(r, "category", 60)
	model := textParam(r, "model", 80)
	duration := r.URL.Query().Get("duration")
	asset := r.URL.Query().Get("asset")
	clauses := []string{}
	args := []any{}
	if id := textParam(r, "resource", 120); id != "" {
		clauses = append(clauses, "c.id=?")
		args = append(args, id)
	}
	if term != "" {
		if strings.IndexFunc(term, func(r rune) bool { return r >= 0x3400 && r <= 0x9fff }) >= 0 {
			if len([]rune(term)) >= 3 {
				clauses = append(clauses, "c.id in (select id from search_zh where search_zh match ?)")
				args = append(args, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
			} else {
				clauses = append(clauses, "(c.title like ? or c.prompt_zh like ? or c.category like ? or c.style like ?)")
				for i := 0; i < 4; i++ {
					args = append(args, "%"+term+"%")
				}
			}
		} else {
			found := words.FindAllString(term, 6)
			if len(found) > 0 {
				parts := make([]string, len(found))
				for i, word := range found {
					parts[i] = `"` + strings.ReplaceAll(word, `"`, ``) + `"*`
				}
				clauses = append(clauses, "c.id in (select id from search where search match ?)")
				args = append(args, strings.Join(parts, " "))
			}
		}
	}
	if category != "" {
		clauses = append(clauses, "c.category = ?")
		args = append(args, category)
	}
	if model != "" {
		clauses = append(clauses, "c.model = ?")
		args = append(args, model)
	}
	switch duration {
	case "under10":
		clauses = append(clauses, "c.duration < 10")
	case "10to20":
		clauses = append(clauses, "c.duration >= 10 and c.duration < 20")
	case "20to30":
		clauses = append(clauses, "c.duration >= 20 and c.duration < 30")
	case "30plus":
		clauses = append(clauses, "c.duration >= 30")
	}
	switch asset {
	case "images":
		clauses = append(clauses, "c.input_images != '[]'")
	case "missing":
		clauses = append(clauses, "c.missing_reference = 1")
	}
	where := ""
	if len(clauses) > 0 {
		where = " where " + strings.Join(clauses, " and ")
	}
	var total, accepted int
	if err := s.db.QueryRow("select count(*) from cases c"+where, args...).Scan(&total); err != nil {
		fail(w, err)
		return
	}
	if err := s.db.QueryRow("select value from meta where key='count'").Scan(&accepted); err != nil {
		fail(w, err)
		return
	}
	queryArgs := append(append([]any{}, args...), 24, (page-1)*24)
	rows, err := s.db.Query("select c.id,c.title,c.category,c.style,c.model,c.generation_type,c.duration,c.poster,c.video,c.input_images from cases c"+where+" order by c.rowid limit ? offset ?", queryArgs...)
	if err != nil {
		fail(w, err)
		return
	}
	items := []map[string]any{}
	for rows.Next() {
		var x item
		var images string
		if err = rows.Scan(&x.ID, &x.Title, &x.Category, &x.Style, &x.Model, &x.GenerationType, &x.Duration, &x.Poster, &x.Video, &images); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		poster := s.mediaURL(x.ID, "poster", x.Poster)
		var d any = nil
		if x.Duration.Valid {
			d = x.Duration.Float64
		}
		var input []string
		_ = json.Unmarshal([]byte(images), &input)
		firstImage := ""
		if len(input) > 0 {
			firstImage = s.mediaURL(x.ID, "input/0", input[0])
			if firstImage != "" {
				firstImage += "?preview=1"
			}
		}
		items = append(items, map[string]any{"id": x.ID, "title": x.Title, "category": x.Category, "style": x.Style, "model": x.Model, "generation_type": x.GenerationType, "duration": d, "poster": poster, "video": s.mediaURL(x.ID, "video", x.Video), "input_image_count": len(input), "input_image_preview": firstImage})
	}
	rows.Close()
	groups := func(column string) []map[string]any {
		result := []map[string]any{}
		rs, e := s.db.Query("select " + column + ",count(*) from cases group by " + column + " order by count(*) desc limit 30")
		if e != nil {
			return result
		}
		defer rs.Close()
		for rs.Next() {
			var name string
			var count int
			if rs.Scan(&name, &count) == nil {
				result = append(result, map[string]any{"name": name, "count": count})
			}
		}
		return result
	}
	var imageCases, missingCases int
	_ = s.db.QueryRow("select count(*) from cases where input_images != '[]'").Scan(&imageCases)
	_ = s.db.QueryRow("select count(*) from cases where missing_reference = 1").Scan(&missingCases)
	pricing, err := readPricing(s.appDB)
	if err != nil {
		apiError(w, 500, "价格读取失败")
		return
	}
	jsonResponse(w, map[string]any{"cost": pricing.Video, "total": total, "accepted": accepted, "image_cases": imageCases, "missing_cases": missingCases, "page": page, "size": 24, "items": items, "categories": groups("category"), "models": groups("model")})
}
func (s *server) detail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/cases/")
	if !validID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	var zh, images, video string
	var missingReference, hasOutputAudio int
	err := s.db.QueryRow("select prompt_zh,input_images,missing_reference,has_output_audio,video from cases where id=?", id).Scan(&zh, &images, &missingReference, &hasOutputAudio, &video)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	if !s.requireResource(w, r, "video", id) {
		return
	}
	var input []string
	_ = json.Unmarshal([]byte(images), &input)
	paths := make([]string, 0, len(input))
	for i, relative := range input {
		if media := s.mediaURL(id, fmt.Sprintf("input/%d", i), relative); media != "" {
			paths = append(paths, media)
		}
	}
	jsonResponse(w, map[string]any{"id": id, "prompt_zh": zh, "video": s.mediaURL(id, "video", video), "input_images": paths, "missing_reference": missingReference != 0, "has_output_audio": hasOutputAudio != 0})
}
func (s *server) media(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/media/"), "/")
	if len(parts) < 2 || len(parts) > 3 || !validID.MatchString(parts[0]) {
		http.NotFound(w, r)
		return
	}
	var video, poster, images string
	err := s.db.QueryRow("select video,poster,input_images from cases where id=?", parts[0]).Scan(&video, &poster, &images)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	relative := ""
	switch parts[1] {
	case "video":
		if len(parts) == 2 {
			relative = video
		}
	case "poster":
		if len(parts) == 2 {
			relative = poster
		}
	case "input":
		if len(parts) == 3 {
			i, e := strconv.Atoi(parts[2])
			var list []string
			_ = json.Unmarshal([]byte(images), &list)
			if e == nil && i >= 0 && i < len(list) {
				relative = list[i]
			}
		}
	}
	if relative == "" {
		http.NotFound(w, r)
		return
	}
	preview := parts[1] == "input" && r.URL.Query().Get("preview") == "1"
	public := parts[1] == "poster" || preview
	if !public && !s.requireResource(w, r, "video", parts[0]) {
		return
	}
	kind := parts[1]
	if kind == "input" {
		i, _ := strconv.Atoi(parts[2])
		kind = fmt.Sprintf("inputs/image-%02d", i+1)
	}
	key := "v1/cases/" + parts[0] + "/" + kind + strings.ToLower(filepath.Ext(relative))
	if preview {
		s.servePreview(w, r, key, relative)
		return
	}
	if s.r2Access != "" && s.r2Secret != "" {
		s.proxyMedia(w, r, key, public)
		return
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
	setMediaHeaders(w, public)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(path)))
	http.ServeContent(w, r, filepath.Base(path), stat.ModTime(), file)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
func main() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Fatal("Unable to load .env: ", err)
	}
	port := flag.Int("port", 18766, "local HTTP port")
	appPath := flag.String("app-db", "app.sqlite3", "application SQLite path")
	flag.Parse()
	if _, err := loadSMTPConfig(); err != nil {
		log.Print("SMTP not configured correctly; email verification is unavailable")
	} else {
		log.Print("SMTP email verification configured (TLS required)")
	}
	dir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "catalog.sqlite3")); err != nil {
		log.Fatal("请先建立索引: python3 build_index.py")
	}
	root, err := filepath.Abs(filepath.Join(dir, ".."))
	if err != nil {
		log.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "catalog.sqlite3")+"?mode=ro")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	r2Access, r2Secret := os.Getenv("R2_ACCESS_KEY_ID"), os.Getenv("R2_SECRET_ACCESS_KEY")
	if (r2Access == "") != (r2Secret == "") {
		log.Fatal("R2 credentials must be set together")
	}
	if r2Access == "" {
		log.Print("R2 not configured; media requires local source files")
	} else {
		log.Print("R2 read-only media configured")
	}
	var imageDB *sql.DB
	if fileExists(filepath.Join(dir, "images_catalog.sqlite3")) {
		imageDB, err = sql.Open("sqlite", "file:"+filepath.Join(dir, "images_catalog.sqlite3")+"?mode=ro")
		if err != nil {
			log.Fatal(err)
		}
		defer imageDB.Close()
		imageDB.SetMaxOpenConns(4)
	}
	appDB, err := sql.Open("sqlite", "file:"+*appPath+"?mode=rwc")
	if err != nil {
		log.Fatal(err)
	}
	defer appDB.Close()
	appDB.SetMaxOpenConns(1)
	if err = initAppDB(appDB); err != nil {
		log.Fatal(err)
	}
	payProvider := strings.ToLower(strings.TrimSpace(os.Getenv("PAY_PROVIDER")))
	if payProvider == "" {
		payProvider = "disabled"
	}
	var huifu *huifuPayClient
	if payProvider == "huifu" {
		huifu = newHuifuPayClientFromEnv()
	}
	s := &server{db: db, imageDB: imageDB, appDB: appDB, root: root, r2Access: r2Access, r2Secret: r2Secret, payProvider: payProvider, huifu: huifu}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/register", s.register)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("POST /api/auth/email-code", s.sendEmailCode)
	mux.HandleFunc("POST /api/auth/reset-password", s.resetPassword)
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("GET /api/user/profile", s.requireLevel(1, s.userProfile))
	mux.HandleFunc("GET /api/user/usage", s.requireLevel(1, s.userUsage))
	mux.HandleFunc("POST /api/user/password", s.requireLevel(1, s.changePassword))
	mux.HandleFunc("POST /api/user/email", s.requireLevel(1, s.changeEmail))
	mux.HandleFunc("POST /api/user/delete", s.requireLevel(1, s.deleteAccount))
	mux.HandleFunc("GET /api/member/plans", s.memberPlans)
	mux.HandleFunc("GET /api/member/status", s.requireLevel(1, s.memberStatus))
	mux.HandleFunc("GET /api/member/center", s.requireLevel(1, s.memberCenter))
	mux.HandleFunc("GET /api/orders", s.requireLevel(1, s.orderRecords))
	mux.HandleFunc("GET /api/payments/products", s.paymentProducts)
	mux.HandleFunc("GET /api/admin/payment-prices", s.requireLevel(3, s.adminPaymentPrices))
	mux.HandleFunc("POST /api/admin/payment-prices", s.requireLevel(3, s.adminPaymentPrices))
	mux.HandleFunc("POST /api/payments/orders", s.requireLevel(1, s.createPaymentOrder))
	mux.HandleFunc("GET /api/payments/{orderNo}", s.requireLevel(1, s.paymentStatus))
	mux.HandleFunc("POST /api/payments/{orderNo}/cancel", s.requireLevel(1, s.cancelPayment))
	mux.HandleFunc("POST /api/payments/{orderNo}/mock-success", s.requireLevel(1, s.mockPaymentSuccess))
	mux.HandleFunc("POST /api/payments/notify/huifu", s.huifuNotify)
	mux.HandleFunc("GET /api/payments/notify/huifu", s.huifuNotify)
	mux.HandleFunc("GET /api/assets", s.requireLevel(1, s.purchasedAssets))
	mux.HandleFunc("GET /api/pricing", s.pricing)
	mux.HandleFunc("GET /api/admin/pricing", s.requireLevel(3, func(w http.ResponseWriter, r *http.Request, _ *currentUser) { s.pricing(w, r) }))
	mux.HandleFunc("POST /api/admin/pricing", s.requireLevel(3, s.updatePricing))
	mux.HandleFunc("GET /api/points/balance", s.requireLevel(1, s.pointBalance))
	mux.HandleFunc("GET /api/points/records", s.requireLevel(1, s.pointRecords))
	mux.HandleFunc("POST /api/admin/member/grant", s.requireLevel(3, s.adminGrantMember))
	mux.HandleFunc("POST /api/admin/points/adjust", s.requireLevel(3, s.adminAdjustPoints))
	mux.HandleFunc("GET /api/admin/users", s.requireLevel(3, s.adminUsers))
	mux.HandleFunc("GET /api/admin/users/", s.requireLevel(3, s.adminUserDetail))
	mux.HandleFunc("POST /api/admin/users/block", s.requireLevel(3, s.adminBlockUser))
	mux.HandleFunc("POST /api/admin/users/unblock", s.requireLevel(3, s.adminUnblockUser))
	mux.HandleFunc("GET /api/cases", s.list)
	mux.HandleFunc("GET /api/resources/{kind}/{id}/access", s.resourceAccess)
	mux.HandleFunc("POST /api/resources/{kind}/{id}/unlock", s.requireLevel(levelNormal, s.unlockResource))
	mux.HandleFunc("GET /api/cases/", s.detail)
	mux.HandleFunc("GET /media/", s.media)
	mux.HandleFunc("GET /api/images", s.imageList)
	mux.HandleFunc("GET /api/images/", s.imageDetail)
	mux.HandleFunc("GET /image-media/", s.imageMedia)
	mux.HandleFunc("GET /images", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/images.html", http.StatusMovedPermanently)
	})
	for _, route := range []string{"/assets", "/account", "/member"} {
		mux.HandleFunc("GET "+route, func(w http.ResponseWriter, r *http.Request) {
			body, _ := assets.ReadFile("workspace.html")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(body)
		})
	}
	for _, name := range []string{"index.html", "style.css", "app.js", "images.html", "images.js", "images.css", "account.js", "account.css", "shell.js", "site.css", "usage.js", "web-vendor/lucide.min.js", "web-vendor/chart.umd.js", "web-vendor/qrcode.min.js"} {
		filename := name
		route := "/" + name
		mux.HandleFunc("GET "+route, func(w http.ResponseWriter, r *http.Request) {
			f, e := assets.Open(filename)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			defer f.Close()
			w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(filename)))
			if filename == "index.html" || filename == "images.html" {
				w.Header().Set("Cache-Control", "no-cache")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=3600")
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = io.Copy(w, f)
		})
	}
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		f, _ := assets.Open("index.html")
		defer f.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = io.Copy(w, f)
	})
	srv := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", *port), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("本地网站 http://%s/", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}

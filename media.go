package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

var defaultMediaClient = &http.Client{Timeout: 5 * time.Minute}

func setMediaHeaders(w http.ResponseWriter, public bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	if public {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func (s *server) fetchR2(r *http.Request, key, byteRange string) (*http.Response, error) {
	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodGet, signedR2URL(s.r2Access, s.r2Secret, key, time.Now(), 900), nil)
	if err != nil {
		return nil, err
	}
	if byteRange != "" {
		upstream.Header.Set("Range", byteRange)
	}
	client := s.mediaClient
	if client == nil {
		client = defaultMediaClient
	}
	return client.Do(upstream)
}

func (s *server) proxyMedia(w http.ResponseWriter, r *http.Request, key string, public bool) {
	response, err := s.fetchR2(r, key, r.Header.Get("Range"))
	if err != nil {
		// Do not log signed URLs or credentials from network errors.
		log.Printf("R2 media request failed: key=%s", key)
		apiError(w, http.StatusBadGateway, "媒体读取失败，请稍后重试")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 && response.StatusCode != 206 {
		log.Printf("R2 media response: key=%s status=%d", key, response.StatusCode)
		if response.StatusCode == 416 {
			w.Header().Set("Content-Range", response.Header.Get("Content-Range"))
			apiError(w, 416, "请求范围无效")
			return
		}
		apiError(w, http.StatusBadGateway, "媒体文件暂不可用，请稍后重试")
		return
	}
	setMediaHeaders(w, public)
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if value := response.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, response.Body)
	}
}

func (s *server) servePreview(w http.ResponseWriter, r *http.Request, key, relative string) {
	hash := sha256.Sum256([]byte(key + ":" + relative))
	cacheFile := filepath.Join(".media-cache", fmt.Sprintf("%x.jpg", hash))
	if data, err := os.ReadFile(cacheFile); err == nil {
		setMediaHeaders(w, true)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(data)
		return
	}
	var reader io.ReadCloser
	if s.r2Access != "" && s.r2Secret != "" && key != "" {
		response, err := s.fetchR2(r, key, "")
		if err != nil {
			apiError(w, 502, "预览读取失败")
			return
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			apiError(w, 502, "预览读取失败")
			return
		}
		reader = response.Body
	} else {
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
		reader = file
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, (24<<20)+1))
	if err != nil || len(data) > 24<<20 {
		apiError(w, 502, "预览处理失败")
		return
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		apiError(w, 502, "预览格式不支持")
		return
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		apiError(w, 502, "预览处理失败")
		return
	}
	wid, hei := config.Width, config.Height
	if wid > 320 || hei > 320 {
		if wid >= hei {
			hei = max(1, hei*320/wid)
			wid = 320
		} else {
			wid = max(1, wid*320/hei)
			hei = 320
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, wid, hei))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	var out bytes.Buffer
	if err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 35}); err != nil {
		apiError(w, 500, "预览处理失败")
		return
	}
	if os.MkdirAll(filepath.Dir(cacheFile), 0700) == nil {
		if f, err := os.CreateTemp(filepath.Dir(cacheFile), "preview-*"); err == nil {
			_, writeErr := f.Write(out.Bytes())
			closeErr := f.Close()
			if writeErr == nil && closeErr == nil {
				_ = os.Rename(f.Name(), cacheFile)
			}
			_ = os.Remove(f.Name())
		}
	}
	setMediaHeaders(w, true)
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(out.Bytes())
}

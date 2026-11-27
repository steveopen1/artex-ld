// toolenv_download_test.go：环境准备的下载链路测试。
//
// 用一个真实的 httptest 服务端跑完整链路（真 HTTP 请求 → 真限流读取 →
// 真解压），而不是 mock。这一层最容易出事的地方恰恰是"真实字节流"：
// chunked 响应没有 Content-Length、服务端 302 到别处、内容是 gzip 而不是
// zip——mock 掉这些就等于把风险一起 mock 掉了。
package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newPrepTestServer builds a minimal Server good enough for the download path.
// envPrepDownload 只用到 s.m.GlobalProxy()，所以不需要 DB。
func newPrepTestServer(t *testing.T) *Server {
	t.Helper()
	return &Server{m: &Manager{}}
}

func TestEnvPrepDownloadFetchesZip(t *testing.T) {
	body := zipWith(t, map[string]string{"gogo_tool.exe": "MZ fake"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只断言"带了可识别的 UA"，不断言具体字样 —— 去标识化要求 UA 里不能出现
		// 产品名，锁死旧前缀会让这条测试在每次品牌调整时误报。
		if ua := r.Header.Get("User-Agent"); ua == "" {
			t.Errorf("下载请求必须带 User-Agent（部分 CDN 对空 UA 直接 403）")
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	s := newPrepTestServer(t)
	got, finalURL, sum, err := s.envPrepDownload(context.Background(), srv.URL+"/gogo-release.zip")
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("下载内容与原始字节不一致（%d vs %d）", len(got), len(body))
	}
	if finalURL != srv.URL+"/gogo-release.zip" {
		t.Errorf("final URL = %q", finalURL)
	}
	if len(sum) != 64 {
		t.Errorf("sha256 长度应为 64，实得 %d", len(sum))
	}
}

// TestEnvPrepDownloadChunked 覆盖不给 Content-Length 的服务端。
// 这类响应走 LimitReader 兜底，正是限额真正生效的路径。
func TestEnvPrepDownloadChunked(t *testing.T) {
	body := zipWith(t, map[string]string{"a.txt": "hi"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 不设 Content-Length，显式 Flush 让 Go 用 chunked 编码。
		fl := w.(http.Flusher)
		_, _ = w.Write(body[:len(body)/2])
		fl.Flush()
		_, _ = w.Write(body[len(body)/2:])
	}))
	defer srv.Close()

	s := newPrepTestServer(t)
	got, _, _, err := s.envPrepDownload(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("chunked 下载失败: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Error("chunked 响应内容不完整")
	}
}

// TestEnvPrepDownloadFollowsRedirect 确认 302 能被跟随，且 final URL 反映终点。
func TestEnvPrepDownloadFollowsRedirect(t *testing.T) {
	body := []byte("MZ\x90\x00binary")
	mux := http.NewServeMux()
	mux.HandleFunc("/real", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/real", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newPrepTestServer(t)
	got, finalURL, _, err := s.envPrepDownload(context.Background(), srv.URL+"/redirect")
	if err != nil {
		t.Fatalf("跟随重定向失败: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Error("重定向后的内容不正确")
	}
	if !strings.HasSuffix(finalURL, "/real") {
		t.Errorf("final URL 应指向终点，实得 %q", finalURL)
	}
}

// TestEnvPrepDownloadRejectsNonHTTP 确认 file:// / data: 这类 scheme 被拒。
func TestEnvPrepDownloadRejectsNonHTTP(t *testing.T) {
	s := newPrepTestServer(t)
	for _, u := range []string{
		"file:///etc/passwd",
		"data:text/plain;base64,aGk=",
		"ftp://example.com/x.zip",
	} {
		if _, _, _, err := s.envPrepDownload(context.Background(), u); err == nil {
			t.Errorf("%q 应当被拒绝", u)
		}
	}
}

func TestEnvPrepDownloadReportsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	s := newPrepTestServer(t)
	_, _, _, err := s.envPrepDownload(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("应报出 404，实得 %v", err)
	}
}

func TestEnvPrepDownloadRejectsEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := newPrepTestServer(t)
	if _, _, _, err := s.envPrepDownload(context.Background(), srv.URL); err == nil {
		t.Error("空响应应当被拒绝")
	}
}

// ─────── 整链路：下载 → 解压 → 识别入口 ───────

// TestEnvPrepEndToEndLayout 是这条能力的主验收：一个 zip 发布包从 HTTP
// 拿下来，解压到目标目录，入口程序被正确识别。
func TestEnvPrepEndToEndLayout(t *testing.T) {
	mainBin := "gogo_tool.exe"
	if runtime.GOOS != "windows" {
		mainBin = "gogo_tool"
	}
	body := zipWith(t, map[string]string{
		mainBin:           "MZ\x90\x00fake",
		"cfg/config.yaml": "scan: {}\n",
		"dict/pass.txt":   "123456\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	s := newPrepTestServer(t)
	raw, _, _, err := s.envPrepDownload(context.Background(), srv.URL+"/gogo.zip")
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}

	target := t.TempDir()
	res, err := envPrepExtract(target, raw)
	if err != nil {
		t.Fatalf("解压失败: %v", err)
	}
	if res.Archive != "zip" {
		t.Errorf("archive = %q，期望 zip", res.Archive)
	}
	entry := envPrepPickEntry(target, "gogo_tool", res.Written)
	if entry != mainBin {
		t.Errorf("入口识别 = %q，期望 %q", entry, mainBin)
	}
	// 目录结构与内容都要对得上。
	for rel, want := range map[string]string{
		mainBin:           "MZ\x90\x00fake",
		"cfg/config.yaml": "scan: {}\n",
		"dict/pass.txt":   "123456\n",
	} {
		p := filepath.Join(target, filepath.FromSlash(rel))
		got, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Errorf("期望的文件缺失 %s: %v", rel, rerr)
			continue
		}
		if string(got) != want {
			t.Errorf("%s 内容不符: %q", rel, string(got))
		}
	}
}

// TestEnvPrepEndToEndTarGz 覆盖 Linux 上更常见的 tar.gz 发布包形态。
func TestEnvPrepEndToEndTarGz(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name, content string, mode int64) {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: mode, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	bin := "fscan"
	if runtime.GOOS == "windows" {
		bin = "fscan.exe"
	}
	add(bin, "ELF fake", 0o755)
	add("dict/pass.txt", "admin\nroot\n", 0o644)
	_ = tw.Close()
	_ = gz.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	s := newPrepTestServer(t)
	raw, _, _, err := s.envPrepDownload(context.Background(), srv.URL+"/fscan.tar.gz")
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	target := t.TempDir()
	res, err := envPrepExtract(target, raw)
	if err != nil {
		t.Fatalf("解压失败: %v", err)
	}
	if res.Archive != "tar.gz" {
		t.Errorf("archive = %q，期望 tar.gz", res.Archive)
	}
	entry := envPrepPickEntry(target, "fscan", res.Written)
	if entry != bin {
		t.Errorf("入口识别 = %q，期望 %q", entry, bin)
	}
}

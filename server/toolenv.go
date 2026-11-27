// toolenv.go：外部工具的「环境准备」——把发布包下载并解压到受管目录。
//
// 为什么需要它：上传接口只接受单个文件。但真实的攻防工具很多是发布包
// （zip / tar.gz），里面有主程序、依赖 DLL、字典、POC 模板、yaml 配置、
// 甚至自带的 Python/Node 运行时。用户只传一个 exe 是装不起来的，
// 以前的办法是让用户在宿主上自己解压好、再想办法塞进来——这正是要消除的
// 手工步骤。
//
// 安全边界（这是本设计的全部要点）：
//
//  1. **只下载与解压，永不执行。** 不跑 install.sh / setup.py / postinst /
//     .bat，甚至不解压后 chmod +x 之外做任何动作。解压脚本型安装包是
//     供应链攻击最常见的入口，平台不碰这一块。
//  2. **只写入受管目录。** 每个解压条目都过 safeJoin，任何想写到
//     ../ 之外的条目直接判定整个包非法（不静默跳过——静默跳过会让用户
//     以为装好了）。
//  3. **下载源不做白名单。** 内网源、私有 OSS、GitHub release 都要能用，
//     白名单会把正常场景堵死。取而代之的是：不限地址，但把「哪个 URL、
//     什么时候、谁要求的、落了哪些文件、内容哈希」全部落库（tool_env_preps），
//     出问题可以完整回溯。
//  4. **不跟随到非预期协议。** 重定向只允许 http/https，跳转到 file://
//     data: 之类一律拒绝。
//
// 与 run_tool 的关系：环境准备只是"让工具存在"，真正调用仍然要经过
// tool_assets 的认领与契约校验。也就是说环境准备降低了 agent 的接入成本，
// 但没有放松任何一道运行时闸门。
package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"artex/db"
)

// 环境准备的硬限额。挡的是"把宿主磁盘塞满"和"解压炸弹"这两类事故，
// 不是防恶意——恶意包的解压是纯 CPU/磁盘动作，限额就是全部防线。
const (
	// envPrepMaxDownload 是单次下载上限（压缩包本体）。
	envPrepMaxDownload = 1024 << 20 // 1 GiB
	// envPrepMaxTotal 是解压后总量上限。压缩比超过 ~20:1 的包基本是炸弹。
	envPrepMaxTotal = 4 << 30 // 4 GiB
	// envPrepMaxFiles 是解压出的文件数上限。
	envPrepMaxFiles = 20000
	// envPrepMaxOneFile 是单个解压文件上限。
	envPrepMaxOneFile = int64(1024 << 20) // 1 GiB
	// envPrepTimeout 覆盖"下载 + 解压"的总时长。
	envPrepTimeout = 10 * time.Minute
)

// envPrepUserAgent 让上游能识别是谁在下（有些 CDN 对空 UA 直接 403）。
const envPrepUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// ─────────────────────────── 下载 ───────────────────────────

// envPrepHTTPClient builds the download client.
//
// 代理策略（这是刻意的，与平台其他出网请求不同）：**默认直连，只有显式
// 配置 tool_prep_proxy 时才走代理**。
//
// 为什么不能复用 global_proxy：那个设置定义为"所有**目标**流量走它"——
// 它是为渗透测试目标流量准备的，会被代理池轮换、健康检查、故障切换接管。
// 工具包下载不是目标流量：它从 GitHub release / 内网 OSS / 私有制品库拿文件。
// 让二者共用一条链路会带来两种真实故障：
//
//  1. 扫描代理挂掉时工具装不上（代理池 cooling 期间下载全失败）；
//  2. 代理存在时内网地址（127.0.0.1、10.x、*.internal）被塞进代理，
//     本该直连的请求绕道外网，直接失败。
//
// 所以这里用独立设置 tool_prep_proxy，缺省直连。需要代理出网的环境
// （内网隔离、需要跳板）显式配一次即可。
//
// 另外仍然尊重 HTTP(S)_PROXY 进程环境变量——容器部署常用这一套。
// 重定向只允许 http/https：一个 https 地址 302 到 file:// 会让下载变成读本地文件。
func (s *Server) envPrepHTTPClient() *http.Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   20 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
	if raw := strings.TrimSpace(s.toolPrepProxy()); raw != "" {
		if pu, err := url.Parse(raw); err == nil && pu.Host != "" {
			tr.Proxy = http.ProxyURL(pu)
		}
	}
	return &http.Client{
		Transport: tr,
		Timeout:   envPrepTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("重定向次数过多")
			}
			if sc := strings.ToLower(req.URL.Scheme); sc != "http" && sc != "https" {
				return fmt.Errorf("拒绝重定向到非 HTTP(S) 地址: %s", req.URL.Scheme)
			}
			return nil
		},
	}
}

// toolPrepProxySetting is the dedicated egress proxy for tool package
// downloads. Deliberately separate from global_proxy — see envPrepHTTPClient.
const toolPrepProxySetting = "tool_prep_proxy"

// toolPrepProxy returns the configured download proxy, or "" for direct.
func (s *Server) toolPrepProxy() string {
	if s.m == nil || s.m.pg == nil {
		return ""
	}
	v, ok, err := s.m.pg.GetSetting(toolPrepProxySetting)
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// envPrepDownload fetches rawURL into memory (bounded) and returns the bytes,
// the final URL after redirects, and the content hash.
//
// 落内存而不是直接落盘：解压前要先判格式（zip/tar/gz/裸二进制），而且
// 1 GiB 的上限决定了内存占用可控。真超过上限会在读的时候就被截断拒绝，
// 不会先把宿主磁盘写满再报错。
func (s *Server) envPrepDownload(ctx context.Context, rawURL string) (body []byte, finalURL, sum string, err error) {
	req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if rerr != nil {
		return nil, "", "", fmt.Errorf("下载地址无效: %w", rerr)
	}
	sc := strings.ToLower(req.URL.Scheme)
	if sc != "http" && sc != "https" {
		return nil, "", "", fmt.Errorf("只支持 http/https 下载地址，收到 %q", req.URL.Scheme)
	}
	req.Header.Set("User-Agent", envPrepUserAgent)
	req.Header.Set("Accept", "*/*")

	resp, derr := s.envPrepHTTPClient().Do(req)
	if derr != nil {
		return nil, "", "", fmt.Errorf("下载失败: %w", derr)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.Request.URL.String(), "", fmt.Errorf("下载地址返回 %d", resp.StatusCode)
	}

	// Content-Length 只是提前拒绝的快路径；真正的镜像在下面用 LimitReader
	// 兜住——不少服务端不给 Content-Length（chunked）。
	if resp.ContentLength > envPrepMaxDownload {
		return nil, resp.Request.URL.String(), "", fmt.Errorf(
			"发布包过大（%.1f MiB，上限 %d MiB）",
			float64(resp.ContentLength)/(1<<20), envPrepMaxDownload>>20)
	}

	h := sha256.New()
	var buf bytes.Buffer
	n, cerr := io.Copy(io.MultiWriter(&buf, h), io.LimitReader(resp.Body, envPrepMaxDownload+1))
	if cerr != nil {
		return nil, resp.Request.URL.String(), "", fmt.Errorf("读取下载内容失败: %w", cerr)
	}
	if n > envPrepMaxDownload {
		return nil, resp.Request.URL.String(), "", fmt.Errorf(
			"发布包超过上限 %d MiB，请改用更小的包或先自行裁剪", envPrepMaxDownload>>20)
	}
	if n == 0 {
		return nil, resp.Request.URL.String(), "", fmt.Errorf("下载内容为空")
	}
	return buf.Bytes(), resp.Request.URL.String(), hex.EncodeToString(h.Sum(nil)), nil
}

// ─────────────────────────── 落盘 ───────────────────────────

// safeJoin resolves an archive entry name under root, refusing escapes.
//
// Zip Slip 是所有解压实现的第一号漏洞：条目名写 ../../.ssh/authorized_keys
// 就能写到受管目录之外。这里同时挡三种写法：
//   - 绝对路径（/etc/cron.d/x、C:\Windows\x）
//   - 盘符与 UNC（C:、\\server\share）
//   - .. 逃逸（显式拒绝，不做静默改写）
//
// 为什么"拒绝"而不是"净化"：path.Clean("/a/../../b") 会折叠成 /b，看起来
// 安全地留在 root 里，但那意味着一个恶意包被悄悄"修正"后照常解压——用户
// 以为装的是官方包，实际内容和包里的路径对不上。更糟的是两个不同条目可以
// 折叠到同一路径，后写的覆盖先写的。所以这里对任何含 ".." 段的条目直接
// 判定整个包非法。
//
// 注意用 path（而不是 filepath）处理条目名：zip/tar 里的分隔符永远是 '/'，
// 在 Windows 上用 filepath 会把 'C:' 当成盘符语义。
func safeJoin(root, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("条目名为空")
	}
	// 统一分隔符后再判：Windows 打包工具会写反斜杠。
	n := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(n, "/") {
		return "", fmt.Errorf("条目 %q 是绝对路径", name)
	}
	// 盘符 / UNC：C:/x、C:x、//server/share
	if len(n) >= 2 && n[1] == ':' {
		return "", fmt.Errorf("条目 %q 带盘符", name)
	}
	if strings.HasPrefix(n, "//") {
		return "", fmt.Errorf("条目 %q 是 UNC 路径", name)
	}
	// 显式拒绝任何 ".." 段（不依赖 Clean 的折叠行为）。
	for _, seg := range strings.Split(n, "/") {
		if seg == ".." {
			return "", fmt.Errorf("条目 %q 含 .. 路径段，整个包被判定非法", name)
		}
	}
	clean := path.Clean("/" + n) // 现在只折叠 "." 与空段
	if clean == "/" {
		return "", fmt.Errorf("条目 %q 规范化后为空", name)
	}
	rel := strings.TrimPrefix(clean, "/")
	abs := filepath.Join(root, filepath.FromSlash(rel))
	// 二次确认：Join 之后必须仍在 root 之内（挡 Windows 上的短名/设备名怪例）。
	r := filepath.Clean(root)
	if abs != r && !strings.HasPrefix(abs, r+string(os.PathSeparator)) {
		return "", fmt.Errorf("条目 %q 逃出了目标目录", name)
	}
	return abs, nil
}

// envPrepTargetDirName derives a safe directory name from a tool name.
//
// 工具名会成为受管目录下的子目录名，所以复用上传路径的字符约束。
func envPrepTargetDirName(toolName string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(toolName))
	if !toolNameRe.MatchString(n) {
		return "", fmt.Errorf("工具名 %q 只能包含字母数字与 . _ -，且以字母数字开头", toolName)
	}
	return n, nil
}

// envPrepExtractResult summarizes one extraction.
type envPrepExtractResult struct {
	Archive    string // zip / tar.gz / tar / gz / binary
	Files      int
	TotalBytes int64
	// Written lists files written (relative, slash-separated), used for
	// picking the entry binary.
	Written []string
}

// envPrepExtract writes an archive's contents under dir.
//
// 它只做"写文件"，不做任何 chmod +x 之外的权限动作、不执行任何条目。
// 可执行位在 Unix 上按压缩包里的记录恢复（工具需要它才能跑），Windows
// 上不需要额外动作。
func envPrepExtract(dir string, body []byte) (*envPrepExtractResult, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建目标目录失败: %w", err)
	}
	switch envPrepSniff(body) {
	case "zip":
		return envPrepExtractZip(dir, body)
	case "tar.gz":
		return envPrepExtractTar(dir, body, true)
	case "tar":
		return envPrepExtractTar(dir, body, false)
	case "gz":
		return envPrepExtractGzip(dir, body)
	default:
		return nil, nil // 裸二进制，由调用方按文件名落盘
	}
}

// envPrepSniff identifies the container format from magic bytes.
func envPrepSniff(body []byte) string {
	switch {
	case len(body) >= 4 && body[0] == 'P' && body[1] == 'K' &&
		(body[2] == 3 || body[2] == 5 || body[2] == 7):
		return "zip" // PK\x03\x04 / PK\x05\x06 (empty) / PK\x07\x08 (spanned)
	case len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b:
		// gzip 可能是 tar.gz，也可能只是单文件 .gz。先看 gzip 里第一个头部
		// 是不是 tar（ustar 在偏移 257），解压一小段就能判。
		if gz, err := gzip.NewReader(bytes.NewReader(body)); err == nil {
			head := make([]byte, 512)
			n, _ := io.ReadFull(gz, head)
			_ = gz.Close()
			if n >= 262 && string(head[257:262]) == "ustar" {
				return "tar.gz"
			}
		}
		return "gz"
	case len(body) >= 262 && string(body[257:262]) == "ustar":
		return "tar"
	}
	return "binary"
}

func envPrepExtractZip(dir string, body []byte) (*envPrepExtractResult, error) {
	zr, err := newSkillZipReader(body) // 复用已注册 bzip2/zstd 解压器的读取器
	if err != nil {
		return nil, err
	}
	res := &envPrepExtractResult{Archive: "zip"}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := zipEntryName(f) // GBK 兜底解码，与 skill 上传一致
		dst, jerr := safeJoin(dir, name)
		if jerr != nil {
			return nil, jerr
		}
		if res.Files+1 > envPrepMaxFiles {
			return nil, fmt.Errorf("解压文件数超过上限 %d", envPrepMaxFiles)
		}
		if f.UncompressedSize64 > uint64(envPrepMaxOneFile) {
			return nil, fmt.Errorf("压缩包内文件过大: %s", name)
		}
		// 用记录里的解压后大小提前累加：Zip 的声明值可被伪造，所以下面
		// 写入时还用 LimitReader 兜一次。
		if res.TotalBytes+int64(f.UncompressedSize64) > envPrepMaxTotal {
			return nil, fmt.Errorf("解压后总量超过上限 %d MiB", envPrepMaxTotal>>20)
		}
		rc, oerr := f.Open()
		if oerr != nil {
			return nil, fmt.Errorf("打开条目 %s 失败: %w", name, oerr)
		}
		n, werr := envPrepWriteFile(dst, rc, res.TotalBytes)
		_ = rc.Close()
		if werr != nil {
			return nil, fmt.Errorf("写入 %s 失败: %w", name, werr)
		}
		// 执行位处理：zip 记录的 mode 要保留；很多 Linux 工具包（gogo/fscan/naabu）
		// 是在 Windows 上打的 zip，mode 恒为 0，解压后跑不了，入口识别也会判成
		// "没有可执行文件"。对「无扩展名」的条目补 +x —— 配置文件/字典都有扩展名，
		// 裸名字基本就是二进制本身。
		if f.Mode()&0o111 != 0 || !strings.Contains(path.Base(name), ".") {
			_ = os.Chmod(dst, 0o755)
		}
		res.Files++
		res.TotalBytes += n
		res.Written = append(res.Written, filepath.ToSlash(strings.TrimPrefix(dst, dir+string(os.PathSeparator))))
	}
	return res, nil
}

func envPrepExtractTar(dir string, body []byte, gzipped bool) (*envPrepExtractResult, error) {
	var r io.Reader = bytes.NewReader(body)
	if gzipped {
		gz, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("解压 gzip 失败: %w", err)
		}
		defer gz.Close()
		r = gz
	}
	tr := tar.NewReader(r)
	res := &envPrepExtractResult{Archive: "tar"}
	if gzipped {
		res.Archive = "tar.gz"
	}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取 tar 失败: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			// 正常文件，下面处理
		case tar.TypeDir, tar.TypeXGlobalHeader, tar.TypeXHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
			continue
		default:
			// 符号链接/硬链接/设备/FIFO 全部跳过：符号链接可以让后续写入
			// 落到目标目录之外（先放 ln -s /etc x，再往 x/passwd 写），
			// 这是 tar 解压的经典逃逸手法，不写最安全。
			continue
		}
		dst, jerr := safeJoin(dir, hdr.Name)
		if jerr != nil {
			return nil, jerr
		}
		if res.Files+1 > envPrepMaxFiles {
			return nil, fmt.Errorf("解压文件数超过上限 %d", envPrepMaxFiles)
		}
		if hdr.Size > envPrepMaxOneFile {
			return nil, fmt.Errorf("压缩包内文件过大: %s", hdr.Name)
		}
		if res.TotalBytes+hdr.Size > envPrepMaxTotal {
			return nil, fmt.Errorf("解压后总量超过上限 %d MiB", envPrepMaxTotal>>20)
		}
		n, werr := envPrepWriteFile(dst, tr, res.TotalBytes)
		if werr != nil {
			return nil, fmt.Errorf("写入 %s 失败: %w", hdr.Name, werr)
		}
		// tar 里记录的执行位要保留，否则解压出来的工具跑不了。
		if hdr.Mode&0o111 != 0 {
			_ = os.Chmod(dst, 0o755)
		}
		res.Files++
		res.TotalBytes += n
		res.Written = append(res.Written, filepath.ToSlash(strings.TrimPrefix(dst, dir+string(os.PathSeparator))))
	}
	return res, nil
}

// envPrepExtractGzip handles a plain .gz holding a single file (fscan_linux_amd64.gz
// is a common shape). The inner name comes from the gzip header, or the outer
// file name when absent.
func envPrepExtractGzip(dir string, body []byte) (*envPrepExtractResult, error) {
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("解压 gzip 失败: %w", err)
	}
	defer gz.Close()
	name := filepath.Base(strings.TrimSpace(gz.Name))
	if name == "" || name == "." || name == string(os.PathSeparator) {
		return nil, fmt.Errorf("gzip 未记录内部文件名，请改用 zip 或 tar.gz 包")
	}
	dst, jerr := safeJoin(dir, name)
	if jerr != nil {
		return nil, jerr
	}
	n, werr := envPrepWriteFile(dst, gz, 0)
	if werr != nil {
		return nil, fmt.Errorf("写入 %s 失败: %w", name, werr)
	}
	_ = os.Chmod(dst, 0o755)
	return &envPrepExtractResult{
		Archive: "gz", Files: 1, TotalBytes: n,
		Written: []string{filepath.Base(dst)},
	}, nil
}

// envPrepWriteFile streams src into dst with a per-file cap.
//
// already 是本次已写入的累计字节，用于把单文件上限收到"剩余总预算"内：
// 否则 20000 个 1 MiB 的文件就能绕过总量上限。
func envPrepWriteFile(dst string, src io.Reader, already int64) (int64, error) {
	budget := envPrepMaxOneFile
	if rest := envPrepMaxTotal - already; rest < budget {
		budget = rest
	}
	if budget <= 0 {
		return 0, fmt.Errorf("解压后总量超过上限 %d MiB", envPrepMaxTotal>>20)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	n, cerr := io.Copy(f, io.LimitReader(src, budget+1))
	closeErr := f.Close()
	if cerr != nil {
		return n, cerr
	}
	if closeErr != nil {
		return n, closeErr
	}
	if n > budget {
		return n, fmt.Errorf("文件超过大小上限 %d MiB", envPrepMaxOneFile>>20)
	}
	return n, nil
}

// ─────────────────────────── 主可执行文件识别 ───────────────────────────

// envPrepPickEntry picks the most plausible entry binary from a file list.
//
// 规则简单但覆盖了绝大多数发布包：
//  1. 只在"可执行"候选里挑（按平台扩展名，Unix 上还看执行位）。
//  2. 优先和工具名同名的；其次同一目录下的主程序（gogo_linux_amd64 → gogo）。
//  3. 深度浅的优先（根目录的 exe 比 thirdparty/x/y.exe 更像入口）。
//
// 猜错不是安全问题（只是给 keeper 一个提示），所以这里宁可给个候选也
// 不要求 100% 精确。
func envPrepPickEntry(dir, toolName string, files []string) string {
	type cand struct {
		rel   string
		depth int
		score int
	}
	var cands []cand
	want := strings.ToLower(toolName)
	for _, rel := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if !envPrepLooksExecutable(abs, rel) {
			continue
		}
		base := strings.ToLower(path.Base(rel))
		stem := strings.TrimSuffix(base, path.Ext(base))
		depth := strings.Count(rel, "/")
		// 打分：完全同名 100，名字里含工具名 60，其余 10。
		// 每深一层扣 5，避免选到 examples/ 或 tools/ 里的附属程序。
		score := 10
		switch {
		case stem == want:
			score = 100
		case strings.HasPrefix(stem, want) || strings.Contains(stem, want):
			score = 60
		}
		score -= depth * 5
		cands = append(cands, cand{rel: rel, depth: depth, score: score})
	}
	if len(cands) == 0 {
		return ""
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		if cands[i].depth != cands[j].depth {
			return cands[i].depth < cands[j].depth
		}
		return cands[i].rel < cands[j].rel
	})
	return cands[0].rel
}

// envPrepLooksExecutable reports whether a file looks runnable on this host.
func envPrepLooksExecutable(abs, rel string) bool {
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		return false
	}
	ext := strings.ToLower(path.Ext(rel))
	switch runtime.GOOS {
	case "windows":
		switch ext {
		case ".exe", ".bat", ".cmd", ".ps1", ".com":
			return true
		}
	case "darwin":
		switch ext {
		case ".sh", ".py", ".command":
			return true
		}
		return fi.Mode()&0o111 != 0
	default:
		switch ext {
		case ".sh", ".py":
			return true
		}
		if fi.Mode()&0o111 != 0 {
			return true
		}
		// 无扩展名：Linux 工具二进制（gogo/fscan/naabu/httpx…）的典型形态。
		// 不认这一条，解压出来的入口永远识别不到。
		return ext == ""
	}
	return false
}

// ─────────────────────────── 编排 ───────────────────────────

// toolEnvPrepRequest is the shared input shape for the tool and the HTTP route.
type toolEnvPrepRequest struct {
	ToolName string `json:"tool_name"`
	URL      string `json:"url"`
	// EntryHint lets the caller override the auto-detected entry binary
	// (relative path inside the extracted dir).
	EntryHint string `json:"entry_bin"`
	// Claim asks the platform to wake tool_keeper after a successful prep.
	Claim bool `json:"claim"`
}

// toolEnvPrep is the end-to-end preparation: download → extract → register.
//
// 返回值直接给 agent 用：它需要知道落在哪、主程序是哪个、登记成功没有。
// 任何一步失败都会在 tool_env_preps 里留一条 state=failed 的记录——失败
// 也要可追溯，否则"我明明让它装过"就成了不可查的悬案。
func (s *Server) toolEnvPrep(ctx context.Context, req toolEnvPrepRequest, by string) (map[string]any, error) {
	pg := s.m.pg
	if pg == nil {
		return nil, fmt.Errorf("数据库不可用")
	}
	name, nerr := envPrepTargetDirName(req.ToolName)
	if nerr != nil {
		return nil, nerr
	}
	rawURL := strings.TrimSpace(req.URL)
	if rawURL == "" {
		return nil, fmt.Errorf("缺少下载地址")
	}

	rec := &db.ToolEnvPrep{ToolName: name, URL: rawURL, By: by, State: "failed"}
	// fail closes out the audit row and returns the error, so every early
	// return leaves a trace without repeating the insert four times.
	fail := func(err error) (map[string]any, error) {
		rec.Error = err.Error()
		if _, ierr := pg.InsertToolEnvPrep(rec); ierr != nil {
			return nil, fmt.Errorf("%v（且审计写入失败: %v）", err, ierr)
		}
		return nil, err
	}

	dir := s.toolAssetDir()
	// 目标子目录：<受管目录>/<tool>。已在受管目录内，safeJoin 的 root 也用它。
	target := filepath.Join(dir, name)
	// 重装语义：先清掉同名目录，避免旧版本残留文件混进新版本（残留的旧
	// DLL 会造成"新版本起不来"这种极难排查的问题）。只删受管目录内的路径。
	if strings.HasPrefix(filepath.Clean(target), filepath.Clean(dir)+string(os.PathSeparator)) {
		_ = os.RemoveAll(target)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fail(fmt.Errorf("创建目标目录失败: %w", err))
	}

	dlCtx, cancel := context.WithTimeout(ctx, envPrepTimeout)
	defer cancel()
	body, finalURL, sum, derr := s.envPrepDownload(dlCtx, rawURL)
	if derr != nil {
		// 失败必须清掉刚建的空目录：留着会让下次重装误以为有残留，
		// 也会让 toolAssetDir 的目录清单多一个空壳。
		_ = os.RemoveAll(target)
		return fail(derr)
	}
	rec.FinalURL = finalURL
	rec.SHA256 = sum
	rec.Downloaded = int64(len(body))

	res, xerr := envPrepExtract(target, body)
	if xerr != nil {
		_ = os.RemoveAll(target)
		return fail(xerr)
	}
	if res == nil {
		// 裸二进制：没有容器可解，按 URL 末段（无则用工具名）落一个文件。
		fname := envPrepBinaryName(finalURL, name)
		dst, jerr := safeJoin(target, fname)
		if jerr != nil {
			_ = os.RemoveAll(target)
			return fail(jerr)
		}
		n, werr := envPrepWriteFile(dst, bytes.NewReader(body), 0)
		if werr != nil {
			_ = os.RemoveAll(target)
			return fail(fmt.Errorf("写入 %s 失败: %w", fname, werr))
		}
		_ = os.Chmod(dst, 0o755)
		res = &envPrepExtractResult{Archive: "binary", Files: 1, TotalBytes: n, Written: []string{fname}}
	}
	rec.Archive = res.Archive
	rec.FileCount = res.Files
	rec.TotalBytes = res.TotalBytes

	// The relative dir is what goes into tool_assets.rel_path's directory part.
	relDir, rerr := filepath.Rel(dir, target)
	if rerr != nil {
		_ = os.RemoveAll(target)
		return fail(fmt.Errorf("计算相对路径失败: %w", rerr))
	}
	relDir = filepath.ToSlash(relDir)
	rec.TargetDir = relDir

	// Entry binary: caller's hint wins, else auto-detect.
	entry := strings.TrimSpace(req.EntryHint)
	if entry != "" {
		if _, jerr := safeJoin(target, entry); jerr != nil {
			_ = os.RemoveAll(target)
			return fail(fmt.Errorf("指定的入口程序路径非法: %w", jerr))
		}
		entry = filepath.ToSlash(strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(entry, "\\", "/")), "/"))
	} else {
		entry = envPrepPickEntry(target, name, res.Written)
	}
	rec.EntryBin = entry
	if entry == "" {
		// 没有可执行文件也算成功：有些包只是字典/模板库，供 run_tool 的
		// 参数引用。但要让调用方知道"这里没有主程序"。
		rec.State = "ok"
		if _, ierr := pg.InsertToolEnvPrep(rec); ierr != nil {
			return nil, fmt.Errorf("审计写入失败: %w", ierr)
		}
		s.syncToolAssetsToCatalog()
		return map[string]any{
			"ok": true, "tool": name, "dir": relDir, "files": res.Files,
			"archive": res.Archive, "sha256": sum, "downloaded": rec.Downloaded,
			"entry_bin": "", "note": "包内未识别出可执行文件（可能只是字典/模板库）",
			"prep_id": rec.ID,
		}, nil
	}

	// Register as a tool asset so it enters the normal claim → contract → run_tool
	// pipeline. 环境准备只是"让文件存在"，运行时闸门一道都没松。
	entryAbs := filepath.Join(target, filepath.FromSlash(entry))
	fi, serr := os.Stat(entryAbs)
	if serr != nil {
		return fail(fmt.Errorf("入口程序不存在: %s", entry))
	}
	asset := &db.ToolAsset{
		Name:        name,
		DisplayName: entry,
		SHA256:      sum,
		SizeBytes:   fi.Size(),
		Platform:    detectToolPlatform(entryAbs),
		RelPath:     filepath.ToSlash(path.Join(relDir, entry)),
		State:       "uploaded",
		Limits:      db.DefaultContractJSON("utility"),
	}
	replaced := false
	if prev, perr := pg.ToolAssetByName(name, asset.Platform); perr == nil && prev != nil {
		asset.ID = prev.ID
		replaced = true
		if err := pg.ReplaceToolAssetBinary(prev.ID, asset); err != nil {
			return fail(fmt.Errorf("覆盖登记失败: %w", err))
		}
	} else if _, err := pg.InsertToolAsset(asset); err != nil {
		return fail(fmt.Errorf("登记失败: %w", err))
	}
	if len(asset.Limits) > 0 {
		_ = pg.SetToolAssetLimits(asset.ID, asset.Limits)
	}
	rec.AssetID = asset.ID
	rec.State = "ok"
	if _, ierr := pg.InsertToolEnvPrep(rec); ierr != nil {
		// 登记成功但审计失败：明确说出来，别让调用方以为一切正常。
		return nil, fmt.Errorf("工具已登记（asset_id=%d）但审计写入失败: %w", asset.ID, ierr)
	}
	s.syncToolAssetsToCatalog()

	// Wake the keeper so the tool gets claimed (classified + contract) without
	// the user having to do anything.
	if req.Claim {
		s.toolassets_triggerClaim([]map[string]any{{"name": name}})
	}

	return map[string]any{
		"ok": true, "tool": name, "asset_id": asset.ID, "dir": relDir,
		"entry_bin": entry, "archive": res.Archive, "files": res.Files,
		"sha256": sum, "downloaded": rec.Downloaded, "replaced": replaced,
		"prep_id": rec.ID,
	}, nil
}

// envPrepBinaryName derives a file name for a bare-binary download.
//
// URL 末段可信度不高（可能带查询串或就是个 id），所以只取路径基名并做
// 字符校验；不合格就退回 "<tool>"。
func envPrepBinaryName(finalURL, toolName string) string {
	base := ""
	if u, err := url.Parse(finalURL); err == nil {
		base = path.Base(u.Path)
	}
	base = strings.TrimSpace(base)
	if base == "" || base == "/" || base == "." || !toolNameRe.MatchString(base) {
		if runtime.GOOS == "windows" {
			return toolName + ".exe"
		}
		return toolName
	}
	return base
}

// Package scopetext 从 shell 命令文本里提取「这次执行会打到哪些网络目标」。
//
// 为什么需要它：平台的范围校验（db.ScopeMatcher）只作用在 run_tool 受管入口上，
// 而 worker 完全可以用 Bash 直接跑 curl / nmap / nuclei —— 那条路径平台看不到
// 目标，范围约束等于不存在。要把它补上，第一步是从任意命令文本里把目标找出来。
//
// 设计取舍（宁可漏、不可误杀）：
//   - 文本层必然有盲区：脚本执行、管道、变量拼接、base64、先写文件再跑，
//     这些在字符串层面无法还原真实目标；
//   - 因此本包只做**高置信度**提取，解析不出就返回空 —— 调用方放行并记审计，
//     而不是猜一个目标去拦。误杀正常命令的代价远高于漏掉一次判定，
//     漏掉的部分由网络层（代理）兜底。
//
// 高置信度目标 = 绝对 URL / IPv4（含端口与 CIDR）/ 以及"命令里确实调用了网络工具"
// 时的裸主机名。最后一条必须限定工具上下文，否则 `python3 script.py` 里的
// script.py 会被当成域名。
package scopetext

import (
	"net"
	"regexp"
	"strings"
)

var (
	// 绝对 URL：http(s)。
	// 结束符排除 shell 里常见的引号/管道/分号/重定向，外加 **$ ` { }** ——
	// 这几个是 shell 变量/命令替换/花括号展开的起始符。实测踩过：
	//   curl "http://127.0.0.1:8811$p"
	// 若不排除 $，会提取出 "http://127.0.0.1:8811$p"，url.Parse 后 host 非法，
	// InScope 判 false，于是**范围内**的合法命令被误拦。误拦比漏判严重得多。
	reURL = regexp.MustCompile("https?://[^\\s'\"\\\\|;&)<>$`{}]+")

	// IPv4 字面量，可选 :port，可选 /mask。
	reIPv4 = regexp.MustCompile(`\b(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})(:\d{1,5})?(/\d{1,2})?\b`)

	// 形如 host.tld 的 token（用于网络工具上下文下的裸主机名提取）。
	reHostToken = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9\-]{0,62})(\.[A-Za-z0-9]([A-Za-z0-9\-]{0,62}))+\.[A-Za-z]{2,24}$`)

	// bash 的 /dev/tcp 与 /dev/udp 重定向：`echo > /dev/tcp/host/80`。
	// 实测被 agent 用来绕过（token 以 "/" 开头，原先会被跳过）。
	// 这条几乎不可能有误报 —— /dev/tcp 就是 bash 的裸 socket 语法。
	reDevTCP = regexp.MustCompile(`/dev/(?:tcp|udp)/([A-Za-z0-9.\-]+|\[[0-9a-fA-F:]+\])/(\d{1,5})`)

	// openssl s_client -connect host:port / nc host port 这类"主机+端口"组合。
	reConnectFlag = regexp.MustCompile(`(?:-connect|--connect)[\s=]+([A-Za-z0-9.\-]+|\[[0-9a-fA-F:]+\]):(\d{1,5})`)
)

// networkTools 是"会主动建立网络连接"的命令名。只有当命令里出现其中之一，
// 裸主机名才会被当作目标 —— 这是避免误杀 `python3 script.py` 的关键。
var networkTools = map[string]bool{
	"curl": true, "wget": true, "httpie": true, "http": true, "https": true,
	"nmap": true, "naabu": true, "masscan": true, "zmap": true,
	"nuclei": true, "httpx": true, "katana": true, "ffuf": true, "gobuster": true,
	"dirsearch": true, "feroxbuster": true, "wfuzz": true, "gospider": true,
	"subfinder": true, "amass": true, "assetfinder": true, "dnsx": true,
	"gogo": true, "fscan": true, "kscan": true, "afrog": true, "xray": true,
	"dig": true, "host": true, "nslookup": true, "whois": true,
	"ssh": true, "scp": true, "sftp": true, "telnet": true, "nc": true, "ncat": true,
	"openssl": true, "smbclient": true, "rpcclient": true, "enum4linux": true,
	"msfconsole": true, "sqlmap": true, "hydra": true, "medusa": true,
	"whatweb": true, "wafw00f": true, "nikto": true, "arjun": true,
}

// fileExts 是"看起来像文件名"的扩展名。带这些扩展名的 token 即使在网络
// 工具上下文里也不当目标是 —— 例如 `nuclei -t sqli.yaml`、`ffuf -w dict.txt`。
var fileExts = map[string]bool{
	"py": true, "sh": true, "bash": true, "zsh": true, "js": true, "mjs": true, "ts": true,
	"go": true, "php": true, "rb": true, "pl": true, "lua": true, "ps1": true,
	"txt": true, "json": true, "yml": true, "yaml": true, "xml": true, "csv": true,
	"md": true, "html": true, "htm": true, "zip": true, "tar": true, "gz": true,
	"tgz": true, "bz2": true, "xz": true, "7z": true, "log": true, "conf": true,
	"ini": true, "cfg": true, "toml": true, "lock": true, "pem": true, "crt": true,
	"key": true, "db": true, "sqlite": true, "sql": true, "bin": true, "so": true,
	"dll": true, "exe": true, "png": true, "jpg": true, "jpeg": true, "gif": true,
	"svg": true, "pdf": true, "doc": true, "docx": true, "xls": true, "xlsx": true,
}

// MaxTargets 是一次提取的目标上限。命令被拼接成超长行时不至于把内存和审计
// 记录撑爆；超出部分直接丢弃（属于"漏判"，符合本包宁可漏不可误杀的取舍）。
const MaxTargets = 64

// Targets 返回命令文本里高置信度的网络目标（已去重、保持出现顺序）。
// 解析不出任何目标时返回空切片 —— 调用方据此放行并记审计。
func Targets(cmd string) []string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, 8)
	push := func(t string) {
		t = strings.TrimRight(strings.TrimSpace(t), ".,;:)]}>")
		if t == "" || seen[t] {
			return
		}
		if len(out) >= MaxTargets {
			return
		}
		seen[t] = true
		out = append(out, t)
	}

	// 1) 绝对 URL —— 最高置信度，直接采信。
	for _, m := range reURL.FindAllString(cmd, -1) {
		push(m)
	}
	// 1b) bash /dev/tcp 与 openssl/nc 的 host:port 组合 —— 高置信度的裸 socket 用法。
	//     这两类不含 scheme，URL 正则抓不到，且 token 常以 "/" 开头被主机名规则跳过，
	//     实测是 agent 的绕过首选。
	for _, m := range reDevTCP.FindAllStringSubmatch(cmd, -1) {
		push(m[1])
	}
	for _, m := range reConnectFlag.FindAllStringSubmatch(cmd, -1) {
		push(m[1])
	}

	// URL 里已经带了目标，先把它们从文本里扣掉，避免同一目标被后面的
	// IP/主机名规则重复提取（http://10.0.0.1:8080 不应同时产出 "10.0.0.1:8080"）。
	scrubbed := reURL.ReplaceAllString(cmd, " ")

	// 2) IPv4（含端口 / CIDR）。要求每段是合法十进制，挡掉 999.1.1.1 这类版本号。
	for _, m := range reIPv4.FindAllStringSubmatch(scrubbed, -1) {
		if !validIPv4(m[1]) {
			continue
		}
		push(m[0])
	}

	// 3) 裸主机名：只在命令确实调用了网络工具时才采信。
	//    同样先扣掉已提取的 IP，避免 "10.0.0.1:8080" 又被当作 host:port 解析一遍。
	if !usesNetworkTool(cmd) {
		return out
	}
	scrubbed = reIPv4.ReplaceAllString(scrubbed, " ")
	for _, tok := range strings.Fields(scrubbed) {
		t := strings.Trim(tok, `"'` + "`")
		t = strings.TrimPrefix(t, "//")
		// 去掉 host:port 的端口部分再判形态
		if h, _, err := net.SplitHostPort(t); err == nil {
			t = h
		}
		if t == "" || strings.HasPrefix(t, "-") || strings.HasPrefix(t, "/") ||
			strings.HasPrefix(t, ".") || strings.HasPrefix(t, "$") ||
			strings.HasPrefix(t, "@") || strings.HasPrefix(t, "%") {
			continue
		}
		if !reHostToken.MatchString(t) {
			continue
		}
		if fileExts[strings.ToLower(t[strings.LastIndex(t, ".")+1:])] {
			continue
		}
		push(t)
	}
	return out
}

// usesNetworkTool 判断命令里是否调用了会主动联网的工具。
// 只看每个 token 的 basename，兼容 /usr/bin/curl 与 env 前缀。
func usesNetworkTool(cmd string) bool {
	for _, tok := range strings.Fields(cmd) {
		base := tok
		if i := strings.LastIndexAny(base, `/\`); i >= 0 {
			base = base[i+1:]
		}
		if networkTools[strings.ToLower(base)] {
			return true
		}
	}
	return false
}

func validIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 3 {
			return false
		}
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
			n = n*10 + int(c-'0')
		}
		if n > 255 {
			return false
		}
	}
	return true
}

// HostOfURL 返回绝对 URL 的主机名（无端口、小写）。非 URL 输入原样返回小写。
func HostOfURL(raw string) string {
	rest := strings.TrimSpace(raw)
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		rest = rest[i+1:]
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 && !strings.Contains(rest, "]") {
		rest = rest[:i]
	}
	return strings.ToLower(strings.TrimSpace(rest))
}

// PathOfURL 返回 URL 的路径部分（不含 query），根路径返回空串。
func PathOfURL(raw string) string {
	rest := strings.TrimSpace(raw)
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	i := strings.IndexAny(rest, "/?#")
	if i < 0 {
		return ""
	}
	rest = rest[i:]
	if j := strings.IndexAny(rest, "?#"); j >= 0 {
		rest = rest[:j]
	}
	if rest == "/" {
		return ""
	}
	return rest
}

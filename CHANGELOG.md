# artex-ld 更新日志

本项目的重要变更记录在此文件中，格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。

版本说明：`artex-ld` 从 `0.1.2` 起使用独立版本序列。Release 名称为 **artex-ld 0.2.3**，Git Tag 为 **v0.2.3**。`0.3.16`–`0.3.18` 是 [RuoJi6/ARTEX](https://github.com/RuoJi6/ARTEX) 的上游条目（合并带入）；`0.3.15` 及更早内容是 [Autumn-27/ARTEX](https://github.com/Autumn-27/ARTEX) 历史。

## [Unreleased]

### Added

- **主按钮改为中性浅灰**（`--primary: #c8ccd4`）。实测参考的「新建项目」按钮取色
  为浅灰 `#b1b1b1` 配深色文字 —— 参考全站按钮都是中性色，蓝色只出现在极少数强调位
  （logo、进度条当前段、侧栏选中指示条）。此前把 `--primary` 设为品牌蓝 `#5ca6ff`，
  导致 51 处 `bg-primary` 的控件（含顶栏设置/主题切换图标按钮、新建任务按钮）全部是蓝底，
  与参考的克制用法相反。改 `--primary` 一处即全站统一。
- **登录页左侧面板去色块**（`(auth)/login/page.tsx` + `.login-brand-panel`）。原来是
  `bg-primary` 铺满三分之一屏 —— 品牌色只适合按钮/链接这种小面积，大面积铺就是一块
  刺眼的高饱和蓝。改为深空底 + 两记光池 + 同心辅助圆环，与暗色主题一致。
- **Abyss（深空）暗色主题预设**（`web/src/styles/presets/abyss.css`）。参考
  [alkaid](https://github.com/steveopen1/alkaid) 的暗色体系落地，三条设计规则：
  暗色里"浮起的面"要更亮而非更暗；层次靠半透描边而非色块；语义色保持同一柔和档。
  提供深蓝黑画布（`#1a202b`）、比画布亮一档的卡片、品牌蓝交互色，并补齐侧栏选中态、
  输入控件、吸顶表头、玻璃面板、深井面板等表面样式。切到其它预设时全部不生效。
  背板与配色按参考实现（`webapp/src/styles/base.css` + `alkaid.css`）逐项对齐：
  - **背景只有三样**：28px 点阵、两记极淡环境光池、底色。**没有网格线** —— 参考里
    `--topo-grid` 只用于拓扑图画布，之前我把它当成页面背景做成了 CAD 蓝图，已全部删除。
  - **颜色与材质改为对 alkaid 暗色截图做像素级测量**（此前一直从 CSS 变量反推，
    而 `--alkaid-panel-solid: #252b37` 是"玻璃的不透明等价色"，实际合成出来是深蓝档）。
    对 1895×974 截图主内容区密集采样得到的真实值：
    外壳 `#0f1216` / 侧栏 `#1b2330` / 画布 `#1b2331` / 卡片 `#202939`。
  - **玻璃感的真正构造**（读 `theme/industries.ts` 的 `shell.dark` 才找到）：
    参考的暗色是**外壳 + 左右两块同材质玻璃**，不是一层底色：
    外壳 `--shell-chrome: #0A0F1A`，左右两列各覆 `--shell-panel/-sidebar: rgba(38,48,68,0.55)`。
    `color-mix(#263044 55%, #0A0F1A)` = `#192131`，截图实测 `#1b2331` —— 差 2 阶。
    所以那层半透**不能改成实色**：数值虽然对得上，但会从"一块玻璃"退化成"一块平色板"
    （我上一版就是这么错的）。
  - 玻璃必须叠在**外壳**上才会压暗；若 html 铺 `--background`，玻璃叠上去反而亮成
    `#1f2838`（实测），这就是"玻璃把底变亮"的原因。
  - 背板只保留参考实有的两样：28px 点阵（`rgba(255,255,255,.028)`）与两记极淡中性灰
    环境光池（`.055` / `.04`）。点阵被玻璃 blur 抹成磨砂 —— 这是玻璃质感的物理来源。
  - 此前加的光池(0.16)、线网格、扫描光带全部删除：放大 6 倍看参考，背景是平滑纯色。

- **平台拦截提醒可配置 + 附带流量证据**（`guard/blockmessage.go`、`guard/guard.go`、
  `server/intercept_evidence.go`、`settings`）。此前拦截只把一句写死前缀的文案内联进
  该次工具调用的 `tool_result`，用户自定义的规则 message 塞不进工具名/命令，deny 也不写
  会话流。现在：
  - 模板可配（`settings.intercept_block_message`），支持 `{tool}` `{rule}` `{reason}`
    `{command}` `{action}` 变量；空值回落内置默认（与历史文案等价）。超长命令按 200 字截断。
  - 每次拦截额外往 worker 会话流写一条 `intercept_denied` 活动（deny / 审批未通过 /
    已取消三种），与 `ask` 的审批卡对齐，前端能在对应会话里看到。
  - 提醒自动附带当次真实报文：从被拦命令里抽 `method + URL`，在 30 分钟窗口内按
    host/path 查录制流量；精确 method 命中优先，写操作通常未发出时退回同 host+path
    的邻近报文。找不到就不附，不凭空猜。
  - 开关 `settings.intercept_traffic_evidence`（默认开）。
- **资产图谱显式边**（`db/asset_edges.go`、`db/schema.sql`）。新增 `asset_edges`
  表记录 `root_domain --contains--> subdomain --resolves_to--> ip --hosts--> service
  --exposes--> endpoint`，由各 `Upsert*` 在写资产时维护（best-effort，失败不影响入库）。
  读侧提供 `AssetNeighbors`（递归 CTE 展开子树 + 直属上级，深度上限 10）与
  `AssetPath`（最短连通路径）。此前 `assets` 只能靠 `bound_domains` / `record_value` /
  url 前缀在应用层拼关系，既无法遍历也无法回答"这个站下面有哪些端点"。
- **`asset_neighbors` 工具**（`agent/tools_insert.go`）与
  `GET /api/assets/{id}/neighbors`。planner 提示词里早就写了"需要细节可调
  asset_neighbors"，但工具此前不存在——这条指令指向了空。现已绑定 worker /
  mainagent / planner，前端补 `api.assetNeighbors()` 客户端方法。
- **护栏统计回流 planner**（`guard/guard.go`、`agent/tools.go`、`agent/planner.go`、
  `server/task_llm.go`）。guard 早就在累计 allow/deny/ask 与目标侧 WAF 命中，但从来没有
  任何代码读取；`graph_overview` 现输出 `guard_stats`（denied / ask_denied /
  ask_allowed / allowed / target_blocked + 分工具明细，最多 10 行），planner 提示词
  新增一条判据区分「平台策略在挡」与「目标侧 WAF」，避免把合规拦截当成待绕过的障碍
  而每轮重复派同样的意图。
- **`api-authz` 技能 + CWE→技能索引**（`skills/api-authz/`）。越权（BOLA/BFLA）
  双身份差分方法论：接口清单 → 匿名/低权A/低权B 三身份矩阵 → 响应字段级差异判定，
  附误报排除清单与证据要求；`references/cwe-skill-index.md` 给出 CWE-284/639/862/863
  等 30 余条到内置技能的映射。默认绑定 `worker` / `pentest`，并加进 planner 的技能集。

### Fixed

- **zip 包解压丢失执行位**（`server/toolenv.go`）。zip 分支不像 tar 分支那样恢复
  `0o755`，导致 Linux 上解压出来的工具跑不了、`envPrepPickEntry` 也判定"包内未识别出
  可执行文件"；`TestEnvPrepEndToEndLayout`、`TestToolEnvPrepEndToEndPG`、
  `TestToolEnvPrepReplacesCleanly` 因此在 Linux 上恒失败。现保留 zip 记录的执行位，
  并对无扩展名条目（gogo/fscan/naabu 这类裸二进制）补 `+x`。


## [0.2.3] - 2026-11-25

### Fixed

- **压缩在自适应思考模型上产出空正文 / 半截正文**（`agent/compaction.go`）。
  压缩固定 `max_tokens=1500`，而生产块（单节点 500+ 字、多 id/约束）会让
  MiniMax-M3.1-Flash 把预算全花在思考上：`finish_reason=length`、正文为空。
  实测同一端点：1500 → 0 字正文 / 1500 思考 token；8192 → 1435 字正文 / 7626 token
  （仅剩约 7% 余量）；更密的尾部需要 16384。现改为三段预算：

  | 阶段 | max_tokens | 超时 | 触发 |
  | --- | --- | --- | --- |
  | 首试 | 1500 | 90s | 所有块（简单块保持低成本） |
  | 升级 | 8192 | 300s | 空正文或半截正文，且 `stop=max_tokens` |
  | 兜底 | 16384 | 300s | 8192 仍被截断 |

  正文非空但 `stop=max_tokens` 也视为截断并升级；最终仍截断时返回最长的一份
  正文而不是报错（不丢已产生的结论）。单次超时按「可转移的瞬时失败」处理：
  批次 ctx 仍存活时升级重试，HTTP 400/401 与取消不重试。错误信息带
  `stop` / `output_tokens` / 最终预算，日志可直接判断是截断还是超时。
- 新增真实端点集成测试 `agent/compaction_live_test.go`（默认跳过）：
  38 个 500+ 字节点、真实 `compress()` 路径，验证 1500 → 8192 升级并产出正文。
- 修正测试注释中无法复核的「360 次」说法：可核记录为 11-24 09:00–18:32 的 34 条
  disabled 报错，更早数据已被 `llm_records` 保留策略清理。

## [0.2.2] - 2026-11-25

### Changed

- **Release 流水线不再被可选集成阻断**（`.github/workflows/release.yml`）：
  `ARTEX_DOCKER_IMAGE` 未配置时跳过 Docker 构建/发布，前端、五平台二进制与
  GitHub Release 照常产出；Docker 已配置但构建失败时仍会阻止发布。
- **CI 自动产出完整资产**：5 个平台的 zip + linux-amd64 一键部署包 +
  覆盖两者的 `SHA256SUMS`；同一 tag 重跑时检测到「已发布且指向当前提交」改为
  notice 跳过，不再把流水线染红。
- **修复仓库内脚本缺少执行位导致 CI 失败**：`build.sh` 等以 `100644` 入库，CI 直接
  `./build.sh` 会以 exit 126 失败；本次把构建/部署脚本的 git 模式改为 `100755`，
  并让 CI 统一用 `bash build.sh` 调用，避免再次依赖执行位。
- **Demo 工作流降级为可选部署**（`.github/workflows/demo.yml`）：Release 资产不完整、
  非 latest、或缺少 Vercel 变量/密钥时输出 notice 并跳过，而不是失败；
  配置齐全时部署行为不变。
- 版本元数据统一为 `0.2.2`（`Dockerfile.source` / `docker-compose.yml` / `.env.example` / `web/package.json`）。

### Fixed

- v0.2.1 Release 补齐 `linux-arm64` / `darwin-amd64` / `darwin-arm64` / `windows-amd64`
  与完整 `SHA256SUMS`，修复 Demo 流水线因资产不完整而失败的问题。

## [0.2.1] - 2026-11-25

### Changed

- **README 按仓库真实能力重写**：能力矩阵（标注来源与可选依赖）、二开增强点、Mermaid
  架构图/时序图/状态机/流程图、部署运维、安全模型、能力边界与已知限制；并修正原文档中
  `install.sh` 注册 systemd、Release 覆盖全平台、审核接口路径与失效文档链接等不实描述。
- 版本元数据统一为 `0.2.1`（`Dockerfile.source` / `docker-compose.yml` / `.env.example` /
  `web/package.json`）。Docker 构建此前默认注入 `0.1.4`，会导致 banner、健康检查与更新比较
  显示旧版本，本次一并修正。

### Fixed

- **新部署不给 worker / pentest 绑定方法论 skill**（`db/db.go`）。移植进来的 6 个 PTS skill
  （fingerprint-finder / api-tester / sensitive-info-leak / route-guard-bypass /
  response-modifier / tool-arsenal）只在 `b941a95` 里落了文件，`builtinSkillVisibility`
  仅登记了 `api-recon`，`seedPlannerSkillVisibility` 也只管 planner。结果是全新部署下
  worker 与 pentest 的工具集里只有 `api-recon` —— 方法论 skill 文件在磁盘上却永远加载
  不到，等价于没装；agent 因此不会去派发需要这些方法的子任务。生产库看起来正常，是因为
  历史上有人在 UI 里手工勾选过，属幸存者偏差。
  该缺陷从 `b941a95` 起被 `skills_injection_test.go` 的断言覆盖，但那条测试有
  `ARTEX_PG_DSN` 前置守卫，而仓库不带 `config.json`、本机也从未设过该变量，测试一直
  静默跳过，所以从未暴露。
- **Markdown 报告信息头丢失客户名与项目名**（`reportx/md.go`）。docx 通道由
  `report_builder.py` 渲染封面，客户名 / 项目名齐全；纯 Go 的 md 通道只写了出具方、
  日期、版本与统计，客户名和项目名两个字段从头到尾没有被写入。抽出 `markdownHeader`
  纯函数并补 5 条单测覆盖。

### Docs

- `docs/开发环境注意事项.md` 补全本机 Go 环境的几个坑：`go vet`（必须 `-vet=off`）、
  go 命令整体变慢的实测现象与未查明的根因、跑大包测试应直接用测试二进制且 CWD 必须在包目录、
  `server` 包测试需要独立的 `ARTEX_PG_DSN` 库（含 psql 的真实路径）。


### Fixed

- **新部署不给 worker / pentest 绑定方法论 skill**（`db/db.go`）。移植进来的 6 个 PTS skill
  （fingerprint-finder / api-tester / sensitive-info-leak / route-guard-bypass /
  response-modifier / tool-arsenal）只在 `b941a95` 里落了文件，`builtinSkillVisibility`
  仅登记了 `api-recon`，`seedPlannerSkillVisibility` 也只管 planner。结果是全新部署下
  worker 与 pentest 的工具集里只有 `api-recon` —— 方法论 skill 文件在磁盘上却永远加载
  不到，等价于没装；agent 因此不会去派发需要这些方法的子任务。生产库看起来正常，是因为
  历史上有人在 UI 里手工勾选过，属幸存者偏差。
  该缺陷从 `b941a95` 起被 `skills_injection_test.go` 的断言覆盖，但那条测试有
  `ARTEX_PG_DSN` 前置守卫，而仓库不带 `config.json`、本机也从未设过该变量，测试一直
  静默跳过，所以从未暴露。
- **Markdown 报告信息头丢失客户名与项目名**（`reportx/md.go`）。docx 通道由
  `report_builder.py` 渲染封面，客户名 / 项目名齐全；纯 Go 的 md 通道只写了出具方、
  日期、版本与统计，客户名和项目名两个字段从头到尾没有被写入。抽出 `markdownHeader`
  纯函数并补 5 条单测覆盖。

### Changed

- `docs/开发环境注意事项.md` 补全本机 Go 环境的几个坑：`go vet`（必须 `-vet=off`）、
  go 命令整体变慢的实测现象与未查明的根因、跑大包测试应直接用测试二进制且 CWD 必须在包目录、
  `server` 包测试需要独立的 `ARTEX_PG_DSN` 库（含 psql 的真实路径）。


## [0.2.0] - 2026-11-20

整合 RuoJi6/ARTEX v0.3.27 与 AntiFox v2.0 的能力进 artex-ld，保留 artex-ld 自身的四个可控点。

### 合并方式

- 以 ARTEX v0.3.15（三方共同上游基线）为 base 做三方合并：`ours` = artex-ld 0.1.4，`theirs` = RuoJi6/ARTEX v0.3.27。
- 后端 44 处、前端 15 处冲突全部按并集语义解决，双方特性同时保留；模块路径统一为 `artex`（artex-ld 的本地模块名），`norma` 保持 vendored。
- 合并后补齐若干被覆盖的逻辑：`UpdateTask` 的 CASE 参数编号重排、`insertTaskDispatchTargets`（内网派发资产登记）、`egress_probe_url` 校验、`taskFromPG` 全字段镜像、`lifecycleSnapshotLocked`、建任务提示词快照链。
- 消除 `POST /api/exploration/findings/{id}/traffic` 的重复路由注册（Go ServeMux 冲突会 panic，二进制启动即崩）。

### 来自 RuoJi6/ARTEX v0.3.27

- **计划任务**：日历窗口（一次性 / 按周，北京或系统时区）自动启停任务，支持运行历史、立即运行、批量操作与备注。
- **Finding 案例化**：同一缺陷的重复上报归并为案例（分组 / 合并建议 / 人工否决 / 锚点重挂），统一报告随案例维护。
- **任务情报与凭据整理**：agent 记录凭据 / 线索 / 敏感数据（带证据与置信度），凭据处理 agent 统一整理。
- **任务级 worker 池**：按 LLM profile 绑定 worker 槽位；任务级 `worker_count` 覆盖全局并发（硬上限 50）。
- **全局资产共享开关**：`share_global_assets` 默认关闭，任务级决定是否可见全局资产。
- **代理池与出口探测**：代理池导入 / 轮换 / 健康阈值 / 故障自愈，出口 IP 探测与顶栏展示。
- **审批裁判增强**：裁判重试与置信度阈值（Jev），脚本快照不完整时强制转人工；静态脚本分析沙箱（shell / python 静态解析，**绝不执行**）。
- **Jev / System One 协议**：可绑定到审批裁判专用（不能当对话 / 任务模型）。
- **HTTP 请求重放**：Burp 式 repeater（编辑 / 历史 / 过滤 / SSRF 防护）。
- **任务并发上限**、**任务备注**、**真实运行时长**、**暂停原因语义**、**系统指标**、**自动标题**、**DeepSeek Search API**、**LLM 请求限速**、共享 `llmhttp` 出口客户端。

### 来自 AntiFox v2.0（证据驱动交付）

- **复现过程就地落库**：`findings.reproduction` 保存「入口点 / 前置条件 / 有序步骤 / 来源」，agent 提示词要求逐字写真实请求与响应。
- **自动关联真实流量包**：登记时把每一步解析到当次真实录制的 MITM 报文（method+path 定位，query/body/响应特征逐级收窄且不会把匹配打成 0），报告与发现页可一键跳转。
- **确认即富化**：确认后自动生成结构化富化（描述 / 风险 / 修复建议 / CVSS 3.1），可人工编辑；漏洞证据截图按步骤序号挂载。
- **交付级报告**：新增 `reportx` 包（`.docx` 封面 / 目录 / 页码 / 漏洞属性表 + Markdown 交付包），**简体中文 / 繁体中文 / 英文**三语；出具方设置、Logo、测试人员、可复用模版与报告登记。`reportx` 已解耦 AntiFox 重写过的 `engine/` 内核，改用本仓库的 `agent.Config → llm.Provider → Complete` 单轮调用。
- **知识库两层 RAG**：全局通用知识 + 本任务专属资料；文档抽取 / 分块 / embedding / 检索，`search_knowledge` 与 `list_knowledge_categories` 已挂到 worker 与 planner 工具集。pgvector 为可选增强，缺失时**自动降级为关键词检索**，不阻塞启动。

### 前端

- 报告管理（`/system/reports`）与知识库（`/system/knowledge`）两页移植落地；漏洞详情新增结构化复现过程分步、每步证据截图与跳真实流量包。
- 新增本地文案表 `lib/i18n-lite.ts` 承接 AntiFox 的 i18next 调用，**不新增前端依赖**。
- 移除合并遗留的重复导入 / 重复定义，统一 `statusTone` / `fmtBytes` / `fmtTokens` 到共享 utils；恢复 `openSSE` 与 `@dagrejs/dagre`（拓扑编辑器自动布局）。

### 验证

- `go build ./...`、`go vet ./...` 通过；`go test ./...` 相对两个上游基线（干净库）**零新增回归**。
- `npx tsc --noEmit` 0 error；`npm run build` 通过，`/system/reports` 与 `/system/knowledge` 进入产物。
- 二进制带内嵌前端启动正常；知识库建档 → 检索 → 分类枚举正常；交付级报告生成 → 下载 → 登记表正常，中英文两版内容均正确。

## [0.1.4] - 2026-11-05

### Finding 显式编排

- `spawn_task` 子任务改用独立探索链，不再创建 `source_task_ids` 来源关系；带 `finding_id` 时仅在任务创建事务内复制该 Finding 沿 `yields/derived_from` 反向可达的 `intent/fact/finding` 祖先子图，重映射 origin、节点、边和锚点，复制 intent 固定为 `done`，父 Finding 记录与无关分支保持不变。工具 Schema 和默认 Dispatcher 提示词提供幂等迁移，自定义提示词不覆盖。
- 全部系统工具及 `reporter`、`vuln_dispatcher`、`retester`、`trace_cleaner` 均由代码幂等 seed，首次启动即可使用，不再依赖导入 `config-export`；数据库中的后续修改与删除仍保留。MCP 连接、凭据和可见性继续由用户配置。
- `trace_cleaner` 新增事务化 Agent、默认提示词、4 项工具绑定及显式派发触发器 seed，避免部分初始化；其全局 `on_finding` 默认关闭。

- 新增 Planner 专用 `dispatch_finding_to_agent` 工具：可将当前任务的独立漏洞记录（findings.id）显式派发给任意已启用、非内置的自定义 Agent；具体场景目标和证据门槛由系统提示词约束。
- `vuln_dispatcher` 与 `trace_cleaner` 默认关闭全局 `on_finding` 订阅，改由“外网渗透（内置）”Planner 在确认立足点或仅漏洞路径证据后显式派发；Worker 内置提示词同步强化立足点证据规则。
- 外网 Planner 的 Finding 派发提示词改为可扩展的 Agent 清单和独立规则块，明确 `vuln_dispatcher` 与 `trace_cleaner` 不互斥、清理确认不属于派发前提，避免已取得立足点且存在 `[投放]` 事实时漏派清理审计。
- 新增 `finding_agent_invocations` 持久记录、Conversation 回填和 10 分钟原子补发；合并 Trigger Run 会回填全部 Finding，重复调用返回首次 Conversation ID。
- 新增外网内置 Planner/Worker 提示词、默认任务快照及旧 Dispatcher/Trace Cleaner 触发配置的幂等迁移；自定义模板和自定义任务快照保持不变。
- AI 预审 `note` 改为单条意图的完整中文说明，禁止 ID/分号堆叠；保持 JSON 契约、200 字上限和既有展示方式。
- Worker WebShell 上传要求使用与目标技术栈匹配的正常应用文件名，禁止攻击特征/随机命名及覆盖既有文件，并保持落地路径、`shell_add` 与 `[投放]` 记录一致。
- 外网渗透场景默认关闭意图审核；存量分类一次性迁移，之后手动重开不会被重启覆盖，其他场景默认值不变。

### 修复与发布

- Docker 源码构建恢复 `ARTEX_VERSION` → `main.version` 注入，当前发布版本统一为 **0.1.4**，恢复健康检查版本展示和更新比较能力；运行镜像补齐 `start.sh` 启动必需的 `scripts/artex-config.sh` 与升级 SQL，避免入口脚本启动即退出。
- 拓扑自动整理与 C 段归组增加同步防重入、错误提示与失败重载；归组标记读写容错，手动归组后不再被意外自动归回。
- 人工预审 `pass` / `revise` 的 `note` 统一为必填；空值不进入预审结果，合法理由保留 trim 后内容，自动处置契约不变。
- Agent 触发列表对 Planner 显式派发消息模板显示 `工具调用(dispatch_finding_to_agent)`，不再误显示“无条件”；不额外开启 Scheduler `on_tool_call`，避免重复入队。
- 补充定向回归测试：`spawn_task` 独立探索链验证子任务不创建 `source_task_ids` 来源关系、仅复制触发 Finding 的 intent/fact/finding 血缘链；`vuln_dispatcher` 独立探索链提示词迁移验证默认血统切换、重入幂等与自定义提示词不覆盖。

### 安全与发布

- `.dockerignore` 收紧构建上下文：`Dockerfile.source` 的 `COPY . .` 不再收进 `*.key`、`*.pem`、`config-export/`、`backup/` 与 `/dev/`，避免密钥和业务数据进入镜像构建层。
- `config-export/` 导出快照含密钥明文，改由 `.gitignore` 忽略、不再入库（配置种子已内置源码，见上文幂等 seed）；仓库同步移除该示例目录。

## [0.1.3] - 2026-11-04

### 新增

- **内网拓扑图**：任务详情新增资产覆盖图 / 内网拓扑切换视图，配套拓扑画布、编辑工具栏与分组布局，数据表与 API 路由同步落地。

### 变更

- **部署包内置场景预设**：Release 包自带 `config-export/` 编排配置，支持导入场景分类、planner 模板与审核触发器。
- **代码优化**：例行重构与清理，提升可维护性。
- **文档优化**：README 重写为快速开始，部署手册与功能详解同步更新。

## [0.1.2] - 2026-10-30

### 二开功能

- **意图审核流**：新增待审意图、review agent 预审、人工批准 / 驳回、修订重送、乐观锁与可选自动处置；预审建议不会在人工全审模式下直接放行。
- **planner 模板与场景预设**：新增模板库、创建时快照、分类默认模板 / 审核 / 种子意图 / 执行器，以及任务级 worker 并发。
- **自定义执行器**：自定义 agent 可声明 `executor_key`，由种子意图或送审 / 修订时的 `target_executor` 路由；失效时告警并回退 worker。
- **finding 后编排**：通用触发器支持运行策略与水位线；首启播种 `vuln_dispatcher` 和 `reporter`，支持派生场景子任务和自动报告。
- **证据交接**：`report_finding` 支持 `evidence_hint_id`，报告流程按 `evidence_version` 防止旧证据覆盖新报告。
- **可观测性与迁移**：补充关键链路日志、日志级别热切换、访问日志，以及 19 类业务配置的导出 / 幂等导入和提示词升级命令。

### 安全与发布

- 新增首启安装令牌、文件工具任务目录边界与 `python_interpreter` 校验。
- Docker Compose 改为通过 `Dockerfile.source` 从当前二开源码构建，避免误拉上游镜像；JWT key 持久化到数据目录。
- 新增脱敏 `config-export/` 示例包：不含 LLM 档和密钥，MCP 默认禁用；`retester` / `trace_cleaner` 是可选配置，不冒充源码 seed。
- 修正 review 种子提示词与升级 SQL，使“预审只写建议、人工或自动处置才改状态”的文案和代码行为一致。

### 执行模式与任务编排

- 新增 `foothold` / `vulnerability_only` 双执行模式，由后端根据任务目标和描述识别，并注入 Goals、Planner 与漏洞派发上下文。
- 新增 Worker 场景模板、任务级 Worker 提示词快照、场景默认模板和模板校验接口；历史任务保留创建时的执行上下文。
- 新增宿主资产视图、内网任务拓扑、资产派发记录和同资产 Finding 接续能力，减少重复任务链。
- 内网渗透子任务名称由服务端统一生成为 `内网渗透·<目标资产>`，不再依赖模型自觉填写，也不再把长任务摘要作为任务名。

### 提示词与漏洞派发

- 重构 Goals、Planner、Worker、Pentest、Auto、Vuln Dispatcher 和 Trace Cleaner 提示词，统一目标意图、证据验收、投放登记和清理规则。
- GetShell 证据验收区分完整 Shell 与单次命令执行：单次 RCE 回显不再直接等同于取得立足点。
- 漏洞派发仅接受明确的 `foothold` 路径，并要求 `critical/high` 与 WebShell、交互式 Shell 或可用 C2 等实证；仅 RCE、文件上传、信息泄露、未授权接口、弱口令和模糊猜测不创建内网渗透任务。
- 暂停独立漏洞利用和权限提升场景的自动派发；权限提升只作为已创建内网渗透子任务中的内部步骤。
- 派发判断继续服从父任务授权范围。取得 foothold 不会覆盖“禁止内网扫描、横向移动、代理或隧道”等明确限制。

### Review 与前端

- Review 会话恢复原有上下布局，不再把审核操作框错误显示到 Planner、系统审计和普通 Worker 会话。
- 待审队列由多卡片纵向滚动改为 `1、2、3…` 编号切换，一次只显示当前待审意图；保留通过、驳回、修订和 Review 对话。
- 修复会话内容区 flex 高度和静态导出缓存问题，确保部署的前端资源与源码一致。

### MCP 与配置迁移

- `start.sh` 支持 MCP 本地程序、插件和配置目录随包导出；记录文件 SHA256、大小和执行权限。
- 导入时重写 `{{MCP_ROOT}}` 路径、逐文件校验并恢复可执行位；可用 `ARTEX_IMPORT_MCP_BIN=0` 仅导入 MCP 配置。
- 配置包包含 Agent、提示词、触发器、工具绑定、任务分类、Worker 模板和 MCP 配置；同步/Release 配置包不包含 LLM API Key。
- 部署文档统一为源码编译和 Docker 两种方式，并分别提供配置导入命令。

### 构建与源码修复

- 恢复 `evidence/store.go`、`evidence/store_test.go` 和 `evidence/testmain_test.go`，解决源码仍引用 evidence 包但目录缺失导致的构建阻塞。
- 明确前端静态构建使用 `NEXT_EXPORT=1`；清理旧 `.next/out` 后重建，避免旧 Review 分栏资源混入新部署包。
- 修复漏洞派发提示词导入时的 PostgreSQL 角色环境问题，导入失败使用单文件事务回滚，成功后再重启验证。

### 数据库与升级注意事项

- 新增 Worker 模板、资产派发和拓扑相关表及字段；升级前必须备份 PostgreSQL。
- 配置导入不会导入 LLM Key，也不会替代数据库备份；已有任务的历史提示词快照不会被自动改写。
- 二进制升级、配置导入和提示词升级是不同操作，应分别执行并验证。

### 验证

- 子任务命名定向 Go 测试通过；前端 TypeScript 和静态构建通过。
- Chrome 验证主 Agent、Planner、系统审计和 Worker 不显示审核框；Review 编号切换、通过、对话和移动布局通过。
- Linux 二进制部署、健康检查、首页和嵌入前端资源断言通过。
- 完整 `go test ./server` 需要有效的 `ARTEX_PG_DSN`；本次环境未执行完整数据库测试。Docker 部署链路已核读，但未在本次 Windows 环境实机构建镜像。

## [0.3.18] - 2026-10-12

### 审批审查

- 新增脚本审查快照，向审查模型提供直接引用脚本的路径、大小、SHA-256 和源码；快照不完整时强制转人工审批。
- 新增审批旁路问答会话，支持基于审批上下文连续追问，且旁路模型不能执行工具或改变审批状态。
- 新增审批记录全文检索、审批编号精确定位、日期范围筛选及状态、判定来源组合筛选。
- 修复审批详情脚本区域裁切、脚本展开状态显示和旁路面板关闭后的聊天布局问题。

## [0.3.17] - 2026-10-10

### 计划任务

- 新增日历式计划任务，支持按时间规则创建、编辑、启停和删除任务。
- 任务列表支持查看计划状态与下一次运行时间，并补充计划任务管理页面。
- 支持按日期范围筛选计划任务、日历式运行时间线和批量选择操作。
- 优化立即运行、暂停来源与状态颜色展示，并在计划状态变化后刷新时间线。

## [0.3.16] - 2026-10-09

### 访问控制

- 新增可选的 HTTP Basic Auth 访问控制，可在系统设置中配置，并修复配置持久化占用请求锁的问题。

### 同一漏洞的重复上报归并

- Reporter 在生成独立报告后按同任务候选证据判断同一实际缺陷，明确重复时归并，证据不足时提交人工确认建议。
- 新增可展开漏洞文件夹、统一报告、原始等级分布、独立漏洞/上报记录统计及归并历史；每条原报告、证据和流量继续保留。
- 支持手动整理所选历史记录、移出成员与防止自动再次归并，继承任务保持只读。
- 统一报告采用证据版本校验；导出默认统一报告，可包含原始子报告，未生成或过期时明确提示并保留原材料。

## [0.3.15] - 2026-10-07

### 部署与发布维护

### Finding 显式编排

- `spawn_task` 子任务改用独立探索链，不再创建 `source_task_ids` 来源关系；带 `finding_id` 时仅在任务创建事务内复制该 Finding 沿 `yields/derived_from` 反向可达的 `intent/fact/finding` 祖先子图，重映射 origin、节点、边和锚点，复制 intent 固定为 `done`，父 Finding 记录与无关分支保持不变。工具 Schema 和默认 Dispatcher 提示词提供幂等迁移，自定义提示词不覆盖。
- 全部系统工具及 `reporter`、`vuln_dispatcher`、`retester`、`trace_cleaner` 均由代码幂等 seed，首次启动即可使用，不再依赖导入 `config-export`；数据库中的后续修改与删除仍保留。MCP 连接、凭据和可见性继续由用户配置。
- `trace_cleaner` 新增事务化 Agent、默认提示词、4 项工具绑定及显式派发触发器 seed，避免部分初始化；其全局 `on_finding` 默认关闭。

- 新增 Planner 专用 `dispatch_finding_to_agent` 工具：可将当前任务的独立漏洞记录（findings.id）显式派发给任意已启用、非内置的自定义 Agent；具体场景目标和证据门槛由系统提示词约束。
- `vuln_dispatcher` 与 `trace_cleaner` 默认关闭全局 `on_finding` 订阅，改由“外网渗透（内置）”Planner 在确认立足点或仅漏洞路径证据后显式派发；Worker 内置提示词同步强化立足点证据规则。
- 外网 Planner 的 Finding 派发提示词改为可扩展的 Agent 清单和独立规则块，明确 `vuln_dispatcher` 与 `trace_cleaner` 不互斥、清理确认不属于派发前提，避免已取得立足点且存在 `[投放]` 事实时漏派清理审计。
- 新增 `finding_agent_invocations` 持久记录、Conversation 回填和 10 分钟原子补发；合并 Trigger Run 会回填全部 Finding，重复调用返回首次 Conversation ID。
- 新增外网内置 Planner/Worker 提示词、默认任务快照及旧 Dispatcher/Trace Cleaner 触发配置的幂等迁移；自定义模板和自定义任务快照保持不变。
- AI 预审 `note` 改为单条意图的完整中文说明，禁止 ID/分号堆叠；保持 JSON 契约、200 字上限和既有展示方式。
- Worker WebShell 上传要求使用与目标技术栈匹配的正常应用文件名，禁止攻击特征/随机命名及覆盖既有文件，并保持落地路径、`shell_add` 与 `[投放]` 记录一致。
- 外网渗透场景默认关闭意图审核；存量分类一次性迁移，之后手动重开不会被重启覆盖，其他场景默认值不变。
### 拦截

### 修复与发布

- Docker 源码构建恢复 `ARTEX_VERSION` → `main.version` 注入，当前发布版本统一为 **0.1.4**，恢复健康检查版本展示和更新比较能力；运行镜像补齐 `start.sh` 启动必需的 `scripts/artex-config.sh` 与升级 SQL，避免入口脚本启动即退出。
- 拓扑自动整理与 C 段归组增加同步防重入、错误提示与失败重载；归组标记读写容错，手动归组后不再被意外自动归回。
- 人工预审 `pass` / `revise` 的 `note` 统一为必填；空值不进入预审结果，合法理由保留 trim 后内容，自动处置契约不变。
- Agent 触发列表对 Planner 显式派发消息模板显示 `工具调用(dispatch_finding_to_agent)`，不再误显示“无条件”；不额外开启 Scheduler `on_tool_call`，避免重复入队。
- 补充定向回归测试：`spawn_task` 独立探索链验证子任务不创建 `source_task_ids` 来源关系、仅复制触发 Finding 的 intent/fact/finding 血缘链；`vuln_dispatcher` 独立探索链提示词迁移验证默认血统切换、重入幂等与自定义提示词不覆盖。

### 安全与发布

- `.dockerignore` 收紧构建上下文：`Dockerfile.source` 的 `COPY . .` 不再收进 `*.key`、`*.pem`、`config-export/`、`backup/` 与 `/dev/`，避免密钥和业务数据进入镜像构建层。
- `config-export/` 导出快照含密钥明文，改由 `.gitignore` 忽略、不再入库（配置种子已内置源码，见上文幂等 seed）；仓库同步移除该示例目录。


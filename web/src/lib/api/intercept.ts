// intercept 域：全局拦截规则、资产拦截黑名单、人工/模型审批（pending/history/
// task 维度）、全局工具拦截范围与 LLM 兜底审批配置。
/**
 * 主要函数/类型：
 *   - interceptApi / InterceptApi：拦截域 API 聚合对象及其类型
 *   - interceptRules 系列：全局拦截规则 CRUD 与开关
 *   - assetInterceptRules 系列：资产拦截黑名单 CRUD 与开关
 *   - interceptPending/interceptDecide/interceptHistory/interceptTask 系列：人工/模型审批查询与决定
 *   - interceptGetToolConfig/interceptSetToolConfig：全局工具拦截范围
 *   - interceptGetJudgeConfig/interceptSetJudgeConfig/interceptJudgeUsage：LLM 兜底审批配置与用量
 */

import { arr, del, get, interceptPageQuery, post, put } from "@/lib/api/http";
import { MOCK } from "@/lib/mock/enabled";
import type {
  AssetInterceptRule,
  InterceptApprovalFilter,
  InterceptApprovalRow,
  InterceptDetail,
  InterceptRuleStat,
  InterceptPending,
  InterceptRule,
  JudgeConfig,
  JudgeUsage,
} from "@/lib/types";

export const interceptApi = {
  // ---- intercept rules ----
  interceptRules: () => get<{ rules: InterceptRule[] }>("/intercept/rules").then((r) => arr(r.rules)),
  createInterceptRule: (rule: Omit<InterceptRule, "id" | "created_at" | "updated_at">) =>
    post<InterceptRule>("/intercept/rules", rule),
  updateInterceptRule: (id: number, rule: Omit<InterceptRule, "id" | "created_at" | "updated_at">) =>
    put<InterceptRule>(`/intercept/rules/${id}`, rule),
  deleteInterceptRule: (id: number) => del<{ deleted: number }>(`/intercept/rules/${id}`),
  toggleInterceptRule: (id: number, enabled: boolean) =>
    post<{ ok: boolean; enabled: boolean }>(`/intercept/rules/${id}/toggle`, { enabled }),

  // ---- asset intercept rules（资产拦截：全局黑名单） ----
  assetInterceptRules: () => get<{ rules: AssetInterceptRule[] }>("/asset-intercept/rules").then((r) => arr(r.rules)),
  createAssetInterceptRule: (rule: Pick<AssetInterceptRule, "enabled" | "kind" | "pattern" | "note">) =>
    post<AssetInterceptRule>("/asset-intercept/rules", rule),
  updateAssetInterceptRule: (id: number, rule: Pick<AssetInterceptRule, "enabled" | "kind" | "pattern" | "note">) =>
    put<AssetInterceptRule>(`/asset-intercept/rules/${id}`, rule),
  deleteAssetInterceptRule: (id: number) => del<{ deleted: number }>(`/asset-intercept/rules/${id}`),
  toggleAssetInterceptRule: (id: number, enabled: boolean) =>
    post<{ ok: boolean; enabled: boolean }>(`/asset-intercept/rules/${id}/toggle`, { enabled }),

  // ---- intercept pending (ask) ----
  interceptPending: () => get<{ pending: InterceptPending[] }>("/intercept/pending").then((r) => arr(r.pending)),
  interceptGetOne: (id: number) => get<InterceptPending>(`/intercept/pending/${id}`),
  interceptDecide: (id: number, decision: "allowed" | "denied") =>
    post<{ ok: boolean }>(`/intercept/pending/${id}/decide`, { decision }),
  interceptExecution: (id: number, conversationId?: number) =>
    get<import("@/lib/types").InterceptExecution>(
      `/intercept/history/${id}/execution${conversationId ? `?conversation=${conversationId}` : ""}`,
    ),
  interceptDetail: (id: number) => get<InterceptDetail>(`/intercept/history/${id}`),
  interceptHistory: () => get<{ items: InterceptApprovalRow[] }>("/intercept/history").then((r) => arr(r.items)),
  // 按规则聚合的命中数：与列表共用同一套筛选（q/from/to/status/decision_source），
  // 保证"筛完看到的条目"与"统计出来的数"口径一致。
  interceptHistoryStats: (filter: InterceptApprovalFilter = {}) =>
    get<{ rules: InterceptRuleStat[] }>(`/intercept/history/stats?${interceptPageQuery(1, 1, filter)}`).then(
      (r) => arr(r.rules),
    ),
  interceptHistoryPage: (page = 1, size = 20, filter: InterceptApprovalFilter = {}) =>
    get<{ items: InterceptApprovalRow[]; total?: number }>(
      `/intercept/history?${interceptPageQuery(page, size, filter)}`,
    ).then((r) => ({
      items: arr(r.items),
      total: r.total ?? r.items?.length ?? 0,
    })),
  interceptTask: (taskId: string) =>
    get<{ items: InterceptApprovalRow[] }>(`/intercept/task/${taskId}`).then((r) => arr(r.items)),
  interceptTaskPage: (taskId: string, page = 1, size = 20, filter: InterceptApprovalFilter = {}) =>
    get<{ items: InterceptApprovalRow[]; total?: number }>(
      `/intercept/task/${encodeURIComponent(taskId)}?${interceptPageQuery(page, size, filter)}`,
    ).then((r) => ({
      items: arr(r.items),
      total: r.total ?? r.items?.length ?? 0,
    })),

  // ---- intercept tool-config (全局工具拦截范围) ----
  interceptGetToolConfig: async (): Promise<{ enabled_tools: string[] }> => {
    if (MOCK) return { enabled_tools: ["bash"] };
    // 走 get()：401 自动跳登录、错误消息取后端文案（原先裸 fetch 只抛状态码）
    return get<{ enabled_tools: string[] }>("/intercept/tool-config");
  },
  interceptSetToolConfig: async (enabledTools: string[]): Promise<void> => {
    if (MOCK) return;
    await put<{ ok: boolean }>("/intercept/tool-config", { enabled_tools: enabledTools });
  },

  // ---- intercept LLM judge (模型兜底审批,全局配置) ----
  interceptGetJudgeConfig: () => get<JudgeConfig>("/intercept/judge"),
  interceptSetJudgeConfig: (cfg: JudgeConfig) => put<{ ok: boolean }>("/intercept/judge", cfg),
  interceptJudgeUsage: (days = 30) => get<JudgeUsage>(`/intercept/judge/usage?days=${days}`),

  // ---- 合并自 RuoJi6/ARTEX v0.3.27 ----
  setBasicAuthSettings: (settings: { enabled: boolean; username: string; password: string }) =>
    put<{ enabled: boolean; username: string; password_set: boolean }>("/settings/basic-auth", settings),

  // ---- 合并自 RuoJi6/ARTEX v0.3.27 ----
  basicAuthSettings: () => get<{ enabled: boolean; username: string; password_set: boolean }>("/settings/basic-auth"),
};

export type InterceptApi = typeof interceptApi;

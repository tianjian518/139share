import { api, type ApiResponse } from '@/utils/api'

// 系统初始化请求接口
export interface InitSystemRequest {
  baseURL: string // 系统基础URL
  enableAuth: boolean // 是否启用认证
  superUsername: string // 超级管理员用户名，长度3-20位
  superPassword: string // 超级管理员密码，长度6-20位
  title: string // 系统标题
}

// 修改系统标题请求接口
export interface ModifyTitleRequest {
  title: string // 新的系统标题
}

// 修改系统基础URL请求接口
export interface ModifyBaseURLRequest {
  baseURL: string // 新的系统基础URL
}

// ===== 系统设置接口 =====

// 获取系统信息
export const getSystemInfo = (): Promise<ApiResponse<Models.SystemInfo>> => {
  return api.get('/setting/info').then((res) => res.data)
}

// 初始化系统
export const initSystem = (data: InitSystemRequest): Promise<ApiResponse> => {
  return api.post('/setting/init_system', data).then((res) => res.data)
}

// 修改系统标题（管理员权限）
export const modifySystemTitle = (title: string): Promise<ApiResponse> => {
  const data: ModifyTitleRequest = { title }
  return api.post('/setting/modify_title', data).then((res) => res.data)
}

// 修改系统基础URL（管理员权限）
export const modifySystemBaseURL = (baseURL: string): Promise<ApiResponse> => {
  const data: ModifyBaseURLRequest = { baseURL }
  return api.post('/setting/modify_base_url', data).then((res) => res.data)
}

// 切换系统鉴权开关（管理员权限）
export interface ToggleEnableAuthRequest {
  enableAuth: boolean // 是否启用鉴权
}

export const toggleSystemEnableAuth = (enableAuth: boolean): Promise<ApiResponse> => {
  const data: ToggleEnableAuthRequest = { enableAuth }
  return api.post('/setting/toggle_enable_auth', data).then((res) => res.data)
}

// 查询系统附加设置（仅登录用户）
export const getSettingAddition = (): Promise<ApiResponse<Models.SettingAddition>> => {
  return api.get('/setting/addition').then((res) => res.data)
}

// 可选修改系统附加设置（管理员权限）
export interface ModifySettingAdditionRequest {
  localProxy?: boolean
  multipleStream?: boolean
  multipleStreamThreadCount?: number
  multipleStreamChunkSize?: number
  taskThreadCount?: number
}

export const modifySettingAddition = (data: ModifySettingAdditionRequest): Promise<ApiResponse> => {
  return api.post('/setting/modify_addition', data).then((res) => res.data)
}

// ===== 移动云盘（139）凭据配置 =====

export interface Yun139Conf {
  account: string // 139 账号（手机号）
  authorization: string // 139 授权串（不含 Basic 前缀）
  configured: boolean // 是否已配置可用凭据
}

export interface ModifyYun139ConfRequest {
  account?: string
  authorization?: string
  clear?: boolean // 传 true 清空已保存凭据
}

// 查询 139 凭据配置
export const getYun139Conf = (): Promise<ApiResponse<Yun139Conf>> => {
  return api.get('/setting/yun139_conf').then((res) => res.data)
}

// 保存 139 凭据配置（立即生效，无需重启）
export const modifyYun139Conf = (
  data: ModifyYun139ConfRequest
): Promise<ApiResponse<Yun139Conf>> => {
  return api.post('/setting/modify_yun139_conf', data).then((res) => res.data)
}

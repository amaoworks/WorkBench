export const FUTU_SERVICE_LABELS = {
  installing: "正在安装服务",
  starting: "正在启动服务",
  running: "正在连接行情",
  stopping: "正在停止服务",
  error: "服务启动失败",
  stopped: "服务已停止",
  external: "正在等待服务连接",
} as const;

export type FutuServiceState = keyof typeof FUTU_SERVICE_LABELS;

export function futuServiceLabel(state: string): string {
  return state in FUTU_SERVICE_LABELS ? FUTU_SERVICE_LABELS[state as FutuServiceState] : "状态未知";
}

export function futuConnectionDetail(state: { connected: boolean; qotLogined: boolean; serviceState: string }): string {
  if (!state.connected) return futuServiceLabel(state.serviceState);
  return state.qotLogined ? "行情已登录。" : "服务已启动，等待行情登录。";
}
